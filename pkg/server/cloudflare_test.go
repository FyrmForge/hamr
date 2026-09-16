package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func realIP(s *Server, peer, xff string) string {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	req.Header.Set("X-Forwarded-For", xff)
	return s.Echo().IPExtractor(req)
}

func fakeCloudflare(t *testing.T, status int, body string) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	old := cloudflareIPsURL
	cloudflareIPsURL = ts.URL
	t.Cleanup(func() { cloudflareIPsURL = old })
}

func TestCloudflareKeyword_usesPinnedList(t *testing.T) {
	s, err := New(WithTrustedProxies(" Cloudflare ", "10.0.0.5/32"))
	require.NoError(t, err)
	assert.True(t, s.cloudflare)
	assert.Equal(t, []string{"10.0.0.5/32"}, s.trustedProxies)

	// Cloudflare edge (pinned 104.16.0.0/13) peer is trusted.
	assert.Equal(t, "1.2.3.4", realIP(s, "104.16.0.1:443", "1.2.3.4"))
	// Stacked: client -> Cloudflare -> own proxy -> app.
	assert.Equal(t, "1.2.3.4", realIP(s, "10.0.0.5:443", "1.2.3.4, 104.16.0.1"))
	// Untrusted peer: XFF ignored.
	assert.Equal(t, "203.0.113.7", realIP(s, "203.0.113.7:443", "1.2.3.4"))
}

func TestCloudflareRefresh_swapsList(t *testing.T) {
	fakeCloudflare(t, http.StatusOK, `{"success":true,"result":{"ipv4_cidrs":["198.51.100.0/24"],"ipv6_cidrs":["2001:db8::/32"]}}`)
	s, err := New(WithTrustedProxies("cloudflare"))
	require.NoError(t, err)

	s.updateCloudflare(context.Background())

	assert.Equal(t, "1.2.3.4", realIP(s, "198.51.100.9:443", "1.2.3.4"), "fetched range trusted")
	assert.Equal(t, "104.16.0.1", realIP(s, "104.16.0.1:443", "1.2.3.4"), "pinned range replaced")
}

func TestCloudflareRefresh_rejectsBadList(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
	}{
		"http error":    {http.StatusInternalServerError, `{}`},
		"bad json":      {http.StatusOK, `not json`},
		"success false": {http.StatusOK, `{"success":false,"result":{"ipv4_cidrs":["198.51.100.0/24"],"ipv6_cidrs":["2001:db8::/32"]}}`},
		"empty v4":      {http.StatusOK, `{"success":true,"result":{"ipv4_cidrs":[],"ipv6_cidrs":["2001:db8::/32"]}}`},
		"empty v6":      {http.StatusOK, `{"success":true,"result":{"ipv4_cidrs":["198.51.100.0/24"],"ipv6_cidrs":[]}}`},
		"invalid cidr":  {http.StatusOK, `{"success":true,"result":{"ipv4_cidrs":["nope"],"ipv6_cidrs":["2001:db8::/32"]}}`},
		"too wide v4":   {http.StatusOK, `{"success":true,"result":{"ipv4_cidrs":["0.0.0.0/0"],"ipv6_cidrs":["2001:db8::/32"]}}`},
		"too wide v6":   {http.StatusOK, `{"success":true,"result":{"ipv4_cidrs":["198.51.100.0/24"],"ipv6_cidrs":["::/0"]}}`},
		"v6 in v4 list": {http.StatusOK, `{"success":true,"result":{"ipv4_cidrs":["2001:db8::/32"],"ipv6_cidrs":["2001:db8::/32"]}}`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fakeCloudflare(t, tc.status, tc.body)
			s, err := New(WithTrustedProxies("cloudflare"))
			require.NoError(t, err)

			s.updateCloudflare(context.Background())

			assert.Equal(t, "1.2.3.4", realIP(s, "104.16.0.1:443", "1.2.3.4"), "pinned list kept")
			assert.Equal(t, "203.0.113.7", realIP(s, "203.0.113.7:443", "1.2.3.4"), "nothing extra trusted")
		})
	}
}
