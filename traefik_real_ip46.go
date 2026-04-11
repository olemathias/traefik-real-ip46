// Package traefik_real_ip46 is a Traefik middleware plugin that converts
// NAT64-mapped IPv6 addresses found in request headers to their embedded
// IPv4 addresses. For /96 prefixes (e.g. 64:ff9b::/96) the IPv4 address
// lives in the last 32 bits of the IPv6 address per RFC 6052.
package traefik_real_ip46

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Config holds the plugin configuration.
type Config struct {
	// Headers is the list of request headers to inspect and rewrite.
	Headers []string `json:"headers,omitempty"`
	// Prefixes is the list of /96 IPv6 CIDR prefixes to match against.
	Prefixes []string `json:"prefixes,omitempty"`
}

// CreateConfig returns the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		Headers:  []string{"X-Real-IP", "X-Forwarded-For"},
		Prefixes: []string{"64:ff9b::/96"},
	}
}

// RealIP46 is the Traefik middleware plugin.
type RealIP46 struct {
	next     http.Handler
	name     string
	headers  []string
	prefixes []*net.IPNet
}

// New creates a new RealIP46 middleware instance.
func New(_ context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if len(config.Headers) == 0 {
		return nil, fmt.Errorf("traefik-real-ip46: at least one header must be configured")
	}
	if len(config.Prefixes) == 0 {
		return nil, fmt.Errorf("traefik-real-ip46: at least one prefix must be configured")
	}

	prefixes := make([]*net.IPNet, 0, len(config.Prefixes))
	for _, p := range config.Prefixes {
		_, ipNet, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("traefik-real-ip46: invalid prefix %q: %w", p, err)
		}
		ones, bits := ipNet.Mask.Size()
		if bits != 128 || ones != 96 {
			return nil, fmt.Errorf("traefik-real-ip46: prefix %q must be a /96 IPv6 prefix", p)
		}
		prefixes = append(prefixes, ipNet)
	}

	return &RealIP46{
		next:     next,
		name:     name,
		headers:  config.Headers,
		prefixes: prefixes,
	}, nil
}

// ServeHTTP processes the request, converting any NAT64 IPv6 addresses that
// match a configured prefix to their embedded IPv4 address.
func (r *RealIP46) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	for _, header := range r.headers {
		value := req.Header.Get(header)
		if value == "" {
			continue
		}

		parts := strings.Split(value, ",")
		modified := false

		for i, part := range parts {
			trimmed := strings.TrimSpace(part)
			ip := net.ParseIP(trimmed)
			if ip == nil {
				continue
			}

			ip16 := ip.To16()
			if ip16 == nil {
				continue
			}

			for _, prefix := range r.prefixes {
				if prefix.Contains(ip16) {
					// For a /96 prefix the IPv4 address is embedded in the
					// last 32 bits (bytes 12–15) of the IPv6 address.
					ipv4 := net.IP(ip16[12:16]).String()

					// Preserve any leading whitespace from the original value
					// so that X-Forwarded-For formatting (", " separators) is
					// kept intact.
					leading := part[:len(part)-len(strings.TrimLeft(part, " \t"))]
					parts[i] = leading + ipv4
					modified = true
					break
				}
			}
		}

		if modified {
			req.Header.Set(header, strings.Join(parts, ","))
		}
	}

	r.next.ServeHTTP(rw, req)
}
