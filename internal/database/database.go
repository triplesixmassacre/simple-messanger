package database

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"messanger/internal/models"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Database представляет подключение к MongoDB
type Database struct {
	client   *mongo.Client
	users    *mongo.Collection
	messages *mongo.Collection
	chats    *mongo.Collection
}

// DBUser представляет структуру пользователя в базе данных
type DBUser struct {
	Username  string    `bson:"username"`
	Password  string    `bson:"password"`
	CreatedAt time.Time `bson:"createdAt"`
}

// DBMessage представляет структуру сообщения в базе данных
type DBMessage struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	From      string             `bson:"from"`
	To        string             `bson:"to"`
	Content   string             `bson:"content"`
	CreatedAt time.Time          `bson:"createdAt"`
	Read      bool               `bson:"read"`
	ChatID    string             `bson:"chatId"`
}

// DBChat представляет структуру чата в базе данных
type DBChat struct {
	ID          string     `bson:"_id"`
	Users       []string   `bson:"users"`
	CreatedAt   time.Time  `bson:"createdAt"`
	LastMessage *DBMessage `bson:"lastMessage,omitempty"`
}

// Chat представляет структуру чата
type Chat struct {
	ID          string    `bson:"_id" json:"id"`
	Users       []string  `bson:"users" json:"users"`
	LastMessage *Message  `bson:"lastMessage,omitempty" json:"lastMessage,omitempty"`
	CreatedAt   time.Time `bson:"createdAt" json:"createdAt"`
}

// Message представляет структуру сообщения
type Message struct {
	ID        string    `bson:"_id" json:"id,omitempty"`
	Type      string    `json:"type,omitempty"`
	ChatID    string    `bson:"chatId" json:"chatId,omitempty"`
	From      string    `bson:"from" json:"from"`
	To        string    `bson:"to" json:"to"`
	Content   string    `bson:"content" json:"content"`
	CreatedAt time.Time `bson:"createdAt" json:"createdAt,omitempty"`
	Read      bool      `bson:"read" json:"read,omitempty"`
}

func NewDatabase(url string) (*Database, error) {
	// Создаем клиент MongoDB
	clientOptions := options.Client().ApplyURI(url)
	client, err := mongo.Connect(context.Background(), clientOptions)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания клиента MongoDB: %v", err)
	}

	// Проверяем подключение
	err = client.Ping(context.Background(), nil)
	if err != nil {
		return nil, fmt.Errorf("ошибка подключения к MongoDB: %v", err)
	}

	log.Println("Успешное подключение к MongoDB")

	// Получаем коллекции
	db := client.Database("messenger")
	users := db.Collection("users")
	messages := db.Collection("messages")
	chats := db.Collection("chats")

	return &Database{
		client:   client,
		users:    users,
		messages: messages,
		chats:    chats,
	}, nil
}

func (db *Database) Close() error {
	return db.client.Disconnect(context.Background())
}

// RegisterUser создает нового пользователя в базе данных
func (db *Database) RegisterUser(username, password string) error {
	// Проверяем валидность логина и пароля
	if err := models.ValidateUsername(username); err != nil {
		log.Printf("Ошибка валидации логина: %v", err)
		return err
	}

	if err := models.ValidatePassword(password); err != nil {
		log.Printf("Ошибка валидации пароля: %v", err)
		return err
	}

	// Проверяем, существует ли пользователь
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Printf("Попытка регистрации пользователя: %s", username)

	count, err := db.users.CountDocuments(ctx, bson.M{"username": username})
	if err != nil {
		log.Printf("Ошибка при проверке существования пользователя: %v", err)
		return err
	}
	if count > 0 {
		log.Printf("Пользователь %s уже существует", username)
		return fmt.Errorf("пользователь с именем %s уже существует", username)
	}

	// Создаем нового пользователя
	user := DBUser{
		Username:  username,
		Password:  password,
		CreatedAt: time.Now(),
	}

	result, err := db.users.InsertOne(ctx, user)
	if err != nil {
		log.Printf("Ошибка при сохранении пользователя: %v", err)
		return err
	}

	log.Printf("Пользователь %s успешно зарегистрирован с ID: %v", username, result.InsertedID)
	return nil
}

