package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"sync"
)

// User представляет структуру пользователя
type User struct {
	Username string `json:"username"`
	Password string `json:"password"` // В реальном приложении нужно хранить хеш пароля
	Token    string `json:"token,omitempty"`
}

// AuthManager управляет аутентификацией и авторизацией
type AuthManager struct {
	mu     sync.RWMutex
	tokens map[string]*User // token -> User
	users  map[string]*User // username -> User
}

// NewAuthManager создает новый менеджер аутентификации
func NewAuthManager() *AuthManager {
	return &AuthManager{
		tokens: make(map[string]*User),
		users:  make(map[string]*User),
	}
}

// validateUsername проверяет корректность имени пользователя
func validateUsername(username string) error {
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
func validatePassword(password string) error {
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

// RegisterUser регистрирует нового пользователя
func (am *AuthManager) RegisterUser(username, password string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	// Сначала проверяем валидность логина и пароля
	if err := validateUsername(username); err != nil {
		return err
	}

	if err := validatePassword(password); err != nil {
		return err
	}

	// Затем проверяем существование пользователя
	if _, exists := am.users[username]; exists {
		return errors.New("пользователь уже существует")
	}

	user := &User{
		Username: username,
		Password: password,
	}
	am.users[username] = user
	return nil
}

// Login выполняет вход пользователя и возвращает токен
func (am *AuthManager) Login(username, password string) (string, error) {
	am.mu.Lock()
	defer am.mu.Unlock()

	if err := validateUsername(username); err != nil {
		return "", err
	}

	if err := validatePassword(password); err != nil {
		return "", err
	}

	user, exists := am.users[username]
	if !exists {
		return "", errors.New("пользователь не найден")
	}

	if user.Password != password {
		return "", errors.New("неверный пароль")
	}

	// Генерируем новый токен
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := base64.URLEncoding.EncodeToString(b)

	// Сохраняем токен
	user.Token = token
	am.tokens[token] = user

	return token, nil
}

// ValidateToken проверяет валидность токена и возвращает имя пользователя
func (am *AuthManager) ValidateToken(token string) (string, error) {
	am.mu.RLock()
	defer am.mu.RUnlock()

	user, exists := am.tokens[token]
	if !exists {
		return "", errors.New("токен не найден")
	}

	return user.Username, nil
}

// Logout выполняет выход пользователя
func (am *AuthManager) Logout(token string) error {
	am.mu.Lock()
	defer am.mu.Unlock()

	user, exists := am.tokens[token]
	if !exists {
		return errors.New("токен не найден")
	}

	// Очищаем токен у пользователя
	user.Token = ""
	delete(am.tokens, token)
	return nil
}
