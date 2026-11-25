package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func railwayIP(s *Server, peer string, h map[string]string) string {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for k, v := range h {
		req.Header.Set(k, v)
	}
	return s.Echo().IPExtractor(req)
}

func newRailway(t *testing.T, entries ...string) *Server {
	t.Helper()
	s, err := New(WithTrustedProxies(entries...))
	require.NoError(t, err)
	return s
}

func TestRailway_usesXRealIP(t *testing.T) {
	s := newRailway(t, "railway")
	got := railwayIP(s, "100.64.0.9:5000", map[string]string{
		"X-Real-IP":        "203.0.113.7",
		"X-Forwarded-For":  "6.6.6.6, 100.64.0.1",
		"CF-Connecting-IP": "7.7.7.7",
	})
	assert.Equal(t, "203.0.113.7", got)
}

func TestRailway_missingOrBadXRealIPFallsBackToPeer(t *testing.T) {
	s := newRailway(t, "railway")
	assert.Equal(t, "100.64.0.9", railwayIP(s, "100.64.0.9:5000", nil))
	assert.Equal(t, "100.64.0.9", railwayIP(s, "100.64.0.9:5000", map[string]string{"X-Real-IP": "garbage"}))
}

func TestRailway_multipleXRealIPUsesPeer(t *testing.T) {
	s := newRailway(t, "railway")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "100.64.0.9:5000"
	req.Header.Add("X-Real-IP", "6.6.6.6")
	req.Header.Add("X-Real-IP", "203.0.113.7")
	assert.Equal(t, "100.64.0.9", s.Echo().IPExtractor(req))
}

func TestRailway_zoneStripped(t *testing.T) {
	s := newRailway(t, "railway", "cloudflare")
	peer := "100.64.0.9:5000"
	assert.Equal(t, "fe80::1", railwayIP(s, peer, map[string]string{"X-Real-IP": "fe80::1%eth0"}))
	assert.Equal(t, "fe80::2", railwayIP(s, peer, map[string]string{
		"X-Real-IP": "2606:4700::1", "CF-Connecting-IP": "fe80::2%eth0"}))
}

func TestRailway_cloudflareFallback(t *testing.T) {
	s := newRailway(t, "railway", "cloudflare")
	peer := "100.64.0.9:5000"
	assert.Equal(t, "1.2.3.4", railwayIP(s, peer, map[string]string{
		"X-Real-IP": "104.16.0.1", "CF-Connecting-IP": "1.2.3.4"}))
	assert.Equal(t, "104.16.0.1", railwayIP(s, peer, map[string]string{
		"X-Real-IP": "104.16.0.1"}), "missing CF-Connecting-IP")
	assert.Equal(t, "104.16.0.1", railwayIP(s, peer, map[string]string{
		"X-Real-IP": "104.16.0.1", "CF-Connecting-IP": "nope"}), "garbage CF-Connecting-IP")
	assert.Equal(t, "203.0.113.7", railwayIP(s, peer, map[string]string{
		"X-Real-IP": "203.0.113.7", "CF-Connecting-IP": "1.2.3.4"}), "non-Cloudflare X-Real-IP: CF header ignored")
}

func TestRailway_noCloudflareKeywordNoFallback(t *testing.T) {
	// A static list does not enable the fallback either.
	for _, s := range []*Server{
		newRailway(t, "railway"),
		newRailway(t, append([]string{"railway"}, CloudflareCIDRs...)...),
	} {
		assert.Equal(t, "104.16.0.1", railwayIP(s, "100.64.0.9:5000", map[string]string{
			"X-Real-IP": "104.16.0.1", "CF-Connecting-IP": "1.2.3.4"}))
	}
}

func TestRailway_nonRailwayPeerGetsNoSpecialTreatment(t *testing.T) {
	s := newRailway(t, "railway", "cloudflare")
	h := map[string]string{"X-Real-IP": "9.9.9.9", "CF-Connecting-IP": "8.8.8.8"}
	assert.Equal(t, "203.0.113.7", railwayIP(s, "203.0.113.7:443", h))
	// Cloudflare peer: normal XFF walk.
	h["X-Forwarded-For"] = "1.2.3.4"
	assert.Equal(t, "1.2.3.4", railwayIP(s, "104.16.0.1:443", h))
	// Just outside 100.64.0.0/10.
	assert.Equal(t, "100.128.0.1", railwayIP(s, "100.128.0.1:443", h))
}

