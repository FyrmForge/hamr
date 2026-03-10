package i18n

import "testing"

func TestDirectionFor(t *testing.T) {
	tests := []struct {
		lang string
		want string
	}{
		{"en", "ltr"},
		{"fr", "ltr"},
		{"ar", "rtl"},
		{"he", "rtl"},
		{"fa", "rtl"},
		{"ja", "ltr"},
		{"", "ltr"},
		{"ar-SA", "rtl"},
		{"he-IL", "rtl"},
		{"fr-CA", "ltr"},
		{"en-US", "ltr"},
	}
	for _, tt := range tests {
		if got := DirectionFor(tt.lang); got != tt.want {
			t.Errorf("DirectionFor(%q) = %q, want %q", tt.lang, got, tt.want)
		}
	}
}
