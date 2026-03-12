package scaffold

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChangeIsRelevant(t *testing.T) {
	tests := []struct {
		name    string
		change  Change
		opts    Options
		want    bool
	}{
		{
			name:   "no relevant options is always relevant",
			change: Change{RelevantOptions: nil},
			opts:   Options{},
			want:   true,
		},
		{
			name:   "empty relevant options is always relevant",
			change: Change{RelevantOptions: []string{}},
			opts:   Options{},
			want:   true,
		},
		{
			name:   "websockets relevant when enabled",
			change: Change{RelevantOptions: []string{"websockets"}},
			opts:   Options{WebSockets: true},
			want:   true,
		},
		{
			name:   "websockets not relevant when disabled",
			change: Change{RelevantOptions: []string{"websockets"}},
			opts:   Options{WebSockets: false},
			want:   false,
		},
		{
			name:   "auth relevant when set",
			change: Change{RelevantOptions: []string{"auth"}},
			opts:   Options{Auth: "session"},
			want:   true,
		},
		{
			name:   "auth not relevant when none",
			change: Change{RelevantOptions: []string{"auth"}},
			opts:   Options{Auth: "none"},
			want:   false,
		},
		{
			name:   "multiple options one match",
			change: Change{RelevantOptions: []string{"stripe", "locale"}},
			opts:   Options{Locale: true},
			want:   true,
		},
		{
			name:   "multiple options no match",
			change: Change{RelevantOptions: []string{"stripe", "locale"}},
			opts:   Options{},
			want:   false,
		},
		{
			name:   "storage relevant when set",
			change: Change{RelevantOptions: []string{"storage"}},
			opts:   Options{Storage: "s3"},
			want:   true,
		},
		{
			name:   "storage not relevant when none",
			change: Change{RelevantOptions: []string{"storage"}},
			opts:   Options{Storage: "none"},
			want:   false,
		},
		{
			name:   "database relevant when set",
			change: Change{RelevantOptions: []string{"database"}},
			opts:   Options{Database: "postgres"},
			want:   true,
		},
		{
			name:   "unknown option key",
			change: Change{RelevantOptions: []string{"nonexistent"}},
			opts:   Options{WebSockets: true},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.change.IsRelevant(tt.opts))
		})
	}
}

func TestChangesReturnsRegistry(t *testing.T) {
	got := Changes()
	assert.NotEmpty(t, got, "changes registry should contain scaffold change entries")
	for _, c := range got {
		assert.NotEmpty(t, c.Title, "change must have a title")
		assert.NotEmpty(t, c.Since, "change must have a since version")
		assert.NotEmpty(t, c.Category, "change must have a category")
		assert.NotEmpty(t, c.AffectedScaffoldFiles, "change must list affected files")
	}
}
