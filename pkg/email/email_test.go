package email_test

import (
	"testing"

	"github.com/FyrmForge/hamr/pkg/email"
)

func TestAddr(t *testing.T) {
	t.Parallel()

	a := email.Addr("Ada Lovelace", "ada@example.com")
	if a.Name != "Ada Lovelace" || a.Email != "ada@example.com" {
		t.Fatalf("Addr: got %+v", a)
	}

	bare := email.Addr("", "ops@example.com")
	if bare.Name != "" || bare.Email != "ops@example.com" {
		t.Fatalf("Addr bare: got %+v", bare)
	}
}
