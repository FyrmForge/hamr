package repo

import (
	"context"
	"time"
)

// UserStore is the user sub-store, reached via Store.Users().
type UserStore interface {
	// GetByID returns a user by ID, or nil if not found.
	GetByID(ctx context.Context, id string) (*User, error)

	// GetByEmail returns a user by email, or nil if not found.
	GetByEmail(ctx context.Context, email string) (*User, error)

	// Create inserts a new user.
	Create(ctx context.Context, user *User) error
}

// User represents a user account.
type User struct {
	ID           string    `db:"id"`
	Email        string    `db:"email"`
	PasswordHash string    `db:"password_hash"`
	Name         string    `db:"name"`
	Role         string    `db:"role"`
	Active       bool      `db:"active"`
	CreatedAt    time.Time `db:"created_at"`
	UpdatedAt    time.Time `db:"updated_at"`
}
