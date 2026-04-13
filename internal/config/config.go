package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL      string
	SessionSecret    string
	Port             string
	SecureCookies    bool
	AllowRegistration bool
	MaxUploadMB      int64
	UploadsPath      string
}

func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		SessionSecret:    os.Getenv("SESSION_SECRET"),
		Port:             getEnvDefault("PORT", "8080"),
		SecureCookies:    os.Getenv("SECURE_COOKIES") == "true",
		AllowRegistration: os.Getenv("ALLOW_REGISTRATION") == "true",
		MaxUploadMB:      parseIntDefault("MAX_UPLOAD_MB", 2000),
		UploadsPath:      getEnvDefault("UPLOADS_PATH", "/app/data/uploads"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.SessionSecret) < 32 {
		return nil, fmt.Errorf("SESSION_SECRET must be at least 32 characters")
	}

	return cfg, nil
}

func getEnvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseIntDefault(key string, def int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}
