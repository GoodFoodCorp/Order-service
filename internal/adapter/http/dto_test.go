package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"goodfood/order-service/internal/domain"
)

func TestWriteDomainError_stock_codes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"out of stock is 409", domain.NewOutOfStockError("Stock insuffisant"), http.StatusConflict},
		{"service unavailable is 503", domain.NewUnavailableError("stock down"), http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeDomainError(rec, httptest.NewRequest(http.MethodGet, "/", nil), tt.err)
			assert.Equal(t, tt.want, rec.Code)
		})
	}
}
