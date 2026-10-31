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

	"goodfood/order-service/internal/adapter/franchiseclient"
	httpadapter "goodfood/order-service/internal/adapter/http"
	"goodfood/order-service/internal/adapter/mailer"
	"goodfood/order-service/internal/adapter/menuclient"
	"goodfood/order-service/internal/adapter/messaging"
	"goodfood/order-service/internal/adapter/outbox"
	"goodfood/order-service/internal/adapter/paymentclient"
	"goodfood/order-service/internal/adapter/postgres"
	"goodfood/order-service/internal/adapter/promoclient"
	"goodfood/order-service/internal/adapter/stockclient"
	"goodfood/order-service/internal/application"
	"goodfood/order-service/internal/config"
	"goodfood/order-service/internal/domain"
	"goodfood/order-service/internal/platform/httpclient"
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

	// menu-service is both the recipe source (stock) and the price authority.
	menu := menuclient.New(cfg.MenuServiceURL, cfg.ServiceAPIKey, httpclient.New("menu-service", httpclient.Settings{}))
	var restaurants domain.RestaurantService
	if cfg.FranchiseServiceURL != "" {
		restaurants = franchiseclient.New(cfg.FranchiseServiceURL, cfg.ServiceAPIKey, httpclient.New("franchise-service", httpclient.Settings{}))
		log.Info().Str("franchise_service", cfg.FranchiseServiceURL).Msg("opening hours, delivery zone and fees checked with franchise-service")
	} else {
		log.Warn().Msg("FRANCHISE_SERVICE_URL not set: opening hours, delivery zone and fees are not checked")
	}
	uc := application.NewUseCases(application.Deps{
		Orders:   postgres.NewOrderRepository(pool),
		// Payments go through Stripe: a longer timeout than the other calls.
		Payments: paymentclient.New(cfg.PaymentServiceURL, cfg.ServiceAPIKey, httpclient.New("payment-service", httpclient.Settings{Timeout: 15 * time.Second})),
		Promos:   promoclient.New(cfg.PromoServiceURL, cfg.ServiceAPIKey, httpclient.New("promo-service", httpclient.Settings{})),
		Recipes:  menu,
		Stock:    stockclient.New(cfg.StockServiceURL, cfg.ServiceAPIKey, httpclient.New("stock-service", httpclient.Settings{})),
		Catalog:  menu,

		Restaurants: restaurants,
	})
	invoices := application.InvoiceDeps{
		Repo: postgres.NewInvoiceRepository(pool),
		Seller: domain.Seller{Name: cfg.CompanyName, Address: cfg.CompanyAddress,
			SIRET: cfg.CompanySIRET, VATNumber: cfg.CompanyVATNumber},
	}
	if cfg.FranchiseServiceURL != "" {
		invoices.Restaurants = franchiseclient.New(cfg.FranchiseServiceURL, cfg.ServiceAPIKey, httpclient.New("franchise-service", httpclient.Settings{}))
	}
	if cfg.SMTPHost != "" {
		invoices.Mailer = mailer.New(mailer.Config{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser,
			Password: cfg.SMTPPassword, From: cfg.SMTPSender, FrontendURL: cfg.FrontendURL})
		log.Info().Str("smtp_host", cfg.SMTPHost).Msg("invoices emailed once the order is paid")
	} else {
		log.Warn().Msg("SMTP_HOST not set: invoices are not emailed (still downloadable)")
	}
	uc.WithInvoices(invoices)
	uc.WithReviews(postgres.NewReviewRepository(pool))
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
	go runStockRetry(runCtx, log, uc, postgres.NewLocker(pool))
	go runInvoices(runCtx, log, uc)

	if cfg.RabbitMQURL != "" {
		publisher := messaging.NewAMQPPublisher(cfg.RabbitMQURL)
		defer publisher.Close()
		go outbox.NewRelay(postgres.NewOutboxRepository(pool), publisher, log).Run(runCtx)
		go messaging.NewPaymentConsumer(cfg.RabbitMQURL, uc.HandlePaymentSucceeded, log).Run(runCtx)
		log.Info().Msg("order events relayed to rabbitmq, payment events consumed")
	} else {
		log.Warn().Msg("RABBITMQ_URL not set: order events stay in the outbox, payments are confirmed by the customer flow only")
	}

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

// syncWorkerLock identifies the retry worker's advisory lock: one replica
// runs each pass, the others skip it.
const syncWorkerLock int64 = 0x6f72646572 // "order"

// runStockRetry replays pending stock synchronisation and refunds until ctx
// ends.
func runStockRetry(ctx context.Context, log zerolog.Logger, uc *application.UseCases, locker *postgres.Locker) {
	timer := time.NewTimer(stockRetryFirstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			ran, err := locker.RunExclusive(ctx, syncWorkerLock, func(ctx context.Context) {
				n := uc.RetryStockSync(ctx, stockRetryBatch)
				r := uc.RetryRefunds(ctx, stockRetryBatch)
				log.Debug().Int("stock", n).Int("refunds", r).Msg("sync retry pass done")
			})
			if err != nil && ctx.Err() == nil {
				log.Warn().Err(err).Msg("sync retry pass skipped")
			} else if !ran {
				log.Debug().Msg("sync retry pass running on another replica")
			}
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

// invoiceInterval: an invoice reaches the customer within a few seconds of
// the payment. Every replica runs the worker: each invoice is locked while
// it is sent (SKIP LOCKED), so the load spreads and none is sent twice.
const (
	invoiceInterval = 10 * time.Second
	invoiceBatch    = 50
)

func runInvoices(ctx context.Context, log zerolog.Logger, uc *application.UseCases) {
	ticker := time.NewTicker(invoiceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := uc.SendPendingInvoices(ctx, invoiceBatch)
			if err != nil {
				log.Warn().Err(err).Msg("invoice pass failed")
			} else if n > 0 {
				log.Info().Int("count", n).Msg("invoices processed")
			}
		}
	}
}
