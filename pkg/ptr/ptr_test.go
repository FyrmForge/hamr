package ptr_test

import (
	"testing"

	"github.com/FyrmForge/hamr/pkg/ptr"
)

func TestTo(t *testing.T) {
	p := ptr.To(42)
	if *p != 42 {
		t.Fatalf("To(42) = %d, want 42", *p)
	}
}

func TestFrom_nonNil(t *testing.T) {
	v := 7
	got := ptr.From(&v)
	if got != 7 {
		t.Fatalf("From(&7) = %d, want 7", got)
	}
}

func TestFrom_nil(t *testing.T) {
	got := ptr.From((*int)(nil))
	if got != 0 {
		t.Fatalf("From[int](nil) = %d, want 0", got)
	}
}

func TestFromOr_nonNil(t *testing.T) {
	v := "hello"
	got := ptr.FromOr(&v, "default")
	if got != "hello" {
		t.Fatalf("FromOr = %q, want %q", got, "hello")
	}
}

func TestFromOr_nil(t *testing.T) {
	got := ptr.FromOr((*string)(nil), "default")
	if got != "default" {
		t.Fatalf("FromOr(nil) = %q, want %q", got, "default")
	}
}

func TestString(t *testing.T) {
	s := "abc"
	if ptr.String(&s) != "abc" {
		t.Fatal("String non-nil failed")
	}
	if ptr.String(nil) != "" {
		t.Fatal("String nil failed")
	}
}

func TestInt(t *testing.T) {
	i := 99
	if ptr.Int(&i) != 99 {
		t.Fatal("Int non-nil failed")
	}
	if ptr.Int(nil) != 0 {
		t.Fatal("Int nil failed")
	}
}

func TestBool(t *testing.T) {
	b := true
	if ptr.Bool(&b) != true {
		t.Fatal("Bool non-nil failed")
	}
	if ptr.Bool(nil) != false {
		t.Fatal("Bool nil failed")
	}
}

func TestIntToStr(t *testing.T) {
	i := 42
	if got := ptr.IntToStr(&i); got != "42" {
		t.Fatalf("IntToStr(&42) = %q, want %q", got, "42")
	}
	if got := ptr.IntToStr(nil); got != "" {
		t.Fatalf("IntToStr(nil) = %q, want %q", got, "")
	}
}

func TestBoolToYesNo(t *testing.T) {
	tests := []struct {
		name string
		in   *bool
		want string
	}{
		{"true", ptr.To(true), "Yes"},
		{"false", ptr.To(false), "No"},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ptr.BoolToYesNo(tt.in); got != tt.want {
				t.Fatalf("BoolToYesNo = %q, want %q", got, tt.want)
			}
		})
	}
}