func TestRailway_ipv6(t *testing.T) {
	s := newRailway(t, "railway", "cloudflare")
	peer := "100.64.0.9:5000"
	assert.Equal(t, "2001:db8::1", railwayIP(s, peer, map[string]string{"X-Real-IP": "2001:DB8:0::1"}))
	assert.Equal(t, "2001:db8::2", railwayIP(s, peer, map[string]string{
		"X-Real-IP": "2606:4700::1", "CF-Connecting-IP": "2001:db8::2"}))
}

func TestRailway_keywordParsing(t *testing.T) {
	for _, kw := range []string{"Railway", " railway ", "RAILWAY"} {
		s := newRailway(t, kw, "10.0.0.5/32", "cloudflare")
		assert.True(t, s.railway, kw)
		assert.True(t, s.cloudflare)
		assert.Equal(t, []string{"10.0.0.5/32"}, s.trustedProxies)
		assert.Equal(t, "203.0.113.7", railwayIP(s, "100.64.0.9:1", map[string]string{"X-Real-IP": "203.0.113.7"}))
		// Configured CIDR still works through the normal extractor.
		assert.Equal(t, "1.2.3.4", railwayIP(s, "10.0.0.5:1", map[string]string{"X-Forwarded-For": "1.2.3.4"}))
	}
	s := newRailway(t, "10.0.0.5/32")
	assert.False(t, s.railway)
}

func TestRailway_schemeTrustsRailwayPeer(t *testing.T) {
	s := newRailway(t, "railway")
	scheme := func(peer string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = peer
		req.Header.Set("X-Forwarded-Proto", "https")
		return s.Echo().NewContext(req, nil).Scheme()
	}
	assert.Equal(t, "https", scheme("100.64.0.9:1"))
	assert.Equal(t, "http", scheme("203.0.113.7:1"))
}

func TestRailway_survivesCloudflareRefresh(t *testing.T) {
	fakeCloudflare(t, http.StatusOK, `{"success":true,"result":{"ipv4_cidrs":["198.51.100.0/24"],"ipv6_cidrs":["2001:db8::/32"]}}`)
	s := newRailway(t, "railway", "cloudflare")
	s.updateCloudflare(context.Background())

	peer := "100.64.0.9:5000"
	assert.Equal(t, "1.2.3.4", railwayIP(s, peer, map[string]string{
		"X-Real-IP": "198.51.100.9", "CF-Connecting-IP": "1.2.3.4"}), "refreshed range triggers fallback")
	assert.Equal(t, "104.16.0.1", railwayIP(s, peer, map[string]string{
		"X-Real-IP": "104.16.0.1", "CF-Connecting-IP": "1.2.3.4"}), "pinned range replaced")
	assert.Equal(t, "203.0.113.7", railwayIP(s, peer, map[string]string{"X-Real-IP": "203.0.113.7"}))

	scheme := func(peer string) string {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = peer
		req.Header.Set("X-Forwarded-Proto", "https")
		return s.Echo().NewContext(req, nil).Scheme()
	}
	assert.Equal(t, "https", scheme("100.64.0.9:1"), "Railway peer still trusted for scheme")
	assert.Equal(t, "http", scheme("203.0.113.7:1"), "other peer not trusted")
}

func TestRailway_peerForms(t *testing.T) {
	s := newRailway(t, "railway")
	h := map[string]string{"X-Real-IP": "203.0.113.7"}
	assert.Equal(t, "203.0.113.7", railwayIP(s, "[::ffff:100.64.0.9]:1234", h), "IPv4-mapped Railway peer")
	for _, peer := range []string{"", "garbage"} {
		assert.NotEqual(t, "203.0.113.7", railwayIP(s, peer, h), "peer %q: X-Real-IP not believed", peer)
	}
}
