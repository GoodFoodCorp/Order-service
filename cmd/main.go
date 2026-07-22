package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	httpadapter "goodfood/order-service/internal/adapter/http"
	"goodfood/order-service/internal/adapter/paymentclient"
	"goodfood/order-service/internal/adapter/postgres"
	"goodfood/order-service/internal/application"
	"goodfood/order-service/internal/config"
)

func main() {
	log := newLogger()

	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("invalid configuration")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := connectWithRetry(ctx, log, cfg.DatabaseURL)
	if err != nil {
		log.Fatal().Err(err).Msg("database unreachable")
	}
	defer pool.Close()

	if err := postgres.Migrate(cfg.DatabaseURL); err != nil {
		log.Fatal().Err(err).Msg("migrations failed")
	}
	log.Info().Msg("migrations applied")

	uc := application.NewUseCases(
		postgres.NewOrderRepository(pool),
		paymentclient.New(cfg.PaymentServiceURL),
	)
	log.Info().Str("payment_service", cfg.PaymentServiceURL).Msg("payments delegated to payment-service")

	router := httpadapter.NewRouter(
		httpadapter.NewOrderHandler(uc),
		cfg.JWTSecret,
		log,
		func(ctx context.Context) error { return pool.Ping(ctx) },
	)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Info().Str("port", cfg.Port).Msg("order-service started")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("server stopped")
	}
}

func newLogger() zerolog.Logger {
	level, err := zerolog.ParseLevel(strings.ToLower(os.Getenv("LOG_LEVEL")))
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	return zerolog.New(os.Stdout).Level(level).With().
		Timestamp().Str("service", "order-service").Logger()
}

func connectWithRetry(ctx context.Context, log zerolog.Logger, url string) (*pgxpool.Pool, error) {
	for i := 1; ; i++ {
		pool, err := postgres.Connect(ctx, url)
		if err == nil {
			return pool, nil
		}
		if i >= 15 {
			return nil, err
		}
		log.Warn().Err(err).Int("attempt", i).Msg("database not ready, retrying in 2s")
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
