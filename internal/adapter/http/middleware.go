package http

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"goodfood/order-service/internal/application"
)

type contextKey string

const (
	ctxKeyActor     contextKey = "actor"
	ctxKeyRequestID contextKey = "request_id"

	headerRequestID = "X-Request-ID"
)

// RequestID propagates the correlation id (or creates one) — see NFRs.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(headerRequestID)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(headerRequestID, id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyRequestID, id)))
	})
}

// Logger emits one structured JSON log line per request.
func Logger(log zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			reqID, _ := r.Context().Value(ctxKeyRequestID).(string)
			log.Info().
				Str("request_id", reqID).
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", rw.status).
				Dur("duration_ms", time.Since(start)).
				Msg("http_request")
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Auth validates the HS256 JWT issued by auth-service (shared secret,
// ADR-001) from the Authorization: Bearer header or the auth_token cookie,
// and stores the Actor in the request context.
func Auth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString := ""
			if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
				tokenString = strings.TrimPrefix(h, "Bearer ")
			} else if c, err := r.Cookie("auth_token"); err == nil {
				tokenString = c.Value
			}
			if tokenString == "" {
				writeError(w, r, http.StatusUnauthorized, "missing authentication token")
				return
			}

			token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return []byte(jwtSecret), nil
			})
			if err != nil || !token.Valid {
				writeError(w, r, http.StatusUnauthorized, "invalid or expired token")
				return
			}
			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				writeError(w, r, http.StatusUnauthorized, "invalid token claims")
				return
			}

			// Keep the raw token: it is forwarded to payment-service so the
			// payment stays attributable to the customer who is paying.
			actor := application.Actor{Token: tokenString}
			if sub, ok := claims["sub"].(string); ok {
				actor.UserID = sub
			}
			if tid, ok := claims["tenant_id"].(string); ok {
				actor.TenantID = tid
			}
			if slugs, ok := claims["role_slugs"].([]interface{}); ok {
				for _, s := range slugs {
					if str, ok := s.(string); ok {
						actor.RoleSlugs = append(actor.RoleSlugs, strings.ToLower(str))
					}
				}
			}
			if actor.UserID == "" {
				writeError(w, r, http.StatusUnauthorized, "token has no subject")
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyActor, actor)))
		})
	}
}

func actorFrom(r *http.Request) application.Actor {
	actor, _ := r.Context().Value(ctxKeyActor).(application.Actor)
	return actor
}
