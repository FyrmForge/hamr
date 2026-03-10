package i18n

import "testing"

func TestPluralRules(t *testing.T) {
	tests := []struct {
		lang string
		n    int
		want PluralCategory
	}{
		// English
		{"en", 0, Other},
		{"en", 1, One},
		{"en", 2, Other},
		{"en", 100, Other},

		// French (0 is "one")
		{"fr", 0, One},
		{"fr", 1, One},
		{"fr", 2, Other},

		// Polish
		{"pl", 1, One},
		{"pl", 2, Few},
		{"pl", 3, Few},
		{"pl", 4, Few},
		{"pl", 5, Many},
		{"pl", 12, Many},
		{"pl", 22, Few},
		{"pl", 25, Many},

		// Russian
		{"ru", 1, One},
		{"ru", 2, Few},
		{"ru", 5, Many},
		{"ru", 11, Many},
		{"ru", 21, One},
		{"ru", 25, Many},

		// Arabic
		{"ar", 0, Zero},
		{"ar", 1, One},
		{"ar", 2, Two},
		{"ar", 5, Few},
		{"ar", 11, Many},
		{"ar", 100, Other},

		// Japanese (always other)
		{"ja", 0, Other},
		{"ja", 1, Other},
		{"ja", 100, Other},

		// Czech
		{"cs", 1, One},
		{"cs", 3, Few},
		{"cs", 5, Other},
	}
	for _, tt := range tests {
		got := RuleFor(tt.lang)(tt.n)
		if got != tt.want {
			t.Errorf("RuleFor(%q)(%d) = %q, want %q", tt.lang, tt.n, got, tt.want)
		}
	}
}

func TestRuleForRegionTag(t *testing.T) {
	// fr-CA should use French rules (0 is "one").
	rule := RuleFor("fr-CA")
	if got := rule(0); got != One {
		t.Errorf("fr-CA: rule(0) = %q, want %q", got, One)
	}
	if got := rule(2); got != Other {
		t.Errorf("fr-CA: rule(2) = %q, want %q", got, Other)
	}

	// ar-SA should use Arabic rules.
	rule = RuleFor("ar-SA")
	if got := rule(0); got != Zero {
		t.Errorf("ar-SA: rule(0) = %q, want %q", got, Zero)
	}
	if got := rule(2); got != Two {
		t.Errorf("ar-SA: rule(2) = %q, want %q", got, Two)
	}
}

func TestRuleForUnknownLanguageFallsBackToEnglish(t *testing.T) {
	rule := RuleFor("xx")
	if got := rule(1); got != One {
		t.Errorf("unknown lang: rule(1) = %q, want %q", got, One)
	}
	if got := rule(2); got != Other {
		t.Errorf("unknown lang: rule(2) = %q, want %q", got, Other)
	}
}

func TestRegisterRule(t *testing.T) {
	RegisterRule("xx", func(n int) PluralCategory {
		if n == 42 {
			return Few
		}
		return Other
	})
	defer func() {
		customRulesMu.Lock()
		delete(customRules, "xx")
		customRulesMu.Unlock()
	}()

	rule := RuleFor("xx")
	if got := rule(42); got != Few {
		t.Errorf("custom rule(42) = %q, want %q", got, Few)
	}
	if got := rule(1); got != Other {
		t.Errorf("custom rule(1) = %q, want %q", got, Other)
	}
}
