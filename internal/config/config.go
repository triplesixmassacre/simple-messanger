package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server    ServerConfig    `yaml:"server"`
	Database  DatabaseConfig  `yaml:"database"`
	WebSocket WebSocketConfig `yaml:"websocket"`
}

type ServerConfig struct {
	Port      string `yaml:"port" env:"SERVER_PORT"`
	StaticDir string `yaml:"static_dir" env:"STATIC_DIR"`
}

type DatabaseConfig struct {
	URL string `yaml:"url" env:"DATABASE_URL"`
}

type WebSocketConfig struct {
	WriteTimeout time.Duration `yaml:"write_timeout"`
	PingInterval time.Duration `yaml:"ping_interval"`
}

func LoadConfig(filename string) (*Config, error) {
	config := &Config{
		Server: ServerConfig{
			Port:      "8080",
			StaticDir: "web/static",
		},
		Database: DatabaseConfig{
			URL: "mongodb://mongodb:27017/messenger",
		},
		WebSocket: WebSocketConfig{
			WriteTimeout: 10 * time.Second,
			PingInterval: 9 * time.Second,
		},
	}

	// если файл существует, загружаем из него
	if _, err := os.Stat(filename); err == nil {
		data, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}

		if err := yaml.Unmarshal(data, config); err != nil {
			return nil, err
		}
	}

	// проверяем переменные окружения
	if envPort := os.Getenv("SERVER_PORT"); envPort != "" {
		config.Server.Port = envPort
	}
	if envDB := os.Getenv("DATABASE_URL"); envDB != "" {
		config.Database.URL = envDB
	}
	if envStatic := os.Getenv("STATIC_DIR"); envStatic != "" {
		config.Server.StaticDir = envStatic
	}

	return config, nil
}
