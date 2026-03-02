package config

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type AppConfig struct {
	Port         string
	Environment  string
	DatabasePath string
	UploadDir    string
	PythonScript string
	MaxFileSize  int64
}

func Load() (*AppConfig, error) {
	// Load .env file
	_ = godotenv.Load()

	cfg := &AppConfig{
		Port:         getEnv("PORT", "3000"),
		Environment:  getEnv("ENV", "development"),
		DatabasePath: getEnv("DATABASE_PATH", "./database.db"),
		UploadDir:    getEnv("UPLOAD_DIR", "./uploads"),
		PythonScript: getEnv("PYTHON_SCRIPT", "./python/extract.py"),
		MaxFileSize:  int64(50 * 1024 * 1024), // 50MB default
	}

	// Validate
	if cfg.Port == "" {
		cfg.Port = "3000"
	}

	log.Printf("📝 Config loaded: Port=%s, Env=%s", cfg.Port, cfg.Environment)
	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	valueStr := getEnv(key, "")
	if valueStr == "" {
		return defaultVal
	}
	value, err := strconv.Atoi(valueStr)
	if err != nil {
		fmt.Printf("Warning: invalid int value for %s, using default\n", key)
		return defaultVal
	}
	return value
}
