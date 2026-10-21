package config

import "testing"

func setRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("JWT_SECRET", "jwt")
	t.Setenv("SERVICE_API_KEY", "svc-key")
}

func TestLoadRequiresServiceAPIKey(t *testing.T) {
	setRequired(t)
	t.Setenv("SERVICE_API_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when SERVICE_API_KEY is empty")
	}
}

func TestLoadServiceDefaults(t *testing.T) {
	setRequired(t)
	t.Setenv("STOCK_SERVICE_URL", "")
	t.Setenv("MENU_SERVICE_URL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServiceAPIKey != "svc-key" {
		t.Errorf("ServiceAPIKey = %q", cfg.ServiceAPIKey)
	}
	if cfg.StockServiceURL != "http://stock-service:8083" || cfg.MenuServiceURL != "http://menu-service:8085" {
		t.Errorf("defaults = %q %q", cfg.StockServiceURL, cfg.MenuServiceURL)
	}
}

func TestLoadServiceOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("STOCK_SERVICE_URL", "http://s:1")
	t.Setenv("MENU_SERVICE_URL", "http://m:2")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StockServiceURL != "http://s:1" || cfg.MenuServiceURL != "http://m:2" {
		t.Errorf("overrides = %q %q", cfg.StockServiceURL, cfg.MenuServiceURL)
	}
}
