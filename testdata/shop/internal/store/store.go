// Package store is the shop's data: SQLite, through sqlx.
package store

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"example.com/shop/internal/config"
)

// User is a customer.
type User struct {
	ID      int64     `db:"id" json:"id" csv:"id"`
	Email   string    `db:"email" json:"email" csv:"email"`
	Plan    string    `db:"plan" json:"plan" csv:"plan"`
	Created time.Time `db:"created" json:"created" csv:"created"`
}

// LogValue keeps an address out of the logs: a user is logged by id and plan,
// and the address is masked down to its first letter and its domain.
func (u User) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int64("id", u.ID),
		slog.String("plan", u.Plan),
		slog.String("email", mask(u.Email)),
	)
}

func mask(email string) string {
	name, domain, ok := strings.Cut(email, "@")
	if !ok || name == "" {
		return "***"
	}
	return name[:1] + "***@" + domain
}

// Order is one order, as the orders table holds it.
type Order struct {
	ID       int64     `db:"id" json:"id"`
	UserID   int64     `db:"user_id" json:"user_id"`
	Total    string    `db:"total" json:"total"`
	Status   string    `db:"status" json:"status"`
	PlacedAt time.Time `db:"placed_at" json:"placed_at"`
}

// The queries, with sqlx's named parameters and its IN (?) expansion.
const (
	UsersOnPlan = `SELECT id, email, plan, created FROM users WHERE plan = :plan ORDER BY id`
	UsersByID   = `SELECT id, email, plan, created FROM users WHERE id IN (?) ORDER BY id`
)

// Store is the database.
type Store struct{ db *sqlx.DB }

// Open opens the database file at path.
func Open(path string) (*Store, error) {
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Default opens the database the settings name.
func Default() (*Store, error) {
	v, err := config.Load()
	if err != nil {
		return nil, err
	}
	return Open(config.Resolve(v, "database.path"))
}

// DB is the connection, for the queries this package does not name.
func (s *Store) DB() *sqlx.DB { return s.db }

// User is one customer.
func (s *Store) User(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.db.GetContext(ctx, &u, `SELECT id, email, plan, created FROM users WHERE id = ?`, id)
	return u, err
}

// Users is every customer, in id order.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	var us []User
	err := s.db.SelectContext(ctx, &us, `SELECT id, email, plan, created FROM users ORDER BY id`)
	return us, err
}

// OnPlan is every customer on a plan.
func (s *Store) OnPlan(ctx context.Context, plan string) ([]User, error) {
	q, args, err := sqlx.Named(UsersOnPlan, map[string]any{"plan": plan})
	if err != nil {
		return nil, err
	}
	var us []User
	err = s.db.SelectContext(ctx, &us, s.db.Rebind(q), args...)
	return us, err
}

// ByID is the customers with these ids.
func (s *Store) ByID(ctx context.Context, ids ...int64) ([]User, error) {
	q, args, err := sqlx.In(UsersByID, ids)
	if err != nil {
		return nil, err
	}
	var us []User
	err = s.db.SelectContext(ctx, &us, s.db.Rebind(q), args...)
	return us, err
}

// Orders is every order a customer placed.
func (s *Store) Orders(ctx context.Context, userID int64) ([]Order, error) {
	var os []Order
	err := s.db.SelectContext(ctx, &os, `SELECT id, user_id, total, status, placed_at FROM orders WHERE user_id = ? ORDER BY id`, userID)
	return os, err
}
