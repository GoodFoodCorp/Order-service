package http

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog"

	"goodfood/order-service/internal/platform/metrics"
)

// HealthChecker lets the router expose readiness without knowing about pgx.
type HealthChecker func(ctx context.Context) error

// NewRouter assembles middlewares and routes.
func NewRouter(handler *OrderHandler, jwtCfg JWTConfig, serviceKey string, log zerolog.Logger, dbCheck HealthChecker) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(metrics.Middleware)
	r.Use(chimw.Compress(5))
	r.Use(RequestID)
	r.Use(Logger(log))

	// Sondes directes K8s (si tes probes kubelet tapent /healthz ou /readyz en interne)
	registerHealthRoutes(r, dbCheck)
	// Scraped by Prometheus inside the cluster; the ingress only routes /api.
	r.Handle("/metrics", metrics.Handler())

	r.Route("/api/orders", func(r chi.Router) {
		// Health checks accessibles aussi via Ingress
		registerHealthRoutes(r, dbCheck)

		// Documentation publique
		r.Get("/docs/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/yaml")
			_, _ = w.Write(openAPISpec)
		})
		r.Get("/docs", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(scalarHTML)
		})

		// Routes protégées par JWT
		r.Group(func(r chi.Router) {
			r.Use(Auth(jwtCfg))

			r.Post("/", handler.Create)
			r.Get("/", handler.List)
			r.Get("/{id}", handler.GetByID)
			r.Get("/{id}/invoice", handler.Invoice)
			r.Post("/{id}/review", handler.Review)
			r.Get("/{id}/review", handler.GetReview)
			r.Post("/{id}/payment-intent", handler.CreatePaymentIntent)
			r.Post("/{id}/confirm", handler.Confirm)
			r.Post("/{id}/cancel", handler.Cancel)
			r.Patch("/{id}/status", handler.UpdateStatus)
		})
	})

	// Verified reviews: read by anyone, moderated by head office.
	r.Route("/api/reviews", func(r chi.Router) {
		r.Get("/", handler.ListReviews)
		r.Get("/summary", handler.ReviewSummaries)
		r.With(Auth(jwtCfg)).Delete("/{id}", handler.DeleteReview)
	})

	// Service-to-service routes for delivery-service, never exposed by the
	// ingress (/api only).
	r.Route("/internal/orders", func(r chi.Router) {
		r.Use(ServiceKey(serviceKey))
		r.Get("/ready-for-delivery", handler.ListClaimableInternal)
		// Sales for analytics-service: what head office and franchisees see
		// aggregated, without any customer data.
		r.Get("/sales", handler.SalesInternal)
		r.Get("/{id}", handler.GetInternal)
		r.Put("/{id}/courier", handler.AssignCourierInternal)
		r.Delete("/{id}/courier/{courierId}", handler.UnassignCourierInternal)
		r.Patch("/{id}/status", handler.UpdateStatusInternal)
	})

	return r
}

func registerHealthRoutes(r chi.Router, dbCheck HealthChecker) {
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
}
