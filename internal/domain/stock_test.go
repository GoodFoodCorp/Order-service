package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	burgerID = "menu-burger"
	wrapID   = "menu-wrap"
	friesID  = "menu-fries"
	steakID  = "stock-steak"
	breadID  = "stock-bread"
)

func TestRequiredIngredients_multiplies_line_quantity_by_recipe_quantity(t *testing.T) {
	items := []OrderItem{{MenuItemID: burgerID, Quantity: 2}}
	recipes := map[string][]StockLine{burgerID: {{StockItemID: steakID, Quantity: 150}}}

	got := RequiredIngredients(items, recipes)

	assert.Equal(t, []StockLine{{StockItemID: steakID, Quantity: 300}}, got)
}

func TestRequiredIngredients_sums_same_ingredient_across_lines_and_dishes(t *testing.T) {
	items := []OrderItem{
		{MenuItemID: burgerID, Quantity: 2},
		{MenuItemID: wrapID, Quantity: 1},
		{MenuItemID: burgerID, Quantity: 1},
	}
	recipes := map[string][]StockLine{
		burgerID: {{StockItemID: steakID, Quantity: 150}, {StockItemID: breadID, Quantity: 1}},
		wrapID:   {{StockItemID: breadID, Quantity: 0.5}},
	}

	got := RequiredIngredients(items, recipes)

	// sorted by StockItemID: bread < steak
	assert.Equal(t, []StockLine{
		{StockItemID: breadID, Quantity: 3.5},
		{StockItemID: steakID, Quantity: 450},
	}, got)
}

func TestRequiredIngredients_rounds_to_three_decimals_and_resists_float_noise(t *testing.T) {
	items := []OrderItem{{MenuItemID: burgerID, Quantity: 1}, {MenuItemID: wrapID, Quantity: 1}, {MenuItemID: friesID, Quantity: 3}}
	recipes := map[string][]StockLine{
		burgerID: {{StockItemID: breadID, Quantity: 0.1}},
		wrapID:   {{StockItemID: breadID, Quantity: 0.2}},
		friesID:  {{StockItemID: steakID, Quantity: 0.3333}},
	}

	got := RequiredIngredients(items, recipes)

	require.Len(t, got, 2)
	assert.Equal(t, StockLine{StockItemID: breadID, Quantity: 0.3}, got[0])
	assert.Equal(t, StockLine{StockItemID: steakID, Quantity: 1}, got[1])
}

func TestRequiredIngredients_ignores_dishes_without_recipe(t *testing.T) {
	items := []OrderItem{{MenuItemID: burgerID, Quantity: 1}, {MenuItemID: friesID, Quantity: 4}}
	recipes := map[string][]StockLine{burgerID: {{StockItemID: steakID, Quantity: 150}}}

	got := RequiredIngredients(items, recipes)

	assert.Equal(t, []StockLine{{StockItemID: steakID, Quantity: 150}}, got)
}

func TestRequiredIngredients_returns_empty_when_no_dish_has_a_recipe(t *testing.T) {
	items := []OrderItem{{MenuItemID: friesID, Quantity: 4}}

	assert.Empty(t, RequiredIngredients(items, map[string][]StockLine{}))
	assert.Empty(t, RequiredIngredients(items, nil))
}

func TestShortageMessage_names_dish_and_ingredient(t *testing.T) {
	items := []OrderItem{
		{MenuItemID: friesID, MenuItemName: "Frites", Quantity: 1},
		{MenuItemID: wrapID, MenuItemName: "Wrap", Quantity: 1},
		{MenuItemID: burgerID, MenuItemName: "Burger", Quantity: 2},
	}
	recipes := map[string][]StockLine{
		burgerID: {{StockItemID: steakID, Quantity: 150}},
		wrapID:   {{StockItemID: breadID, Quantity: 1}},
	}
	missing := []MissingStock{{StockItemID: steakID, Name: "Steak haché", Unit: "g", Required: 300, OnHand: 120.5}}

	got := ShortageMessage(items, recipes, missing)

	assert.Equal(t, "Stock insuffisant pour « Burger » : Steak haché (besoin 300 g, disponible 120.5)", got)
}

func TestShortageMessage_picks_first_order_dish_containing_the_item(t *testing.T) {
	items := []OrderItem{
		{MenuItemID: wrapID, MenuItemName: "Wrap", Quantity: 1},
		{MenuItemID: burgerID, MenuItemName: "Burger", Quantity: 1},
	}
	recipes := map[string][]StockLine{
		burgerID: {{StockItemID: breadID, Quantity: 1}},
		wrapID:   {{StockItemID: breadID, Quantity: 1}},
	}
	missing := []MissingStock{{StockItemID: breadID, Name: "Pain", Unit: "pc", Required: 2, OnHand: 0}}

	assert.Contains(t, ShortageMessage(items, recipes, missing), "« Wrap »")
}

func TestShortageMessage_without_missing_is_generic(t *testing.T) {
	assert.Equal(t, "Stock insuffisant", ShortageMessage(nil, nil, nil))
}
