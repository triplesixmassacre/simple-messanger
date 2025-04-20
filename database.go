package main

import (
	"context"
	"fmt"
	"log"
	"time"

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

	return &Database{
		client:   client,
		users:    users,
		messages: messages,
	}, nil
}

func (db *Database) Close() error {
	return db.client.Disconnect(context.Background())
}

// RegisterUser создает нового пользователя в базе данных
func (db *Database) RegisterUser(username, password string) error {
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

// SaveMessage сохраняет новое сообщение
func (db *Database) SaveMessage(from, to, content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Создаём новое сообщение
	message := DBMessage{
		From:      from,
		To:        to,
		Content:   content,
		CreatedAt: time.Now(),
		Read:      false,
	}

	_, err := db.messages.InsertOne(ctx, message) //сохраняем сообщение с контекстом
	return err
}

// GetMessages получает сообщения между двумя пользователями
func (db *Database) GetMessages(from, to string, limit int64) ([]DBMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{
		"$or": []bson.M{ //or это или, то есть если пользователь отправил сообщение другому пользователю, то он будет встречаться в обоих направлениях
			{"from": from, "to": to},
			{"from": to, "to": from},
		},
	}

	opts := options.Find().SetSort(bson.D{{"createdAt", -1}}).SetLimit(limit) //сортируем с конца(по убыванию) и ограничиваем количество сообщений
	cursor, err := db.messages.Find(ctx, filter, opts)                        //курсор перебирает все сообщения
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

	filter := bson.M{ //фильтр, где идёт поиск сообщений от одного пользователя к другому и они не прочитаны
		"from": from,
		"to":   to,
		"read": false,
	}

	update := bson.M{ //обновляем сообщение, где идёт поиск сообщений от одного пользователя к другому и они не прочитаны
		"$set": bson.M{ // $set это установить, то есть мы устанавливаем что прочитанное сообщение равно true
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
