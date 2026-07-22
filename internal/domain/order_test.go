package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validItems() []NewOrderItemInput {
	return []NewOrderItemInput{
		{MenuItemID: "11111111-1111-1111-1111-111111111111", MenuItemName: "Burger Deluxe", Quantity: 2, UnitPriceCents: 1299},
		{MenuItemID: "22222222-2222-2222-2222-222222222222", MenuItemName: "Pizza Margherita", Quantity: 1, UnitPriceCents: 1499},
	}
}

func TestNewOrder(t *testing.T) {
	tests := []struct {
		name            string
		customerID      string
		restaurantID    string
		deliveryAddress string
		items           []NewOrderItemInput
		wantErr         bool
		wantTotal       int64
	}{
		{
			name:       "valid order computes total",
			customerID: "c1", restaurantID: "r1", deliveryAddress: "12 rue de Paris",
			items: validItems(), wantErr: false, wantTotal: 2*1299 + 1499,
		},
		{
			name:       "missing customer",
			customerID: "", restaurantID: "r1", deliveryAddress: "12 rue de Paris",
			items: validItems(), wantErr: true,
		},
		{
			name:       "missing restaurant",
			customerID: "c1", restaurantID: "", deliveryAddress: "12 rue de Paris",
			items: validItems(), wantErr: true,
		},
		{
			name:       "blank delivery address",
			customerID: "c1", restaurantID: "r1", deliveryAddress: "   ",
			items: validItems(), wantErr: true,
		},
		{
			name:       "empty items",
			customerID: "c1", restaurantID: "r1", deliveryAddress: "12 rue de Paris",
			items: nil, wantErr: true,
		},
		{
			name:       "zero quantity",
			customerID: "c1", restaurantID: "r1", deliveryAddress: "12 rue de Paris",
			items:   []NewOrderItemInput{{MenuItemID: "m1", MenuItemName: "Burger", Quantity: 0, UnitPriceCents: 100}},
			wantErr: true,
		},
		{
			name:       "negative price",
			customerID: "c1", restaurantID: "r1", deliveryAddress: "12 rue de Paris",
			items:   []NewOrderItemInput{{MenuItemID: "m1", MenuItemName: "Burger", Quantity: 1, UnitPriceCents: -5}},
			wantErr: true,
		},
		{
			name:       "item without name",
			customerID: "c1", restaurantID: "r1", deliveryAddress: "12 rue de Paris",
			items:   []NewOrderItemInput{{MenuItemID: "m1", MenuItemName: " ", Quantity: 1, UnitPriceCents: 100}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := NewOrder(tt.customerID, tt.restaurantID, tt.deliveryAddress, tt.items)
			if tt.wantErr {
				require.Error(t, err)
				var derr *Error
				require.ErrorAs(t, err, &derr)
				assert.Equal(t, ErrCodeValidation, derr.Code)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, StatusPlaced, order.Status)
			assert.Equal(t, tt.wantTotal, order.TotalAmountCents)
			assert.NotEmpty(t, order.ID)
			assert.Len(t, order.Items, len(tt.items))
			for _, it := range order.Items {
				assert.Equal(t, order.ID, it.OrderID)
				assert.NotEmpty(t, it.ID)
			}
		})
	}
}

func TestOrderTransitions(t *testing.T) {
	tests := []struct {
		from    OrderStatus
		to      OrderStatus
		allowed bool
	}{
		{StatusPlaced, StatusPaymentPending, true},
		{StatusPlaced, StatusCancelled, true},
		{StatusPlaced, StatusConfirmed, false},
		{StatusPaymentPending, StatusConfirmed, true},
		{StatusConfirmed, StatusInPreparation, true},
		{StatusInPreparation, StatusReadyForPickup, true},
		{StatusReadyForPickup, StatusInDelivery, true},
		{StatusInDelivery, StatusDelivered, true},
		{StatusInDelivery, StatusCancelled, false},
		{StatusDelivered, StatusCancelled, false},
		{StatusCancelled, StatusPlaced, false},
		{StatusDelivered, StatusPlaced, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.from)+"->"+string(tt.to), func(t *testing.T) {
			order := &Order{Status: tt.from}
			err := order.TransitionTo(tt.to)
			if tt.allowed {
				require.NoError(t, err)
				assert.Equal(t, tt.to, order.Status)
				if tt.to == StatusConfirmed {
					assert.NotNil(t, order.ConfirmedAt)
				}
			} else {
				require.Error(t, err)
				var derr *Error
				require.ErrorAs(t, err, &derr)
				assert.Equal(t, ErrCodeInvalidTransition, derr.Code)
				assert.Equal(t, tt.from, order.Status, "status must not change on refused transition")
			}
		})
	}
}

func TestParseOrderStatus(t *testing.T) {
	got, err := ParseOrderStatus("ready_for_pickup")
	require.NoError(t, err)
	assert.Equal(t, StatusReadyForPickup, got)

	_, err = ParseOrderStatus("NOT_A_STATUS")
	require.Error(t, err)
}

func TestOrderOwnership(t *testing.T) {
	order := &Order{CustomerID: "user-1"}
	assert.True(t, order.IsOwnedBy("user-1"))
	assert.False(t, order.IsOwnedBy("user-2"))
}
