package devserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHandleHotkey_GatedUntilReady(t *testing.T) {
	var opened []string
	orig := openBrowser
	openBrowser = func(url string) { opened = append(opened, url) }
	defer func() { openBrowser = orig }()

	r := &Runner{logger: discardLogger(), proxyURL: "http://localhost:3000"}
	cancel := func() {}

	// Not ready: open-browser is ignored (no premature open of a not-yet-bound URL).
	if quit := r.handleHotkey(HotkeyOpenBrowser, nil, cancel); quit {
		t.Fatal("open-browser should not request quit")
	}
	assert.Empty(t, opened, "open-browser must be ignored until ready")

	// Quit always works, even before ready.
	var canceled bool
	if quit := r.handleHotkey(HotkeyQuit, nil, func() { canceled = true }); !quit {
		t.Fatal("quit must be honored during startup")
	}
	assert.True(t, canceled, "quit must cancel even before ready")

	// Once ready, open-browser is honored and uses the bound proxyURL.
	r.ready.Store(true)
	r.handleHotkey(HotkeyOpenBrowser, nil, cancel)
	assert.Equal(t, []string{"http://localhost:3000"}, opened)
}
