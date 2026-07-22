package application

import "goodfood/order-service/internal/domain"

// Actor is the authenticated caller, extracted from the JWT by the HTTP
// adapter. The application layer enforces ownership/role rules with it and
// stays free of any HTTP concern. Token is the raw JWT, forwarded on
// service-to-service calls (payment-service).
type Actor struct {
	UserID    string
	TenantID  string
	RoleSlugs []string
	Token     string
}

func (a Actor) HasRole(slug string) bool {
	for _, r := range a.RoleSlugs {
		if r == slug {
			return true
		}
	}
	return false
}

// Role slugs as issued by the auth-service (see ADR-002).
const (
	RoleAdmin   = "admin"   // head office
	RoleManager = "manager" // franchisee
	RoleUser    = "user"    // customer
	RoleCourier = "livreur" // delivery courier
)

// UseCases wires the domain ports together; one method per use case,
// implemented in one file per use case.
type UseCases struct {
	orders   domain.OrderRepository
	payments domain.PaymentService
}

func NewUseCases(orders domain.OrderRepository, payments domain.PaymentService) *UseCases {
	return &UseCases{orders: orders, payments: payments}
}
