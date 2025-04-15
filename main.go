package main

import (
	"log"
	"net/http"
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

	log.Println("Сервер запущен на http://localhost:8000")
	log.Fatal(http.ListenAndServe(":8000", nil))
}
