package traefik_real_ip46_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	plugin "github.com/olemathias/traefik-real-ip46"
)

func newMiddleware(t *testing.T, cfg *plugin.Config) http.Handler {
	t.Helper()
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {})
	h, err := plugin.New(context.Background(), next, cfg, "test")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return h
}

func request(t *testing.T, h http.Handler, headers map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	return req
}

func TestXRealIP_WellKnownPrefix(t *testing.T) {
	cfg := plugin.CreateConfig()
	h := newMiddleware(t, cfg)

	// 64:ff9b::c0a8:0101 encodes 192.168.1.1
	req := request(t, h, map[string]string{"X-Real-IP": "64:ff9b::c0a8:101"})
	if got := req.Header.Get("X-Real-IP"); got != "192.168.1.1" {
		t.Errorf("X-Real-IP = %q, want %q", got, "192.168.1.1")
	}
}

func TestXForwardedFor_AllMatchingConverted(t *testing.T) {
	cfg := plugin.CreateConfig()
	h := newMiddleware(t, cfg)

	// Two NAT64 addresses and one plain IPv4 — only the NAT64 ones change.
	req := request(t, h, map[string]string{
		"X-Forwarded-For": "64:ff9b::c0a8:101, 64:ff9b::8080:8080, 10.0.0.1",
	})
	want := "192.168.1.1, 128.128.128.128, 10.0.0.1"
	if got := req.Header.Get("X-Forwarded-For"); got != want {
		t.Errorf("X-Forwarded-For = %q, want %q", got, want)
	}
}

func TestXForwardedFor_NonMatchingUnchanged(t *testing.T) {
	cfg := plugin.CreateConfig()
	h := newMiddleware(t, cfg)

	const value = "203.0.113.1, 2001:db8::1"
	req := request(t, h, map[string]string{"X-Forwarded-For": value})
	if got := req.Header.Get("X-Forwarded-For"); got != value {
		t.Errorf("X-Forwarded-For = %q, want unchanged %q", got, value)
	}
}

func TestMultiplePrefixes(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"64:ff9b::/96", "64:ff9b:1::/96"},
	}
	h := newMiddleware(t, cfg)

	// Address in the second configured prefix.
	// 64:ff9b:1::c0a8:0202 encodes 192.168.2.2
	req := request(t, h, map[string]string{"X-Real-IP": "64:ff9b:1::c0a8:202"})
	if got := req.Header.Get("X-Real-IP"); got != "192.168.2.2" {
		t.Errorf("X-Real-IP = %q, want %q", got, "192.168.2.2")
	}
}

func TestConfigurableHeaders(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"CF-Connecting-IP"},
		Prefixes: []string{"64:ff9b::/96"},
	}
	h := newMiddleware(t, cfg)

	req := request(t, h, map[string]string{"CF-Connecting-IP": "64:ff9b::c0a8:101"})
	if got := req.Header.Get("CF-Connecting-IP"); got != "192.168.1.1" {
		t.Errorf("CF-Connecting-IP = %q, want %q", got, "192.168.1.1")
	}
}

func TestAbsentHeaderSkipped(t *testing.T) {
	cfg := plugin.CreateConfig()
	h := newMiddleware(t, cfg)

	req := request(t, h, nil)
	if got := req.Header.Get("X-Real-IP"); got != "" {
		t.Errorf("expected empty X-Real-IP, got %q", got)
	}
}

func TestMultipleHeadersProcessed(t *testing.T) {
	cfg := plugin.CreateConfig()
	h := newMiddleware(t, cfg)

	req := request(t, h, map[string]string{
		"X-Real-IP":       "64:ff9b::c0a8:101",
		"X-Forwarded-For": "64:ff9b::c0a8:101, 10.0.0.2",
	})
	if got := req.Header.Get("X-Real-IP"); got != "192.168.1.1" {
		t.Errorf("X-Real-IP = %q, want %q", got, "192.168.1.1")
	}
	if got := req.Header.Get("X-Forwarded-For"); got != "192.168.1.1, 10.0.0.2" {
		t.Errorf("X-Forwarded-For = %q, want %q", got, "192.168.1.1, 10.0.0.2")
	}
}

func TestNew_NoHeaders(t *testing.T) {
	cfg := &plugin.Config{Prefixes: []string{"64:ff9b::/96"}}
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {})
	if _, err := plugin.New(context.Background(), next, cfg, "test"); err == nil {
		t.Error("expected error for empty headers, got nil")
	}
}

func TestNew_NoPrefixes(t *testing.T) {
	cfg := &plugin.Config{Headers: []string{"X-Real-IP"}}
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {})
	if _, err := plugin.New(context.Background(), next, cfg, "test"); err == nil {
		t.Error("expected error for empty prefixes, got nil")
	}
}

func TestNew_InvalidPrefix(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"not-a-cidr"},
	}
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {})
	if _, err := plugin.New(context.Background(), next, cfg, "test"); err == nil {
		t.Error("expected error for invalid prefix, got nil")
	}
}

func TestNew_Non96Prefix(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"64:ff9b::/32"},
	}
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {})
	if _, err := plugin.New(context.Background(), next, cfg, "test"); err == nil {
		t.Error("expected error for non-/96 prefix, got nil")
	}
}
