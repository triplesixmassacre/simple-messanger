package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"messanger/internal/auth"
	"messanger/internal/database"
	"messanger/internal/models"

	"github.com/gorilla/websocket"
)

const (
	writeWait  = 10 * time.Second
	pingPeriod = (writeWait * 9) / 10
)

// Client представляет подключенного клиента
type Client struct {
	Conn     *websocket.Conn
	Username string
	Token    string
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
	authManager = auth.NewAuthManager()
	// Подключение к базе данных
	db *database.Database
	mu sync.Mutex
)

func init() {
	var err error
	db, err = database.NewDatabase("mongodb://localhost:27017")
	if err != nil {
		log.Fatalf("Ошибка подключения к базе данных: %v", err)
	}
}

// GenerateToken создает новый токен аутентификации
func GenerateToken(username string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.URLEncoding.EncodeToString(b)
	return token, nil
}

// ValidateToken проверяет валидность токена
func ValidateToken(token string) (string, error) {
	return authManager.ValidateToken(token)
}

// RegisterUser регистрирует нового пользователя
func RegisterUser(username, password string) error {
	return authManager.RegisterUser(username, password)
}

// AuthenticateUser проверяет учетные данные пользователя
func AuthenticateUser(username, password string) (string, error) {
	return authManager.Login(username, password)
}