// GetUser получает пользователя по имени
func (db *Database) GetUser(username string) (*DBUser, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Printf("Попытка получения пользователя: %s", username)

	var user DBUser
	err := db.users.FindOne(ctx, bson.M{"username": username}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			log.Printf("Пользователь %s не найден", username)
			return nil, fmt.Errorf("пользователь не найден")
		}
		log.Printf("Ошибка при поиске пользователя: %v", err)
		return nil, err
	}

	log.Printf("Пользователь %s успешно найден", username)
	return &user, nil
}

// GetChat возвращает информацию о чате по его ID
func (db *Database) GetChat(chatID string) (*DBChat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var chat DBChat
	err := db.chats.FindOne(ctx, bson.M{"_id": chatID}).Decode(&chat)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, fmt.Errorf("чат не найден")
		}
		return nil, fmt.Errorf("ошибка получения чата: %v", err)
	}
	return &chat, nil
}

// GetChatMessages получает сообщения из чата
func (db *Database) GetChatMessages(chatID string, limit int64) ([]DBMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Получаем информацию о чате
	chat, err := db.GetChat(chatID)
	if err != nil {
		return nil, err
	}

	// Получаем сообщения
	filter := bson.M{"chatId": chatID}
	opts := options.Find().
		SetSort(bson.D{{"createdAt", 1}}). // Сортировка от старых к новым
		SetLimit(limit)

	cursor, err := db.messages.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("ошибка получения сообщений: %v", err)
	}
	defer cursor.Close(ctx)

	var messages []DBMessage
	if err = cursor.All(ctx, &messages); err != nil {
		return nil, fmt.Errorf("ошибка декодирования сообщений: %v", err)
	}

	// Проверяем, что все сообщения принадлежат участникам чата
	validMessages := make([]DBMessage, 0)
	for _, msg := range messages {
		if (msg.From == chat.Users[0] && msg.To == chat.Users[1]) ||
			(msg.From == chat.Users[1] && msg.To == chat.Users[0]) {
			validMessages = append(validMessages, msg)
		}
	}

	return validMessages, nil
}

// GetOrCreateChat получает существующий чат или создает новый
func (db *Database) GetOrCreateChat(user1, user2 string) (*DBChat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Проверяем существование обоих пользователей
	for _, username := range []string{user1, user2} {
		_, err := db.GetUser(username)
		if err != nil {
			return nil, fmt.Errorf("пользователь %s не найден", username)
		}
	}

	// Сортируем имена пользователей для создания уникального ID чата
	users := []string{user1, user2}
	sort.Strings(users)
	chatID := base64.StdEncoding.EncodeToString([]byte(strings.Join(users, ":")))

	// Пытаемся найти существующий чат
	chat, err := db.GetChat(chatID)
	if err == nil {
		return chat, nil
	}

	// Если чат не найден, создаем новый
	newChat := DBChat{
		ID:        chatID,
		Users:     users,
		CreatedAt: time.Now(),
	}

	_, err = db.chats.InsertOne(ctx, newChat)
	if err != nil {
		return nil, fmt.Errorf("ошибка создания чата: %v", err)
	}

	log.Printf("Создан новый чат между пользователями %s и %s", user1, user2)
	return &newChat, nil
}

// SaveMessage сохраняет новое сообщение и обновляет последнее сообщение в чате
func (db *Database) SaveMessage(from, to, content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Получаем или создаем чат
	chat, err := db.GetOrCreateChat(from, to)
	if err != nil {
		return fmt.Errorf("ошибка получения/создания чата: %v", err)
	}

	// Проверяем права доступа
	hasAccess := false
	for _, user := range chat.Users {
		if user == from {
			hasAccess = true
			break
		}
	}
	if !hasAccess {
		return fmt.Errorf("нет прав для отправки сообщений в этот чат")
	}

	// Создаём новое сообщение
	message := DBMessage{
		From:      from,
		To:        to,
		Content:   content,
		CreatedAt: time.Now(),
		Read:      false,
		ChatID:    chat.ID,
	}

	// Сохраняем сообщение
	result, err := db.messages.InsertOne(ctx, message)
	if err != nil {
		return fmt.Errorf("ошибка сохранения сообщения: %v", err)
	}

	// Обновляем последнее сообщение в чате
	message.ID = result.InsertedID.(primitive.ObjectID)
	_, err = db.chats.UpdateOne(
		ctx,
		bson.M{"_id": chat.ID},
		bson.M{"$set": bson.M{"lastMessage": message}},
	)
	if err != nil {
		return fmt.Errorf("ошибка обновления последнего сообщения: %v", err)
	}

	log.Printf("Сообщение успешно сохранено: от %s к %s в чате %s", from, to, chat.ID)
	return nil
}

