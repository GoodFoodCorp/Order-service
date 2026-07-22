package config

import (
	"fmt"
	"os"
)

type Config struct {
	Port              string
	DatabaseURL       string
	JWTSecret         string
	PaymentServiceURL string
	LogLevel          string
}

// Load reads the typed configuration from environment variables only
// (12-factor). It fails fast on missing required values.
func Load() (*Config, error) {
	cfg := &Config{
		Port:              getEnv("PORT", "8082"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		JWTSecret:         os.Getenv("JWT_SECRET"),
		PaymentServiceURL: getEnv("PAYMENT_SERVICE_URL", "http://payment-service:8086"),
		LogLevel:          getEnv("LOG_LEVEL", "info"),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
