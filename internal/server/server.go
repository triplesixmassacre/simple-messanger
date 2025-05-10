package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"messanger/internal/auth"
	"messanger/internal/config"
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
	writeMu  sync.Mutex // мьютекс для синхронизации записи
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
	// Конфигурация
	cfg *config.Config
	mu  sync.Mutex
)

// lastPing теперь хранит не только время, но и статус

type PingStatus struct {
	Last   time.Time
	Online bool
}

var lastPing = make(map[string]PingStatus)

// InitServer инициализирует сервер с заданной конфигурацией
func InitServer(config *config.Config) {
	cfg = config

	var err error
	db, err = database.NewDatabase(cfg.Database.URL)
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

// safeWrite безопасно отправляет сообщение через WebSocket
func (c *Client) safeWrite(messageType int, data []byte) error {
	if c == nil || c.Conn == nil {
		return fmt.Errorf("клиент или соединение не инициализированы")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.Conn.WriteMessage(messageType, data)
}

func handleWebSocket(conn *websocket.Conn) {
	// Устанавливаем таймаут для чтения
	if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		log.Printf("SetReadDeadline error: %v", err)
	}

	// Устанавливаем обработчик пингов
	conn.SetPingHandler(func(appData string) error {
		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			log.Printf("SetReadDeadline error (ping handler): %v", err)
		}
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
	var currentUsername string
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

		if msg.Type == "ping" {
			if msg.From != "" {
				ps, ok := lastPing[msg.From]
				log.Printf("PING от %s, lastPing: %+v, clients: %v", msg.From, lastPing, clients)
				if !ok || !ps.Online {
					lastPing[msg.From] = PingStatus{Last: time.Now(), Online: true}
					go func(username string) {
						chats, err := db.GetUserChats(username)
						if err != nil {
							log.Printf("Ошибка получения чатов для online: %v", err)
							return
						}
						for _, chat := range chats {
							for _, user := range chat.Users {
								if user != username {
									if c, exists := clients[user]; exists {
										log.Printf("Рассылаю ONLINE: %s -> %s", username, user)
										onlineMsg := models.Message{
											Type:    "user_status",
											From:    username,
											Content: "online",
										}
										onlineBytes, _ := json.Marshal(onlineMsg)
										c.safeWrite(websocket.TextMessage, onlineBytes)
									}
								}
							}
						}
					}(msg.From)
				} else {
					lastPing[msg.From] = PingStatus{Last: time.Now(), Online: true}
				}
			}
			continue
		}

		if msg.Type == "login" {
			currentUsername = msg.From
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

			// Регистрируем пользователя в AuthManager
			if err := authManager.RegisterUser(msg.From, msg.Content); err != nil {
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"`+err.Error()+`"}`))
				continue
			}

			// Регистрируем пользователя в базе данных
			if err := db.RegisterUser(msg.From, msg.Content); err != nil {
				// Если не удалось сохранить в БД, удаляем из AuthManager
				if err := authManager.Logout(msg.From); err != nil {
					log.Printf("Ошибка Logout: %v", err)
				}
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"`+err.Error()+`"}`))
				continue
			}

			conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"success","content":"Регистрация успешна"}`))

		case "login":
			// Проверяем учетные данные в базе данных
			user, err := db.GetUser(msg.From)
			if err != nil {
				log.Printf("Пользователь не найден: %s", msg.From)
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"Пользователь не найден"}`))
				continue
			}

			// Проверяем пароль
			if user.Password != msg.Content {
				log.Printf("Неверный пароль для пользователя: %s", msg.From)
				conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","content":"Неверный пароль"}`))
				continue
			}

			mu.Lock()
			// Проверяем, не существует ли уже соединение для этого пользователя
			if existingClient, exists := clients[msg.From]; exists {
				// Отправляем сообщение о новом входе перед закрытием
				closeMsg := models.Message{
					Type:    "info",
					Content: "Выполнен вход с другого устройства",
				}
				closeMsgBytes, _ := json.Marshal(closeMsg)
				if err := existingClient.safeWrite(websocket.TextMessage, closeMsgBytes); err != nil {
					log.Printf("Ошибка safeWrite (closeMsg): %v", err)
				}
				// Закрываем существующее соединение
				delete(clients, msg.From)
			}
			// Создаем новое подключение
			client := &Client{
				Conn:     conn,
				Username: msg.From,
				Token:    msg.Content,
				writeMu:  sync.Mutex{},
			}
			clients[msg.From] = client
			mu.Unlock()

			// Сброс статуса в offline, чтобы первый ping после логина вызвал рассылку online
			lastPing[msg.From] = PingStatus{Last: time.Now(), Online: false}

			go func(currentUser string) {
				chats, err := db.GetUserChats(currentUser)
				if err != nil {
					log.Printf("Ошибка получения чатов для рассылки статуса online при логине: %v", err)
					return
				}
				for _, chat := range chats {
					for _, user := range chat.Users {
						if user != currentUser {
							mu.Lock()
							recipient, exists := clients[user]
							mu.Unlock()
							if exists {
								log.Printf("Рассылаю ONLINE (логин): %s -> %s", currentUser, user)
								onlineMsg := models.Message{
									Type:    "user_status",
									From:    currentUser,
									Content: "online",
								}
								onlineBytes, _ := json.Marshal(onlineMsg)
								recipient.safeWrite(websocket.TextMessage, onlineBytes)
							}
						}
					}
				}
			}(msg.From)

			// Отправляем подтверждение успешного входа
			response := models.Message{
				Type:    "success",
				Content: "Успешный вход в систему",
			}
			responseBytes, _ := json.Marshal(response)
			client.safeWrite(websocket.TextMessage, responseBytes)

			log.Printf("Пользователь %s вошел в систему", msg.From)

			// Отправляем список чатов пользователя
			go func() {
				chats, err := db.GetUserChats(msg.From)
				if err != nil {
					log.Printf("Ошибка получения чатов: %v", err)
					return
				}

				// Отправляем только те чаты, где пользователь является одним из двух участников
				for _, chat := range chats {
					// Проверяем, что в чате ровно два участника
					if len(chat.Users) != 2 {
						continue
					}

					// Проверяем, что текущий пользователь является одним из участников
					isParticipant := false
					var otherUser string
					for _, user := range chat.Users {
						if user == msg.From {
							isParticipant = true
						} else {
							otherUser = user
						}
					}

					if !isParticipant || otherUser == "" {
						continue
					}

					chatMsg := models.Message{
						Type:    "chat",
						ChatID:  chat.ID,
						From:    msg.From,
						To:      otherUser,
						Content: chat.ID,
					}
					if chat.LastMessage != nil {
						// Проверяем, что последнее сообщение принадлежит участникам чата
						if (chat.LastMessage.From == msg.From || chat.LastMessage.From == otherUser) &&
							(chat.LastMessage.To == msg.From || chat.LastMessage.To == otherUser) {
							chatMsg.Content = chat.LastMessage.Content
						}
					}
					chatBytes, _ := json.Marshal(chatMsg)
					client.safeWrite(websocket.TextMessage, chatBytes)
				}
			}()

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
						ChatID:  msg.ChatID,
					})
					client.safeWrite(websocket.TextMessage, messageBytes)
				}
			}()

			// После добавления клиента в clients, отправляем статусы онлайн всех собеседников
			go func(currentUser string) {
				chats, err := db.GetUserChats(currentUser)
				if err != nil {
					log.Printf("Ошибка получения чатов для статусов онлайн: %v", err)
					return
				}
				for _, chat := range chats {
					for _, user := range chat.Users {
						if user != currentUser {
							if _, exists := clients[user]; exists {
								onlineMsg := models.Message{
									Type:    "user_status",
									From:    user,
									Content: "online",
								}
								onlineBytes, _ := json.Marshal(onlineMsg)
								client.safeWrite(websocket.TextMessage, onlineBytes)
							}
						}
					}
				}
			}(msg.From)

		case "delete_chat":
			// Получаем информацию о чате
			chat, err := db.GetChat(msg.ChatID)
			if err != nil {
				log.Printf("Ошибка получения информации о чате: %v", err)
				errorMsg := models.Message{
					Type:    "error",
					Content: "Чат не найден",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
				continue
			}

			// Проверяем, является ли пользователь участником чата
			isParticipant := false
			for _, user := range chat.Users {
				if user == msg.From {
					isParticipant = true
					break
				}
			}

			if !isParticipant {
				log.Printf("Попытка удаления чужого чата: %s -> %s", msg.From, msg.ChatID)
				errorMsg := models.Message{
					Type:    "error",
					Content: "У вас нет прав для удаления этого чата",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
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
					client.safeWrite(websocket.TextMessage, deleteMsgBytes)
				}
			}

			// Удаляем чат из базы данных
			if err := db.DeleteChat(msg.ChatID); err != nil {
				log.Printf("Ошибка удаления чата: %v", err)
				errorMsg := models.Message{
					Type:    "error",
					Content: "Ошибка при удалении чата",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
			}

		case "message":
			// Проверяем, что отправитель авторизован
			mu.Lock()
			currentClient, exists := clients[msg.From]
			mu.Unlock()

			if !exists || currentClient.Conn != conn {
				log.Printf("Попытка отправки сообщения от имени другого пользователя: %s", msg.From)
				errorMsg := models.Message{
					Type:    "error",
					Content: "У вас нет прав для отправки сообщений от имени этого пользователя",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
				continue
			}

			// Проверяем существование получателя
			log.Printf("Проверка получателя: %s", msg.To)
			if _, err := db.GetUser(msg.To); err != nil {
				log.Printf("Получатель не найден: %s, ошибка: %v", msg.To, err)
				errorMsg := models.Message{
					Type:    "error",
					Content: "Получатель не найден",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
				continue
			}

			// Проверяем существование чата или создаем новый
			dbChat, err := db.GetOrCreateChat(msg.From, msg.To)
			if err != nil {
				log.Printf("Ошибка получения/создания чата: %v", err)
				errorMsg := models.Message{
					Type:    "error",
					Content: fmt.Sprintf("Ошибка создания чата: %v", err),
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
				continue
			}

			// Устанавливаем правильный ChatID
			msg.ChatID = dbChat.ID

			// Сохраняем сообщение
			if err := db.SaveMessage(msg.From, msg.To, msg.Content); err != nil {
				log.Printf("Ошибка сохранения сообщения: %v", err)
				errorMsg := models.Message{
					Type:    "error",
					Content: "Ошибка сохранения сообщения",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
				continue
			}

			// Отправляем подтверждение отправителю
			senderResponse := models.Message{
				Type:    "message",
				From:    msg.From,
				To:      msg.To,
				Content: msg.Content,
				ChatID:  dbChat.ID,
			}
			senderBytes, _ := json.Marshal(senderResponse)
			log.Printf("Отправка подтверждения отправителю: %s", string(senderBytes))

			if err := currentClient.safeWrite(websocket.TextMessage, senderBytes); err != nil {
				log.Printf("Ошибка отправки подтверждения отправителю: %v", err)
			}

			// Отправляем сообщение получателю, если он онлайн
			mu.Lock()
			recipient := clients[msg.To]
			mu.Unlock()

			if recipient != nil {
				recipientResponse := models.Message{
					Type:    "message",
					From:    msg.From,
					To:      msg.To,
					Content: msg.Content,
					ChatID:  dbChat.ID,
				}
				recipientBytes, _ := json.Marshal(recipientResponse)
				log.Printf("Отправка сообщения получателю: %s", string(recipientBytes))
				if err := recipient.safeWrite(websocket.TextMessage, recipientBytes); err != nil {
					log.Printf("Ошибка отправки сообщения получателю: %v", err)
				}
			} else {
				log.Printf("Получатель %s оффлайн, сообщение сохранено", msg.To)
			}

			// Обновляем последнее сообщение в чате для обоих пользователей
			chatMsg := models.Message{
				Type:    "chat",
				ChatID:  dbChat.ID,
				From:    msg.From,
				To:      msg.To,
				Content: msg.Content,
			}
			chatBytes, _ := json.Marshal(chatMsg)

			// Отправляем обновление чата отправителю
			if err := currentClient.safeWrite(websocket.TextMessage, chatBytes); err != nil {
				log.Printf("Ошибка отправки обновления чата отправителю: %v", err)
			}

			// Отправляем обновление чата получателю, если он онлайн
			if recipient != nil {
				if err := recipient.safeWrite(websocket.TextMessage, chatBytes); err != nil {
					log.Printf("Ошибка отправки обновления чата получателю: %v", err)
				}
			}

		case "get_chat_history":
			// Получаем историю сообщений чата
			chatID := msg.Content

			// Проверяем, что пользователь имеет доступ к чату
			chat, err := db.GetChat(chatID)
			if err != nil {
				log.Printf("Ошибка получения информации о чате: %v", err)
				errorMsg := models.Message{
					Type:    "error",
					Content: "Чат не найден",
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
				continue
			}

			// Строгая проверка участников чата
			isParticipant := false
			for _, user := range chat.Users {
				if user == msg.From {
					isParticipant = true
					break
				}
			}

			if !isParticipant {
				log.Printf("Попытка доступа к чужому чату: %s -> %s", msg.From, chatID)
				errorMsg := models.Message{
					Type:    "error",
					Content: "У вас нет доступа к этому чату",
					ChatID:  chatID,
				}
				errorBytes, _ := json.Marshal(errorMsg)
				conn.WriteMessage(websocket.TextMessage, errorBytes)
				continue
			}

			// Получаем сообщения
			dbMessages, err := db.GetChatMessages(chatID, 50)
			if err != nil {
				log.Printf("Ошибка получения истории чата: %v", err)
				continue
			}

			// Отправляем сообщения
			for _, dbMsg := range dbMessages {
				message := database.ConvertDBMessageToMessage(&dbMsg)
				message.Type = "message"
				messageBytes, _ := json.Marshal(message)
				conn.WriteMessage(websocket.TextMessage, messageBytes)
			}
		}

		// Обновляем таймаут после каждого успешного чтения
		if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			log.Printf("SetReadDeadline error (loop): %v", err)
		}
	}

	// Очищаем соединение при выходе
	mu.Lock()
	for username, client := range clients {
		if client.Conn == conn {
			delete(clients, username)
			// Отправляем уведомление о выходе всем пользователям, с которыми есть чаты
			go func() {
				chats, err := db.GetUserChats(username)
				if err != nil {
					log.Printf("Ошибка получения чатов для уведомления о выходе: %v", err)
					return
				}

				for _, chat := range chats {
					for _, user := range chat.Users {
						if user != username {
							if c, exists := clients[user]; exists {
								log.Printf("Рассылаю OFFLINE: %s -> %s", username, user)
								offlineMsg := models.Message{
									Type:    "user_status",
									From:    username,
									Content: "offline",
								}
								offlineBytes, _ := json.Marshal(offlineMsg)
								c.safeWrite(websocket.TextMessage, offlineBytes)
							}
						}
					}
				}
			}()
			break
		}
	}
	mu.Unlock()
	conn.Close()

	if currentUsername != "" {
		delete(lastPing, currentUsername)
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
	// Получаем рабочую директорию
	workDir, _ := os.Getwd()
	if strings.HasSuffix(workDir, filepath.Join("cmd", "server")) {
		workDir = filepath.Join(workDir, "..", "..")
	}

	// Нормализуем URL путь (всегда используем прямые слеши)
	urlPath := path.Clean("/" + r.URL.Path)

	// Устанавливаем правильные MIME-типы
	if strings.HasSuffix(urlPath, ".js") {
		w.Header().Set("Content-Type", "application/javascript")
	} else if strings.HasSuffix(urlPath, ".css") {
		w.Header().Set("Content-Type", "text/css")
	} else if strings.HasSuffix(urlPath, ".html") {
		w.Header().Set("Content-Type", "text/html")
	}

	// Если запрос к статическим файлам
	if strings.HasPrefix(urlPath, "/static/") {
		// Преобразуем URL путь в путь файловой системы
		fsPath := filepath.Join(workDir, "web", strings.TrimPrefix(urlPath, "/"))
		http.ServeFile(w, r, fsPath)
		return
	}

	// Для всех остальных запросов отдаем index.html
	indexPath := filepath.Join(workDir, "web", "static", "index.html")
	http.ServeFile(w, r, indexPath)
}

// Добавляю отдельную горутину для проверки пингов
func init() {
	go func() {
		for {
			time.Sleep(5 * time.Second)
			mu.Lock()
			for username := range clients {
				ps, ok := lastPing[username]
				if !ok || time.Since(ps.Last) > 20*time.Second {
					// Считаем пользователя оффлайн
					delete(clients, username)
					delete(lastPing, username)
					go func(username string) {
						chats, err := db.GetUserChats(username)
						if err != nil {
							log.Printf("Ошибка получения чатов для offline: %v", err)
							return
						}
						for _, chat := range chats {
							for _, user := range chat.Users {
								if user != username {
									if c, exists := clients[user]; exists {
										log.Printf("Рассылаю OFFLINE: %s -> %s", username, user)
										offlineMsg := models.Message{
											Type:    "user_status",
											From:    username,
											Content: "offline",
										}
										offlineBytes, _ := json.Marshal(offlineMsg)
										c.safeWrite(websocket.TextMessage, offlineBytes)
									}
								}
							}
						}
					}(username)
				}
			}
			mu.Unlock()
		}
	}()
}
