package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	http.HandleFunc("/", Handler)
	http.HandleFunc("/ws", HandleConnection)
	http.HandleFunc("/register", handleRegister)
	http.HandleFunc("/login", handleLogin)
	http.HandleFunc("/logout", handleLogout)
	http.HandleFunc("/validate-token", handleValidateToken)

	// Обслуживаем статические файлы
	fs := http.FileServer(http.Dir("."))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	// Создаем сервер
	server := &http.Server{
		Addr:    ":8000",
		Handler: nil,
	}

	// Канал для получения сигналов завершения
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Запускаем сервер в отдельной горутине
	go func() {
		log.Println("Сервер запущен на http://localhost:8000")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
		client.conn.Close()
	}
	mu.Unlock()

	// Выполняем graceful shutdown сервера
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Ошибка при закрытии сервера: %v", err)
	}

	log.Println("Сервер успешно остановлен")
}
