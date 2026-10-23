// Package traefik_real_ip46 is a Traefik middleware plugin that converts
// NAT64-mapped IPv6 addresses found in request headers (and optionally the
// request's RemoteAddr) to their embedded IPv4 addresses. For /96 prefixes
// (e.g. 64:ff9b::/96) the IPv4 address lives in the last 32 bits of the IPv6
// address per RFC 6052. IPv4-mapped addresses (::ffff:a.b.c.d) are always
// converted to plain IPv4. NAT64 addresses that embed an unspecified,
// loopback, link-local, multicast or broadcast IPv4 address are left alone.
package traefik_real_ip46

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"unicode"
)

// Config holds the plugin configuration.
type Config struct {
	// Headers is the list of request headers to inspect and rewrite.
	Headers []string `json:"headers,omitempty"`
	// Prefixes is the list of /96 IPv6 CIDR prefixes to match against.
	Prefixes []string `json:"prefixes,omitempty"`
	// RewriteRemoteAddr also converts the request's RemoteAddr, so that
	// anything derived from it later (such as the X-Forwarded-For entry the
	// proxy appends) carries the IPv4 address.
	RewriteRemoteAddr bool `json:"rewriteRemoteAddr,omitempty"`
	// Debug enables per-request logging explaining why each address was or
	// was not converted.
	Debug bool `json:"debug,omitempty"`
}

// CreateConfig returns the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		Headers:           []string{"X-Real-IP", "X-Forwarded-For"},
		Prefixes:          []string{"64:ff9b::/96"},
		RewriteRemoteAddr: true,
	}
}

// RealIP46 is the Traefik middleware plugin.
type RealIP46 struct {
	next              http.Handler
	name              string
	headers           []string
	prefixes          []*net.IPNet
	rewriteRemoteAddr bool
	debug             bool
}

// New creates a new RealIP46 middleware instance.
func New(_ context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	if len(config.Headers) == 0 {
		return nil, fmt.Errorf("traefik-real-ip46: at least one header must be configured")
	}
	if len(config.Prefixes) == 0 {
		return nil, fmt.Errorf("traefik-real-ip46: at least one prefix must be configured")
	}

	// Traefik also accepts a list written as one comma-separated string,
	// which leaves spaces around the entries, so trim them.
	headers := make([]string, 0, len(config.Headers))
	for _, h := range config.Headers {
		h = strings.TrimSpace(h)
		if h == "" {
			return nil, fmt.Errorf("traefik-real-ip46: header names must not be empty")
		}
		headers = append(headers, h)
	}

	prefixes := make([]*net.IPNet, 0, len(config.Prefixes))
	for _, p := range config.Prefixes {
		p = strings.TrimSpace(p)
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
		next:              next,
		name:              name,
		headers:           headers,
		prefixes:          prefixes,
		rewriteRemoteAddr: config.RewriteRemoteAddr,
		debug:             config.Debug,
	}, nil
}

func (r *RealIP46) debugf(format string, args ...interface{}) {
	os.Stdout.WriteString("traefik-real-ip46[" + r.name + "]: " + fmt.Sprintf(format, args...) + "\n")
}

// convert returns the IPv4 form of addr when it is a NAT64 address under a
// configured prefix or an IPv4-mapped address. addr may be a bare IP, a
// bracketed IPv6 address, or either form with a port; the port is kept.
// An IPv6 zone (e.g. %eth0) is dropped. The last return value explains the
// decision for debug logging.
func (r *RealIP46) convert(addr string) (string, bool, string) {
	host, port, hasPort := addr, "", false
	if h, p, err := net.SplitHostPort(addr); err == nil {
		host, port, hasPort = h, p, true
	} else if strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]") {
		host = addr[1 : len(addr)-1]
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return addr, false, "is not a valid IP, skipping"
	}

	var v4 net.IP
	var why string
	if v4 = ip.To4(); v4 != nil {
		if !strings.Contains(host, ":") {
			return addr, false, "is already IPv4, skipping"
		}
		why = "is IPv4-mapped"
	} else {
		for _, prefix := range r.prefixes {
			if prefix.Contains(ip) {
				// For a /96 prefix the IPv4 address is embedded in the
				// last 32 bits (bytes 12–15) of the IPv6 address.
				v4 = ip[12:16]
				why = "matched prefix " + prefix.String()
				break
			}
		}
		if v4 == nil {
			return addr, false, "is IPv6 but did not match any configured prefix, skipping"
		}
		// These can't be a real translated client, and converting them
		// could fool IP-based middlewares later in the chain.
		if v4.IsUnspecified() || v4.IsLoopback() || v4.IsLinkLocalUnicast() ||
			v4.IsMulticast() || v4.Equal(net.IPv4bcast) {
			return addr, false, why + " but embeds non-routable " + v4.String() + ", skipping"
		}
	}

	out := v4.String()
	if hasPort {
		out = net.JoinHostPort(out, port)
	}
	return out, true, why + ", converted to " + out
}

// rewriteList converts every matching address in a comma-separated header
// value, keeping the original separators and surrounding whitespace.
func (r *RealIP46) rewriteList(header, value string) (string, bool) {
	parts := strings.Split(value, ",")
	modified := false

	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		out, changed, why := r.convert(trimmed)
		if r.debug {
			r.debugf("header %q: %q %s", header, trimmed, why)
		}
		if changed {
			leading := part[:len(part)-len(strings.TrimLeftFunc(part, unicode.IsSpace))]
			trailing := part[len(strings.TrimRightFunc(part, unicode.IsSpace)):]
			parts[i] = leading + out + trailing
			modified = true
		}
	}

	return strings.Join(parts, ","), modified
}

// ServeHTTP processes the request, converting any NAT64 IPv6 addresses that
// match a configured prefix to their embedded IPv4 address.
func (r *RealIP46) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if r.rewriteRemoteAddr && req.RemoteAddr != "" {
		out, changed, why := r.convert(req.RemoteAddr)
		if r.debug {
			r.debugf("RemoteAddr %q %s", req.RemoteAddr, why)
		}
		if changed {
			req.RemoteAddr = out
		}
	}

	for _, header := range r.headers {
		values := req.Header.Values(header)
		if len(values) == 0 {
			if r.debug {
				r.debugf("header %q absent, skipping", header)
			}
			continue
		}

		// A header may appear on several lines; handle each one and keep
		// them all.
		newValues := make([]string, len(values))
		modified := false
		for i, v := range values {
			var changed bool
			newValues[i], changed = r.rewriteList(header, v)
			modified = modified || changed
		}

		if modified {
			req.Header[http.CanonicalHeaderKey(header)] = newValues
		}
	}

	r.next.ServeHTTP(rw, req)
}
