func NewRouter(handler *OrderHandler, jwtSecret string, log zerolog.Logger, dbCheck HealthChecker) http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.Recoverer)
	r.Use(RequestID)
	r.Use(Logger(log))

	// Sondes directes K8s (si tes probes kubelet tapent /healthz ou /readyz en interne)
	registerHealthRoutes(r, dbCheck)

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
			r.Use(Auth(jwtSecret))

			r.Post("/", handler.Create)
			r.Get("/", handler.List)
			r.Get("/ready-for-delivery", handler.ListReadyForDelivery)
			r.Get("/{id}", handler.GetByID)
			r.Post("/{id}/payment-intent", handler.CreatePaymentIntent)
			r.Post("/{id}/confirm", handler.Confirm)
			r.Patch("/{id}/status", handler.UpdateStatus)
		})
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