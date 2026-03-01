package db

import (
	"os"
	"strings"
	"testing"
)

func TestConnectInvalidURL(t *testing.T) {
	_, err := Connect("")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestConnectRetryInvalidDSN(t *testing.T) {
	_, err := Connect("postgres://invalid:5432/nope", WithMaxRetries(1))
	if err == nil {
		t.Fatal("expected error for invalid DSN")
	}
	if !strings.Contains(err.Error(), "db: connecting") {
		t.Fatalf("expected 'db: connecting' in error, got: %v", err)
	}
}

func TestConnectIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	db, err := Connect(dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}
