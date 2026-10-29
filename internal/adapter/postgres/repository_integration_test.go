//go:build integration

package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"goodfood/order-service/internal/domain"
)

// Run with: TEST_DATABASE_URL=postgres://... go test -tags integration ./internal/adapter/postgres/
func setupRepo(t *testing.T) *OrderRepository {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	if err := Migrate(url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := Connect(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return NewOrderRepository(pool)
}

func newStoredOrder(t *testing.T, repo *OrderRepository, status domain.OrderStatus) *domain.Order {
	t.Helper()
	order, err := domain.NewOrder(uuid.NewString(), uuid.NewString(), "12 rue de Paris", []domain.NewOrderItemInput{
		{MenuItemID: uuid.NewString(), MenuItemName: "Burger", Quantity: 2, UnitPriceCents: 1299},
		{MenuItemID: uuid.NewString(), MenuItemName: "Soda", Quantity: 1, UnitPriceCents: 300},
	})
	if err != nil {
		t.Fatal(err)
	}
	order.Status = status
	if err := repo.Create(context.Background(), order); err != nil {
		t.Fatal(err)
	}
	return order
}

func TestCreateAndReadBack(t *testing.T) {
	repo := setupRepo(t)
	order := newStoredOrder(t, repo, domain.StatusPlaced)

	got, err := repo.GetByID(context.Background(), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalAmountCents != order.TotalAmountCents || len(got.Items) != 2 || got.CourierID != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestAssignCourierIsACompareAndSet(t *testing.T) {
	ctx := context.Background()
	repo := setupRepo(t)
	order := newStoredOrder(t, repo, domain.StatusReadyForPickup)
	first, second := uuid.NewString(), uuid.NewString()

	if applied, err := repo.AssignCourier(ctx, order.ID, first); err != nil || !applied {
		t.Fatalf("first claim: %v %v", applied, err)
	}
	if applied, _ := repo.AssignCourier(ctx, order.ID, first); !applied {
		t.Fatal("re-claim by the same courier must apply")
	}
	if applied, _ := repo.AssignCourier(ctx, order.ID, second); applied {
		t.Fatal("a second courier must lose")
	}
	got, _ := repo.GetByID(ctx, order.ID)
	if !got.IsAssignedTo(first) {
		t.Fatalf("courier = %v", got.CourierID)
	}

	if err := repo.UnassignCourier(ctx, order.ID, second); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetByID(ctx, order.ID); !got.IsAssignedTo(first) {
		t.Fatal("only the assigned courier can be removed")
	}
	if err := repo.UnassignCourier(ctx, order.ID, first); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetByID(ctx, order.ID); got.CourierID != nil {
		t.Fatal("courier not cleared")
	}

	confirmed := newStoredOrder(t, repo, domain.StatusConfirmed)
	if applied, _ := repo.AssignCourier(ctx, confirmed.ID, first); applied {
		t.Fatal("a CONFIRMED order is not claimable yet")
	}
}

func countEvents(t *testing.T, repo *OrderRepository, orderID, routingKey string) int {
	t.Helper()
	var n int
	err := repo.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbox_events WHERE routing_key = $1 AND payload->'data'->>'orderId' = $2`,
		routingKey, orderID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStatusChangesWriteTheirEventAtomically(t *testing.T) {
	ctx := context.Background()
	repo := setupRepo(t)
	order := newStoredOrder(t, repo, domain.StatusInPreparation)

	order.Status = domain.StatusReadyForPickup
	if err := repo.UpdateStatus(ctx, order, domain.StatusInPreparation, domain.NewOrderEvent(order)); err != nil {
		t.Fatal(err)
	}
	// A stale writer still believing IN_PREPARATION: no-op, and no event.
	if err := repo.UpdateStatus(ctx, order, domain.StatusInPreparation, domain.NewOrderEvent(order)); err != nil {
		t.Fatal(err)
	}
	if n := countEvents(t, repo, order.ID, domain.EventOrderReadyForPickup); n != 1 {
		t.Fatalf("ready events = %d, want 1", n)
	}

	order.Status = domain.StatusCancelled
	if err := repo.CancelWithStockSync(ctx, order, domain.NewOrderEvent(order)); err != nil {
		t.Fatal(err)
	}
	if err := repo.CancelWithStockSync(ctx, order, domain.NewOrderEvent(order)); err == nil {
		t.Fatal("a second cancellation must be refused")
	}
	if n := countEvents(t, repo, order.ID, "order.cancelled"); n != 1 {
		t.Fatalf("cancelled events = %d, want 1", n)
	}
}
