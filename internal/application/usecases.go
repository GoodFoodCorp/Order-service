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
	// Email is the customer's, from the access token: their invoices go there.
	Email string
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
	promos   domain.PromoService
	recipes  domain.RecipeService
	stock    domain.StockService
	catalog  domain.MenuCatalog
	restos   domain.RestaurantService
	invoices *InvoiceDeps // nil: no invoices
}

// Deps lists the ports the use cases need.
type Deps struct {
	Orders   domain.OrderRepository
	Payments domain.PaymentService
	Promos   domain.PromoService
	Recipes  domain.RecipeService
	Stock    domain.StockService
	Catalog  domain.MenuCatalog
	// Restaurants is optional: without it opening hours and delivery zones
	// are not checked and no delivery fee is charged.
	Restaurants domain.RestaurantService
}

func NewUseCases(d Deps) *UseCases {
	return &UseCases{orders: d.Orders, payments: d.Payments, promos: d.Promos,
		recipes: d.Recipes, stock: d.Stock, catalog: d.Catalog, restos: d.Restaurants}
}
