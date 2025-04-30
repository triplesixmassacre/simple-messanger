package models

import (
	"errors"
	"regexp"
	"time"
)

// User представляет структуру пользователя
type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Token    string `json:"token,omitempty"`
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

// Chat представляет структуру чата
type Chat struct {
	ID          string    `bson:"_id" json:"id"`
	Users       []string  `bson:"users" json:"users"`
	LastMessage *Message  `bson:"lastMessage,omitempty" json:"lastMessage,omitempty"`
	CreatedAt   time.Time `bson:"createdAt" json:"createdAt"`
}

// validateUsername проверяет корректность имени пользователя
func ValidateUsername(username string) error {
	// Проверяем длину строки в байтах
	if len([]rune(username)) > 12 {
		return errors.New("имя пользователя не должно превышать 12 символов")
	}

	// Проверяем, что имя пользователя не содержит спецсимволы
	// Разрешаем любые буквы (включая кириллицу), цифры и подчеркивание
	invalidChars := regexp.MustCompile(`[!@#$%^&*()+\-=\[\]{};':"\\|,.<>\/?]`)
	if invalidChars.MatchString(username) {
		return errors.New("имя пользователя не должно содержать специальные символы")
	}

	return nil
}

// validatePassword проверяет корректность пароля
func ValidatePassword(password string) error {
	// Проверяем длину строки в байтах
	if len([]rune(password)) < 4 || len([]rune(password)) > 16 {
		return errors.New("пароль должен быть от 4 до 16 символов")
	}

	// Проверяем, что пароль содержит только допустимые символы
	validPassword := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	if !validPassword.MatchString(password) {
		return errors.New("пароль может содержать только буквы латиницы и цифры")
	}

	return nil
}
