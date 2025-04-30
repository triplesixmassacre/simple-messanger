package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"messanger/internal/database"
	"messanger/internal/server"
)

var (
	clients = make(map[string]*server.Client)
	mu      sync.Mutex
	db      *database.Database
)

func main() {
	var err error
	db, err = database.NewDatabase("mongodb://localhost:27017")
	if err != nil {
		log.Fatalf("Ошибка подключения к базе данных: %v", err)
	}
	// Обработка всех остальных запросов (включая статические файлы)
	http.HandleFunc("/", server.Handler)
	http.HandleFunc("/ws", server.HandleConnection)
	http.HandleFunc("/register", server.HandleRegister)
	http.HandleFunc("/login", server.HandleLogin)
	http.HandleFunc("/logout", server.HandleLogout)
	http.HandleFunc("/validate-token", server.HandleValidateToken)

	// Создаем сервер
	srv := &http.Server{
		Addr:    ":8000",
		Handler: nil,
	}

	// Канал для получения сигналов завершения
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Запускаем сервер в отдельной горутине
	go func() {
		log.Println("Сервер запущен на http://localhost:8000")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Ошибка запуска сервера: %v", err)
		}
	}()

	// Ожидаем сигнал завершения
	<-stop
	log.Println("Получен сигнал завершения, закрываем сервер...")

	// Создаем контекст с таймаутом для graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Закрываем базу данных
	if err := db.Close(); err != nil {
		log.Printf("Ошибка закрытия базы данных: %v", err)
	}

	// Закрываем все WebSocket соединения
	mu.Lock()
	for _, client := range clients {
		client.Conn.Close()
	}
	mu.Unlock()

	// Выполняем graceful shutdown сервера
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Ошибка при закрытии сервера: %v", err)
	}

	log.Println("Сервер успешно остановлен")
}
