// Package migrate is the shop's table set in the shape ent generates it:
// entc writes this file as ent/migrate/schema.go from the schema types. It is
// written out by hand here, so the fixture needs no code generation, and it
// holds exactly what a generated one would — descriptors, and no connection.
package migrate

import (
	"entgo.io/ent/dialect/sql/schema"
	"entgo.io/ent/schema/field"
)

var (
	// UsersColumns holds the columns for the "users" table.
	UsersColumns = []*schema.Column{
		{Name: "id", Type: field.TypeInt, Increment: true},
		{Name: "email", Type: field.TypeString, Unique: true},
		{Name: "plan", Type: field.TypeEnum, Enums: []string{"free", "pro", "team"}, Default: "free"},
		{Name: "created", Type: field.TypeTime},
	}
	// UsersTable holds the schema information for the "users" table.
	UsersTable = &schema.Table{
		Name:       "users",
		Columns:    UsersColumns,
		PrimaryKey: []*schema.Column{UsersColumns[0]},
	}
	// OrdersColumns holds the columns for the "orders" table.
	OrdersColumns = []*schema.Column{
		{Name: "id", Type: field.TypeInt, Increment: true},
		{Name: "total", Type: field.TypeString},
		{Name: "status", Type: field.TypeEnum, Enums: []string{"placed", "paid", "shipped"}},
		{Name: "placed_at", Type: field.TypeTime},
		{Name: "user_orders", Type: field.TypeInt, Nullable: true},
	}
	// OrdersTable holds the schema information for the "orders" table.
	OrdersTable = &schema.Table{
		Name:       "orders",
		Columns:    OrdersColumns,
		PrimaryKey: []*schema.Column{OrdersColumns[0]},
		ForeignKeys: []*schema.ForeignKey{
			{
				Symbol:     "orders_users_orders",
				Columns:    []*schema.Column{OrdersColumns[4]},
				RefColumns: []*schema.Column{UsersColumns[0]},
				OnDelete:   schema.SetNull,
			},
		},
	}
	// ProductsColumns holds the columns for the "products" table.
	ProductsColumns = []*schema.Column{
		{Name: "id", Type: field.TypeInt, Increment: true},
		{Name: "sku", Type: field.TypeString, Unique: true},
		{Name: "price", Type: field.TypeString},
	}
	// ProductsTable holds the schema information for the "products" table.
	ProductsTable = &schema.Table{
		Name:       "products",
		Columns:    ProductsColumns,
		PrimaryKey: []*schema.Column{ProductsColumns[0]},
	}
	// OrderItemsColumns holds the columns for the "order_items" table.
	OrderItemsColumns = []*schema.Column{
		{Name: "order_id", Type: field.TypeInt},
		{Name: "product_id", Type: field.TypeInt},
	}
	// OrderItemsTable holds the schema information for the "order_items" table.
	OrderItemsTable = &schema.Table{
		Name:       "order_items",
		Columns:    OrderItemsColumns,
		PrimaryKey: []*schema.Column{OrderItemsColumns[0], OrderItemsColumns[1]},
		ForeignKeys: []*schema.ForeignKey{
			{
				Symbol:     "order_items_order_id",
				Columns:    []*schema.Column{OrderItemsColumns[0]},
				RefColumns: []*schema.Column{OrdersColumns[0]},
				OnDelete:   schema.Cascade,
			},
			{
				Symbol:     "order_items_product_id",
				Columns:    []*schema.Column{OrderItemsColumns[1]},
				RefColumns: []*schema.Column{ProductsColumns[0]},
				OnDelete:   schema.Cascade,
			},
		},
	}
	// Tables holds all the tables in the schema.
	Tables = []*schema.Table{
		UsersTable,
		OrdersTable,
		ProductsTable,
		OrderItemsTable,
	}
)

func init() {
	OrdersTable.ForeignKeys[0].RefTable = UsersTable
	OrderItemsTable.ForeignKeys[0].RefTable = OrdersTable
	OrderItemsTable.ForeignKeys[1].RefTable = ProductsTable
}
