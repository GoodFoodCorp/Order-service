package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port              string
	DatabaseURL       string
	JWTSecret         string
	JWTIssuer         string
	JWTAudience       string
	PaymentServiceURL string
	PromoServiceURL   string
	StockServiceURL   string
	MenuServiceURL    string
	// FranchiseServiceURL is optional: empty disables opening hours, delivery
	// zone and fee checks (local development without franchise-service).
	FranchiseServiceURL string
	ServiceAPIKey       string
	LogLevel            string
	RabbitMQURL         string

	// Invoices are emailed when SMTPHost is set (Mailpit locally).
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPSender   string
	FrontendURL  string
	// The seller printed on every invoice.
	CompanyName      string
	CompanyAddress   string
	CompanySIRET     string
	CompanyVATNumber string
}

// Load reads the typed configuration from environment variables only
// (12-factor). It fails fast on missing required values.
func Load() (*Config, error) {
	cfg := &Config{
		Port:                getEnv("PORT", "8082"),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		JWTSecret:           os.Getenv("JWT_SECRET"),
		JWTIssuer:           getEnv("JWT_ISSUER", "goodfood-auth"),
		JWTAudience:         getEnv("JWT_AUDIENCE", "goodfood-api"),
		PaymentServiceURL:   getEnv("PAYMENT_SERVICE_URL", "http://payment-service:8086"),
		PromoServiceURL:     getEnv("PROMO_SERVICE_URL", "http://promo-service:8092"),
		StockServiceURL:     getEnv("STOCK_SERVICE_URL", "http://stock-service:8083"),
		MenuServiceURL:      getEnv("MENU_SERVICE_URL", "http://menu-service:8085"),
		FranchiseServiceURL: os.Getenv("FRANCHISE_SERVICE_URL"),
		ServiceAPIKey:       os.Getenv("SERVICE_API_KEY"),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
		RabbitMQURL:         os.Getenv("RABBITMQ_URL"), // optional: empty disables event publishing
		SMTPHost:            os.Getenv("SMTP_HOST"),
		SMTPPort:            atoiOr(os.Getenv("SMTP_PORT"), 587),
		SMTPUser:            os.Getenv("SMTP_USER"),
		SMTPPassword:        os.Getenv("SMTP_PASSWORD"),
		SMTPSender:          getEnv("SMTP_SENDER", "Good Food <no-reply@goodfood.fr>"),
		FrontendURL:         getEnv("FRONTEND_URL", "http://localhost:3000"),
		CompanyName:         getEnv("INVOICE_COMPANY_NAME", "Good Food"),
		CompanyAddress:      getEnv("INVOICE_COMPANY_ADDRESS", "12 rue de la République, 75011 Paris"),
		CompanySIRET:        os.Getenv("INVOICE_COMPANY_SIRET"),
		CompanyVATNumber:    os.Getenv("INVOICE_COMPANY_VAT_NUMBER"),
	}
	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.JWTSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if cfg.ServiceAPIKey == "" {
		return nil, fmt.Errorf("SERVICE_API_KEY is required")
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func atoiOr(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return fallback
}
