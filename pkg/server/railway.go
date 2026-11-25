package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/labstack/echo/v4"
)

// railwayKeyword in WithTrustedProxies trusts Railway's proxy and reads the
// client IP from X-Real-IP.
const railwayKeyword = "railway"

// railwayCIDR is the range Railway's internal proxy connects from.
const railwayCIDR = "100.64.0.0/10"

// installExtractors builds the IP and scheme extractors from the configured
// CIDRs plus cfCIDRs (the Cloudflare ranges; only passed when the cloudflare
// keyword is on) and swaps them in. New and the Cloudflare refresh share it.
func (s *Server) installExtractors(cfCIDRs []string) error {
	cidrs := append(append([]string{}, s.trustedProxies...), cfCIDRs...)
	ipExt, err := buildIPExtractor(cidrs)
	if err != nil {
		return err
	}
	schemeCIDRs := cidrs
	if s.railway {
		schemeCIDRs = append(append([]string{}, cidrs...), railwayCIDR)
		var cfNets []netip.Prefix
		if s.cloudflare {
			for _, c := range cfCIDRs {
				p, err := netip.ParsePrefix(c)
				if err != nil {
					return fmt.Errorf("server: invalid cloudflare CIDR %q: %w", c, err)
				}
				cfNets = append(cfNets, p)
			}
		}
		ipExt = railwayIPExtractor(ipExt, cfNets)
	}
	schemeExt, err := buildSchemeExtractor(schemeCIDRs)
	if err != nil {
		return err
	}
	s.ipExtractor.Store(&ipExt)
	s.schemeExtractor.Store(&schemeExt)
	return nil
}

// railwayIPExtractor wraps normal: peers outside railwayCIDR get normal's
// answer; Railway peers get X-Real-IP (set by Railway, client value
// overwritten), or CF-Connecting-IP when X-Real-IP is itself a Cloudflare
// address (cfNets non-empty only with the cloudflare keyword). Several
// X-Real-IP values are not believed: the peer address is returned.
func railwayIPExtractor(normal echo.IPExtractor, cfNets []netip.Prefix) echo.IPExtractor {
	railway := netip.MustParsePrefix(railwayCIDR)
	inAny := func(a netip.Addr, nets []netip.Prefix) bool {
		for _, n := range nets {
			if n.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(r *http.Request) string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		peer, err := netip.ParseAddr(host)
		if err != nil || !railway.Contains(peer.Unmap()) {
			return normal(r)
		}
		peer = peer.Unmap()
		// Several X-Real-IP values: Railway overwrites a client value, but
		// that is behaviour, not a guarantee. Believe none of them.
		if len(r.Header.Values("X-Real-IP")) > 1 {
			return peer.String()
		}
		real, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP")))
		if err != nil {
			return peer.String()
		}
		real = real.Unmap().WithZone("")
		if inAny(real, cfNets) {
			if cf, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
				return cf.Unmap().WithZone("").String()
			}
		}
		return real.String()
	}
}
