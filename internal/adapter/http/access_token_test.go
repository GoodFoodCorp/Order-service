package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var testJWT = JWTConfig{Secret: "test-secret", Issuer: "goodfood-auth", Audience: "goodfood-api"}

func signTestToken(t *testing.T, overrides jwt.MapClaims) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": "user-1", "typ": "access", "iss": testJWT.Issuer, "aud": testJWT.Audience,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range overrides {
		if v == nil {
			delete(claims, k)
			continue
		}
		claims[k] = v
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testJWT.Secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseAccessToken(t *testing.T) {
	if _, err := parseAccessToken(signTestToken(t, nil), testJWT); err != nil {
		t.Fatalf("valid access token refused: %v", err)
	}
	cases := map[string]jwt.MapClaims{
		"refresh token":   {"typ": "refresh"},
		"untyped legacy":  {"typ": nil},
		"other audience":  {"aud": "elsewhere"},
		"other issuer":    {"iss": "elsewhere"},
		"expired":         {"exp": time.Now().Add(-time.Hour).Unix()},
		"no expiry claim": {"exp": nil},
	}
	for name, overrides := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseAccessToken(signTestToken(t, overrides), testJWT); err == nil {
				t.Fatal("token must be refused")
			}
		})
	}
	other := testJWT
	other.Secret = "another-secret"
	if _, err := parseAccessToken(signTestToken(t, nil), other); err == nil {
		t.Fatal("token signed with another key must be refused")
	}
}

func TestServiceKey(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	call := func(expected, provided string) int {
		req := httptest.NewRequest(http.MethodPost, "/internal/x", nil)
		if provided != "" {
			req.Header.Set(ServiceKeyHeader, provided)
		}
		rec := httptest.NewRecorder()
		ServiceKey(expected)(ok).ServeHTTP(rec, req)
		return rec.Code
	}
	if call("k", "k") != http.StatusNoContent {
		t.Fatal("valid key refused")
	}
	for _, tc := range [][2]string{{"k", ""}, {"k", "wrong"}, {"", ""}, {"", "anything"}} {
		if call(tc[0], tc[1]) != http.StatusUnauthorized {
			t.Fatalf("expected 401 for %q/%q", tc[0], tc[1])
		}
	}
}
