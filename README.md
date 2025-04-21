# WebSocket Чат

Простой веб-чат с использованием WebSocket для обмена сообщениями в реальном времени.

## Функциональность

- Регистрация и авторизация пользователей
- Отправка и получение сообщений в реальном времени
- Сохранение истории сообщений
- Создание и удаление чатов
- Поддержка кириллицы
- Автоматическое переподключение при потере соединения

## Технологии

- Backend: Go (Golang)
- Frontend: HTML, CSS, JavaScript
- Протокол: WebSocket
- Хранение данных: MongoDB

## Запуск

```bash
go run .
```

Сервер запустится на `http://localhost:8000`

## Структура проекта

- `main.go` - точка входа, настройка сервера
- `server.go` - обработка WebSocket соединений и HTTP запросов
- `authorization.go` - управление аутентификацией и авторизацией
- `database.go` - работа с базой данных MongoDB
- `index.html` - веб-интерфейс чата
- `storage.js` - клиентская логика для работы с localStorage
- `go.mod` и `go.sum` - файлы управления зависимостями Go

## Форматы сообщений

### Регистрация
```json
{
    "type": "register",
    "from": "username",
    "to": "",
    "content": "password"
}
```

### Вход
```json
{
    "type": "login",
    "from": "username",
    "to": "",
    "content": "password"
}
```

### Отправка сообщения
```json
{
    "type": "message",
    "from": "sender",
    "to": "recipient",
    "content": "message text",
    "chatId": "base64_encoded_chat_id"
}
```

### Запрос истории чата
```json
{
    "type": "get_chat_history",
    "content": "chat_id"
}
```

### Удаление чата
```json
{
    "type": "delete_chat",
    "chatId": "chat_id",
    "from": "username"
}
```
