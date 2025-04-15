package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pingPeriod = (writeWait * 9) / 10
)

// Message представляет структуру сообщения
type Message struct {
	Type    string `json:"type"`
	From    string `json:"from"`
	To      string `json:"to"`
	Content string `json:"content"`
}

// Client представляет подключенного клиента
type Client struct {
	conn     *websocket.Conn
	username string
	token    string
}

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	// Хранилище активных клиентов
	clients = make(map[string]*Client)
	// Менеджер аутентификации
	authManager = NewAuthManager()
	mu          sync.Mutex
)

// создание нового токена аутентификации
func generateToken(username string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.URLEncoding.EncodeToString(b)
	return token, nil
}

// validateToken проверяет валидность токена
func validateToken(token string) (string, error) {
	return authManager.ValidateToken(token)
}

// registerUser регистрирует нового пользователя
func registerUser(username, password string) error {
	return authManager.RegisterUser(username, password)
}

// authenticateUser проверяет учетные данные пользователя
func authenticateUser(username, password string) (string, error) {
	return authManager.Login(username, password)
}

func handleWebSocket(conn *websocket.Conn) {
	// Устанавливаем таймаут для чтения
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))

	// Устанавливаем обработчик пингов
	conn.SetPingHandler(func(appData string) error {
		return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(writeWait))
	})

	// Создаем тикер для отправки пингов
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	// Запускаем горутину для отправки пингов
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ticker.C:
				conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					log.Printf("Ошибка отправки ping: %v", err)
					return
				}
			case <-done:
				return
			}
		}
	}()

	// Основной цикл обработки сообщений
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("Ошибка чтения сообщения: %v", err)
			close(done)
			break
		}

		// Парсим сообщение
		var msg Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Ошибка парсинга сообщения: %v", err)
			continue
		}

		// Обрабатываем разные типы сообщений
		switch msg.Type {
		case "register":
			err := authManager.RegisterUser(msg.From, msg.Content)
			if err != nil {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"`+err.Error()+`"}`))
				continue
			}
			conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"success","content":"Регистрация успешна"}`))

		// Вход в существующий аккаунт
		case "login":
			token, err := authManager.Login(msg.From, msg.Content)
			if err != nil {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"`+err.Error()+`"}`))
				continue
			}

			mu.Lock()
			clients[msg.From] = &Client{conn: conn, username: msg.From, token: token}
			mu.Unlock()

			conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"success","content":"`+token+`"}`))
			log.Printf("Пользователь %s вошел в систему", msg.From)

		case "message":
			// Отправка сообщения конкретному клиенту
			mu.Lock()
			recipient, exists := clients[msg.To]
			mu.Unlock()

			if exists {
				recipient.conn.SetWriteDeadline(time.Now().Add(writeWait))
				if err := recipient.conn.WriteMessage(websocket.TextMessage, message); err != nil {
					log.Printf("Ошибка отправки сообщения: %v", err)
				}
			} else {
				log.Printf("Получатель %s не найден", msg.To)
			}
		}

		// Обновляем таймаут после каждого успешного чтения
		conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	}
}

func HandleConnection(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Ошибка обновления соединения: %v", err)
		return
	}

	// Запускаем обработку соединения в отдельной горутине
	go handleWebSocket(conn)
}

func handleRegister(w http.ResponseWriter, r *http.Request) {
	var user User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if err := authManager.RegisterUser(user.Username, user.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "User registered successfully"})
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	var user User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	token, err := authManager.Login(user.Username, user.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"token": token})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token == "" {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}

	if err := authManager.Logout(token); err != nil {
		http.Error(w, "Ошибка при выходе из системы", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Успешный выход из системы"})
}

func handleValidateToken(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token == "" {
		http.Error(w, "Требуется авторизация", http.StatusUnauthorized)
		return
	}

	username, err := authManager.ValidateToken(token)
	if err != nil {
		http.Error(w, "Недействительный токен", http.StatusUnauthorized)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"username": username})
}

func Handler(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "index.html")
}
