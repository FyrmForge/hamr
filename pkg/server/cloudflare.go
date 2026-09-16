package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// cloudflareKeyword in WithTrustedProxies expands to Cloudflare's edge ranges
// and turns on the background refresh.
const cloudflareKeyword = "cloudflare"

// CloudflareCIDRs is Cloudflare's published edge IP list (IPv4 and IPv6),
// pinned at release. Pass it to WithTrustedProxies for a static list that is
// never refreshed, or use the "cloudflare" keyword to start from this list and
// refresh it every 24h.
//
// Fetched 2026-09-16 from https://api.cloudflare.com/client/v4/ips.
var CloudflareCIDRs = []string{
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}

const (
	cloudflareRefreshInterval = 24 * time.Hour
	cloudflareFetchTimeout    = 15 * time.Second
	// Sanity bounds on fetched ranges: anything wider is treated as a bad
	// response, since trusting it would let most of the internet spoof XFF.
	cloudflareMinV4Prefix = 8
	cloudflareMinV6Prefix = 16
)

// cloudflareIPsURL is a var so tests can point it at a fake server.
var cloudflareIPsURL = "https://api.cloudflare.com/client/v4/ips"

// refreshCloudflare fetches Cloudflare's list immediately, then every 24h,
// until ctx is cancelled. A failed or implausible fetch keeps the current list.
func (s *Server) refreshCloudflare(ctx context.Context) {
	t := time.NewTicker(cloudflareRefreshInterval)
	defer t.Stop()
	for {
		s.updateCloudflare(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// updateCloudflare performs one fetch and, on success, swaps in an extractor
// trusting the configured CIDRs plus the fetched Cloudflare ranges.
func (s *Server) updateCloudflare(ctx context.Context) {
	cidrs, err := fetchCloudflareCIDRs(ctx)
	if err != nil {
		slog.Default().Warn("server: cloudflare IP refresh failed, keeping current list", "error", err)
		return
	}
	ext, err := buildIPExtractor(append(append([]string{}, s.trustedProxies...), cidrs...))
	if err != nil {
		// Unreachable: fetched CIDRs are validated and static ones passed New.
		slog.Default().Warn("server: cloudflare IP refresh failed, keeping current list", "error", err)
		return
	}
	s.ipExtractor.Store(&ext)
}

// fetchCloudflareCIDRs downloads and validates Cloudflare's IP list.
func fetchCloudflareCIDRs(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, cloudflareFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cloudflareIPsURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Success bool `json:"success"`
		Result  struct {
			IPv4 []string `json:"ipv4_cidrs"`
			IPv6 []string `json:"ipv6_cidrs"`
		} `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if !body.Success {
		return nil, fmt.Errorf("response reported success=false")
	}
	if len(body.Result.IPv4) == 0 || len(body.Result.IPv6) == 0 {
		return nil, fmt.Errorf("empty ipv4 or ipv6 list")
	}
	if err := validateCIDRs(body.Result.IPv4, false); err != nil {
		return nil, err
	}
	if err := validateCIDRs(body.Result.IPv6, true); err != nil {
		return nil, err
	}
	return append(body.Result.IPv4, body.Result.IPv6...), nil
}

func validateCIDRs(cidrs []string, v6 bool) error {
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			return fmt.Errorf("invalid CIDR %q: %w", c, err)
		}
		ones, bits := n.Mask.Size()
		switch {
		case !v6 && (bits != net.IPv4len*8 || ones < cloudflareMinV4Prefix),
			v6 && (bits != net.IPv6len*8 || ones < cloudflareMinV6Prefix):
			return fmt.Errorf("implausible CIDR %q", c)
		}
	}
	return nil
}
