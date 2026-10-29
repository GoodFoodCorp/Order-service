package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	httpadapter "goodfood/order-service/internal/adapter/http"
	"goodfood/order-service/internal/adapter/menuclient"
	"goodfood/order-service/internal/adapter/messaging"
	"goodfood/order-service/internal/adapter/paymentclient"
	"goodfood/order-service/internal/adapter/postgres"
	"goodfood/order-service/internal/adapter/promoclient"
	"goodfood/order-service/internal/adapter/stockclient"
	"goodfood/order-service/internal/application"
	"goodfood/order-service/internal/config"
	"goodfood/order-service/internal/domain"
)

const (
	// stockRetryInterval is the period of the stock synchronisation retry worker.
	stockRetryInterval = 30 * time.Second
	// stockRetryFirstDelay lets the first pass run shortly after startup so
	// orders left pending by a crash are picked up quickly.
	stockRetryFirstDelay = 10 * time.Second
	// stockRetryBatch is the maximum number of orders handled per pass.
	stockRetryBatch = 50
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

	var events domain.EventPublisher = domain.NoopPublisher{}
	if cfg.RabbitMQURL != "" {
		publisher := messaging.NewAMQPPublisher(cfg.RabbitMQURL)
		defer publisher.Close()
		events = publisher
		log.Info().Msg("order events published to rabbitmq")
	} else {
		log.Warn().Msg("RABBITMQ_URL not set: order events are not published")
	}

	// menu-service is both the recipe source (stock) and the price authority.
	menu := menuclient.New(cfg.MenuServiceURL, cfg.ServiceAPIKey)
	uc := application.NewUseCases(application.Deps{
		Orders:   postgres.NewOrderRepository(pool),
		Payments: paymentclient.New(cfg.PaymentServiceURL, cfg.ServiceAPIKey, nil),
		Promos:   promoclient.New(cfg.PromoServiceURL, cfg.ServiceAPIKey, nil),
		Events:   events,
		Recipes:  menu,
		Stock:    stockclient.New(cfg.StockServiceURL, cfg.ServiceAPIKey),
		Catalog:  menu,
	})
	log.Info().Str("payment_service", cfg.PaymentServiceURL).Msg("payments delegated to payment-service")
	log.Info().Str("promo_service", cfg.PromoServiceURL).Msg("promo codes delegated to promo-service")
	log.Info().Str("menu_service", cfg.MenuServiceURL).Str("stock_service", cfg.StockServiceURL).Msg("stock checked against stock-service using menu-service recipes")

	router := httpadapter.NewRouter(
		httpadapter.NewOrderHandler(uc),
		httpadapter.JWTConfig{Secret: cfg.JWTSecret, Issuer: cfg.JWTIssuer, Audience: cfg.JWTAudience},
		cfg.ServiceAPIKey,
		log,
		func(ctx context.Context) error { return pool.Ping(ctx) },
	)

	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go runStockRetry(runCtx, log, uc)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Info().Str("port", cfg.Port).Msg("order-service started")
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-runCtx.Done()
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("graceful shutdown failed")
		}
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal().Err(err).Msg("server stopped")
	}
	<-done // ListenAndServe returns as soon as Shutdown starts: wait for in-flight requests
	log.Info().Msg("order-service stopped")
}

// runStockRetry replays pending stock synchronisation until ctx ends.
func runStockRetry(ctx context.Context, log zerolog.Logger, uc *application.UseCases) {
	timer := time.NewTimer(stockRetryFirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			n := uc.RetryStockSync(ctx, stockRetryBatch)
			log.Debug().Int("processed", n).Msg("stock sync retry pass done")
			timer.Reset(stockRetryInterval)
		}
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
