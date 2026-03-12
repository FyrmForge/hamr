package scaffold

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Version
		wantErr bool
	}{
		{"plain", "0.3.2", Version{0, 3, 2}, false},
		{"with v prefix", "v1.2.3", Version{1, 2, 3}, false},
		{"zeros", "0.0.0", Version{0, 0, 0}, false},
		{"large numbers", "10.20.30", Version{10, 20, 30}, false},
		{"dev suffix", "0.5.0-dev", Version{0, 5, 0}, false},
		{"rc suffix", "1.0.0-rc.1", Version{1, 0, 0}, false},
		{"v prefix with dev", "v0.3.2-dev", Version{0, 3, 2}, false},
		{"missing patch", "1.2", Version{}, true},
		{"too many parts", "1.2.3.4", Version{}, true},
		{"empty string", "", Version{}, true},
		{"non-numeric major", "a.2.3", Version{}, true},
		{"non-numeric minor", "1.b.3", Version{}, true},
		{"non-numeric patch", "1.2.c", Version{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVersion(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestVersionLess(t *testing.T) {
	tests := []struct {
		name string
		a, b Version
		want bool
	}{
		{"major less", Version{0, 9, 9}, Version{1, 0, 0}, true},
		{"major greater", Version{2, 0, 0}, Version{1, 9, 9}, false},
		{"minor less", Version{1, 2, 9}, Version{1, 3, 0}, true},
		{"minor greater", Version{1, 5, 0}, Version{1, 3, 9}, false},
		{"patch less", Version{1, 2, 3}, Version{1, 2, 4}, true},
		{"patch greater", Version{1, 2, 5}, Version{1, 2, 4}, false},
		{"equal", Version{1, 2, 3}, Version{1, 2, 3}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.a.Less(tt.b))
		})
	}
}

func TestVersionString(t *testing.T) {
	assert.Equal(t, "1.2.3", Version{1, 2, 3}.String())
	assert.Equal(t, "0.0.0", Version{0, 0, 0}.String())
}
