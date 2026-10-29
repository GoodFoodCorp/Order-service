package http

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// JWTConfig is what an access token issued by auth-service must match.
type JWTConfig struct {
	Secret   string
	Issuer   string
	Audience string
}

var errNotAccessToken = errors.New("not an access token")

// parseAccessToken verifies an HS256 access token: signature, expiry, issuer,
// audience and typ=access. Refresh tokens (signed with another key and typed
// "refresh"), unsigned tokens and tokens minted for another audience are all
// refused.
func parseAccessToken(raw string, cfg JWTConfig) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(raw, claims,
		func(*jwt.Token) (any, error) { return []byte(cfg.Secret), nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithAudience(cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return nil, err
	}
	if !token.Valid || claims["typ"] != "access" {
		return nil, errNotAccessToken
	}
	return claims, nil
}

// ServiceKeyHeader carries the shared secret of service-to-service calls.
const ServiceKeyHeader = "X-Service-Key"

// ServiceKey guards the /internal routes: the caller must present the shared
// key. With no key configured the routes stay closed.
func ServiceKey(expected string) func(http.Handler) http.Handler {
	expectedHash := sha256.Sum256([]byte(expected))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			provided := r.Header.Get(ServiceKeyHeader)
			providedHash := sha256.Sum256([]byte(provided))
			if expected == "" || provided == "" || subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
				writeError(w, r, http.StatusUnauthorized, "invalid or missing service key")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