// GetMessages получает сообщения между двумя пользователями
func (db *Database) GetMessages(from, to string, limit int64) ([]DBMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{
		"$or": []bson.M{
			{"from": from, "to": to},
			{"from": to, "to": from},
		},
	}

	opts := options.Find().SetSort(bson.D{{"createdAt", -1}}).SetLimit(limit)
	cursor, err := db.messages.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var messages []DBMessage
	if err = cursor.All(ctx, &messages); err != nil {
		return nil, err
	}

	return messages, nil
}

// MarkMessagesAsRead помечает сообщения как прочитанные
func (db *Database) MarkMessagesAsRead(from, to string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{
		"from": from,
		"to":   to,
		"read": false,
	}

	update := bson.M{
		"$set": bson.M{
			"read": true,
		},
	}

	_, err := db.messages.UpdateMany(ctx, filter, update)
	return err
}

// GetUnreadMessages получает непрочитанные сообщения для пользователя
func (db *Database) GetUnreadMessages(username string) ([]DBMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{
		"to":   username,
		"read": false,
	}

	cursor, err := db.messages.Find(ctx, filter)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var messages []DBMessage
	if err = cursor.All(ctx, &messages); err != nil {
		return nil, err
	}

	return messages, nil
}

// GetMessagesWithPagination получает сообщения между двумя пользователями с пагинацией
func (db *Database) GetMessagesWithPagination(from, to string, page, pageSize int64) ([]DBMessage, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{
		"$or": []bson.M{
			{"from": from, "to": to},
			{"from": to, "to": from},
		},
	}

	total, err := db.messages.CountDocuments(ctx, filter)
	if err != nil {
		return nil, 0, err
	}

	opts := options.Find().
		SetSort(bson.D{{"createdAt", -1}}).
		SetSkip((page - 1) * pageSize).
		SetLimit(pageSize)

	cursor, err := db.messages.Find(ctx, filter, opts)
	if err != nil {
		return nil, 0, err
	}
	defer cursor.Close(ctx)

	var messages []DBMessage
	if err = cursor.All(ctx, &messages); err != nil {
		return nil, 0, err
	}

	return messages, total, nil
}

// GetUserChats получает все чаты пользователя
func (db *Database) GetUserChats(username string) ([]DBChat, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"users": username}
	opts := options.Find().SetSort(bson.D{{"lastMessage.createdAt", -1}})

	cursor, err := db.chats.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var chats []DBChat
	if err = cursor.All(ctx, &chats); err != nil {
		return nil, err
	}

	return chats, nil
}

// DeleteChat удаляет чат и все его сообщения
func (db *Database) DeleteChat(chatID string) error {
	// Удаляем чат
	_, err := db.chats.DeleteOne(context.Background(), bson.M{"_id": chatID})
	if err != nil {
		return err
	}

	// Удаляем все сообщения чата
	_, err = db.messages.DeleteMany(context.Background(), bson.M{"chatId": chatID})
	if err != nil {
		return err
	}

	return nil
}

// ConvertDBChatToChat конвертирует DBChat в Chat
func ConvertDBChatToChat(dbChat *DBChat) *Chat {
	if dbChat == nil {
		return nil
	}

	chat := &Chat{
		ID:        dbChat.ID,
		Users:     dbChat.Users,
		CreatedAt: dbChat.CreatedAt,
	}

	if dbChat.LastMessage != nil {
		chat.LastMessage = ConvertDBMessageToMessage(dbChat.LastMessage)
	}

	return chat
}

// ConvertDBMessageToMessage конвертирует DBMessage в Message
func ConvertDBMessageToMessage(dbMsg *DBMessage) *Message {
	if dbMsg == nil {
		return nil
	}

	return &Message{
		ID:        dbMsg.ID.Hex(),
		ChatID:    dbMsg.ChatID,
		From:      dbMsg.From,
		To:        dbMsg.To,
		Content:   dbMsg.Content,
		CreatedAt: dbMsg.CreatedAt,
		Read:      dbMsg.Read,
	}
}
