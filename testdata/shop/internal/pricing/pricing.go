// Package pricing prices an order, to the cent.
package pricing

import "github.com/shopspring/decimal"

// LineItem is one line of an order. The fields are in the order they were
// added to the storefront, not the order that packs well.
type LineItem struct {
	Gift    bool
	Qty     int64
	Taxable bool
	SKU     string
	Price   decimal.Decimal
}

// TaxRate is what a taxable line is charged on top of its price.
var TaxRate = decimal.RequireFromString("0.0825")

// Subtotal is the price times the quantity.
func (l LineItem) Subtotal() decimal.Decimal {
	return l.Price.Mul(decimal.NewFromInt(l.Qty))
}

// Total is what an order of these lines costs, tax included, rounded to the
// cent.
func Total(items []LineItem) decimal.Decimal {
	total := decimal.Zero
	for _, it := range items {
		sub := it.Subtotal()
		if it.Taxable {
			sub = sub.Add(sub.Mul(TaxRate))
		}
		total = total.Add(sub)
	}
	return total.Round(2)
}

// Sample is one order's lines, to try things against.
func Sample() []LineItem {
	return []LineItem{
		{SKU: "MUG-01", Qty: 2, Price: decimal.RequireFromString("12.50"), Taxable: true},
		{SKU: "TEE-M", Qty: 1, Price: decimal.RequireFromString("24.00"), Taxable: true},
		{SKU: "CARD", Qty: 1, Price: decimal.RequireFromString("3.10"), Gift: true},
	}
}