func handleWebSocket(conn *websocket.Conn) {
	// Устанавливаем таймаут для чтения
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// Устанавливаем обработчик пингов
	conn.SetPingHandler(func(appData string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
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
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
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
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("Ошибка чтения сообщения: %v", err)
			}
			close(done)
			break
		}

		// Парсим сообщение
		var msg models.Message
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Ошибка парсинга сообщения: %v", err)
			continue
		}

		// Обрабатываем разные типы сообщений
		switch msg.Type {
		case "register":
			// Проверяем существование пользователя в базе данных
			_, err := db.GetUser(msg.From)
			if err == nil {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"Пользователь уже существует"}`))
				continue
			}

			// Регистрируем пользователя в базе данных
			if err := db.RegisterUser(msg.From, msg.Content); err != nil {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"`+err.Error()+`"}`))
				continue
			}

			// Регистрируем пользователя в AuthManager
			if err := authManager.RegisterUser(msg.From, msg.Content); err != nil {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"`+err.Error()+`"}`))
				continue
			}

			conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"success","content":"Регистрация успешна"}`))

		case "login":
			// Проверяем учетные данные в базе данных
			user, err := db.GetUser(msg.From)
			if err != nil {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"Пользователь не найден"}`))
				continue
			}

			if user.Password != msg.Content {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"Неверный пароль"}`))
				continue
			}

			mu.Lock()
			// Проверяем, не существует ли уже соединение для этого пользователя
			if existingClient, exists := clients[msg.From]; exists {
				// Закрываем существующее соединение
				existingClient.Conn.Close()
			}
			clients[msg.From] = &Client{Conn: conn, Username: msg.From, Token: msg.Content}
			mu.Unlock()

			// Отправляем подтверждение успешного входа
			response := models.Message{
				Type:    "success",
				Content: "Успешный вход в систему",
			}
			responseBytes, _ := json.Marshal(response)
			if err := conn.WriteMessage(websocket.TextMessage, responseBytes); err != nil {
				log.Printf("Ошибка отправки подтверждения входа: %v", err)
				continue
			}

			// Отправляем непрочитанные сообщения
			go func() {
				messages, err := db.GetUnreadMessages(msg.From)
				if err != nil {
					log.Printf("Ошибка получения непрочитанных сообщений: %v", err)
					return
				}

				for _, msg := range messages {
					messageBytes, _ := json.Marshal(models.Message{
						Type:    "message",
						From:    msg.From,
						To:      msg.To,
						Content: msg.Content,
					})
					conn.WriteMessage(websocket.TextMessage, messageBytes)
				}
			}()

			// Отправляем список чатов пользователя
			go func() {
				chats, err := db.GetUserChats(msg.From)
				if err != nil {
					log.Printf("Ошибка получения чатов: %v", err)
					return
				}

				for _, chat := range chats {
					chatMsg := models.Message{
						Type:    "chat",
						Content: chat.ID,
						From:    chat.Users[0],
						To:      chat.Users[1],
					}
					if chat.LastMessage != nil {
						chatMsg.Content = chat.LastMessage.Content
					}
					chatBytes, _ := json.Marshal(chatMsg)
					conn.WriteMessage(websocket.TextMessage, chatBytes)

					// Загружаем историю сообщений для каждого чата
					messages, err := db.GetChatMessages(chat.ID, 50)
					if err != nil {
						log.Printf("Ошибка получения истории чата %s: %v", chat.ID, err)
						continue
					}

					// Отправляем историю сообщений
					for _, msg := range messages {
						historyMsg := models.Message{
							Type:    "message",
							From:    msg.From,
							To:      msg.To,
							Content: msg.Content,
							ChatID:  msg.ChatID,
						}
						historyBytes, _ := json.Marshal(historyMsg)
						conn.WriteMessage(websocket.TextMessage, historyBytes)
					}
				}
			}()

			log.Printf("Пользователь %s вошел в систему", msg.From)

		case "delete_chat":
			// Получаем информацию о чате
			chat, err := db.GetChat(msg.ChatID)
			if err != nil {
				log.Printf("Ошибка получения информации о чате: %v", err)
				continue
			}

			// Отправляем уведомление об удалении чата всем участникам
			deleteMsg := models.Message{
				Type:   "chat_deleted",
				ChatID: msg.ChatID,
				From:   msg.From,
			}
			deleteMsgBytes, _ := json.Marshal(deleteMsg)

			// Отправляем уведомление всем участникам чата
			for _, user := range chat.Users {
				if client, exists := clients[user]; exists {
					client.Conn.WriteMessage(websocket.TextMessage, deleteMsgBytes)
				}
			}

			// Удаляем чат из базы данных
			if err := db.DeleteChat(msg.ChatID); err != nil {
				log.Printf("Ошибка удаления чата: %v", err)
			}

		case "message":
			// Проверяем существование чата
			dbChat, err := db.GetOrCreateChat(msg.From, msg.To)
			if err != nil {
				log.Printf("Ошибка получения/создания чата: %v", err)
				continue
			}
			chat := database.ConvertDBChatToChat(dbChat)
			msg.ChatID = chat.ID

			// Проверяем, является ли отправитель участником чата
			isParticipant := false
			for _, user := range chat.Users {
				if user == msg.From {
					isParticipant = true
					break
				}
			}

			if !isParticipant {
				errorMsg := models.Message{
					Type:    "error",
					Content: "У вас нет доступа к этому чату",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
				continue
			}

			log.Printf("Получено сообщение: от %s к %s: %s", msg.From, msg.To, msg.Content)

			// Проверка на отправку сообщения самому себе
			if msg.From == msg.To {
				errorMsg := models.Message{
					Type:    "error",
					Content: "Нельзя отправлять сообщения самому себе",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				if err := conn.WriteMessage(websocket.TextMessage, errorBytes); err != nil {
					log.Printf("Ошибка отправки сообщения об ошибке: %v", err)
				}
				continue
			}

			// Сохраняем сообщение в базу данных
			if err := db.SaveMessage(msg.From, msg.To, msg.Content); err != nil {
				log.Printf("Ошибка сохранения сообщения: %v", err)
				continue
			}

			// Отправляем сообщение отправителю для подтверждения
			senderResponse := models.Message{
				Type:    "message",
				From:    msg.From,
				To:      msg.To,
				Content: msg.Content,
				ChatID:  chat.ID,
			}
			senderBytes, _ := json.Marshal(senderResponse)
			log.Printf("Отправка подтверждения отправителю: %s", string(senderBytes))
			if err := conn.WriteMessage(websocket.TextMessage, senderBytes); err != nil {
				log.Printf("Ошибка отправки подтверждения отправителю: %v", err)
			}

			mu.Lock()
			recipient, exists := clients[msg.To]
			mu.Unlock()

			if exists {
				// Если получатель онлайн, отправляем сообщение
				recipientResponse := models.Message{
					Type:    "message",
					From:    msg.From,
					To:      msg.To,
					Content: msg.Content,
					ChatID:  chat.ID,
				}
				recipientBytes, _ := json.Marshal(recipientResponse)
				log.Printf("Отправка сообщения получателю: %s", string(recipientBytes))
				if err := recipient.Conn.WriteMessage(websocket.TextMessage, recipientBytes); err != nil {
					log.Printf("Ошибка отправки сообщения получателю: %v", err)
				} else {
					// Помечаем сообщение как прочитанное
					if err := db.MarkMessagesAsRead(msg.From, msg.To); err != nil {
						log.Printf("Ошибка отметки сообщения как прочитанного: %v", err)
					}
				}
			} else {
				log.Printf("Получатель %s оффлайн, сообщение сохранено", msg.To)
			}

		case "get_chat_history":
			// Получаем историю сообщений чата
			chatID := msg.Content
			dbMessages, err := db.GetChatMessages(chatID, 50) // Получаем последние 50 сообщений
			if err != nil {
				log.Printf("Ошибка получения истории чата: %v", err)
				continue
			}

			// Сортируем сообщения по времени (от старых к новым)
			sort.Slice(dbMessages, func(i, j int) bool {
				return dbMessages[i].CreatedAt.Before(dbMessages[j].CreatedAt)
			})

			// Отправляем историю сообщений
			for _, dbMsg := range dbMessages {
				message := database.ConvertDBMessageToMessage(&dbMsg)
				message.Type = "message"
				messageBytes, _ := json.Marshal(message)
				conn.WriteMessage(websocket.TextMessage, messageBytes)
			}
		}

		// Обновляем таймаут после каждого успешного чтения
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	}

	// Очищаем соединение при выходе
	mu.Lock()
	for username, client := range clients {
		if client.Conn == conn {
			delete(clients, username)
			break
		}
	}
	mu.Unlock()
	conn.Close()
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

// HandleRegister обрабатывает регистрацию пользователя
func HandleRegister(w http.ResponseWriter, r *http.Request) {
	var user models.User
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

// HandleLogin обрабатывает вход пользователя
func HandleLogin(w http.ResponseWriter, r *http.Request) {
	var user models.User
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

// HandleLogout обрабатывает выход пользователя
func HandleLogout(w http.ResponseWriter, r *http.Request) {
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

// HandleValidateToken обрабатывает проверку токена
func HandleValidateToken(w http.ResponseWriter, r *http.Request) {
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
	// Используем FileServer для всех запросов, включая корневой путь
	workDir, _ := os.Getwd()
	// Если мы в cmd/server, поднимаемся на два уровня вверх
	if strings.HasSuffix(workDir, filepath.Join("cmd", "server")) {
		workDir = filepath.Join(workDir, "..", "..")
	}
	// Используем path/filepath для корректной обработки путей на разных ОС
	staticDir := filepath.Join(workDir, "web", "static")
	// Проверяем существование директории
	if _, err := os.Stat(staticDir); os.IsNotExist(err) {
		log.Printf("Директория %s не существует", staticDir)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	http.FileServer(http.Dir(staticDir)).ServeHTTP(w, r)
}
