package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"
)

// HealthChecker lets the router expose readiness without knowing about pgx.
type HealthChecker func(ctx context.Context) error

// NewRouter assembles middlewares and routes.
func NewRouter(handler *OrderHandler, jwtSecret string, log zerolog.Logger, dbCheck HealthChecker) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(RequestID)
	r.Use(Logger(log))

	// Liveness / readiness (K8s probes)
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := dbCheck(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	// OpenAPI spec + Scalar UI
	r.Get("/docs/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(openAPISpec)
	})
	r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(scalarHTML)
	})

	r.Route("/api/orders", func(r chi.Router) {
		r.Use(Auth(jwtSecret))
		r.Post("/", handler.Create)
		r.Get("/", handler.List)
		// Registered before /{id} so "ready-for-delivery" is not captured as an id.
		r.Get("/ready-for-delivery", handler.ListReadyForDelivery)
		r.Get("/{id}", handler.GetByID)
		r.Post("/{id}/payment-intent", handler.CreatePaymentIntent)
		r.Post("/{id}/confirm", handler.Confirm)
		r.Patch("/{id}/status", handler.UpdateStatus)
	})

	return r
}
