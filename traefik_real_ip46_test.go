package traefik_real_ip46_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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

// newErr returns the error from plugin.New for cfg.
func newErr(cfg *plugin.Config) error {
	next := http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {})
	_, err := plugin.New(context.Background(), next, cfg, "test")
	return err
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

// captureStdout runs f and returns whatever was written to os.Stdout.
// It swaps the global os.Stdout, so tests using it must not run in parallel.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()

	// Read while f runs so large output can't fill the pipe and block.
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, r); err != nil {
			t.Errorf("reading captured stdout: %v", err)
		}
		done <- buf.String()
	}()

	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	f()
	w.Close()
	// Yaegi can't run `return <-done` directly, so receive first.
	out := <-done
	return out
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
	if err := newErr(cfg); err == nil {
		t.Error("expected error for empty headers, got nil")
	}
}

func TestNew_NoPrefixes(t *testing.T) {
	cfg := &plugin.Config{Headers: []string{"X-Real-IP"}}
	if err := newErr(cfg); err == nil {
		t.Error("expected error for empty prefixes, got nil")
	}
}

func TestNew_InvalidPrefix(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"not-a-cidr"},
	}
	if err := newErr(cfg); err == nil {
		t.Error("expected error for invalid prefix, got nil")
	}
}

func TestNew_Non96Prefix(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"64:ff9b::/32"},
	}
	if err := newErr(cfg); err == nil {
		t.Error("expected error for non-/96 prefix, got nil")
	}
}

func TestNew_IPv4Prefix(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"192.0.2.0/24"},
	}
	if err := newErr(cfg); err == nil {
		t.Error("expected error for IPv4 prefix, got nil")
	}
}

func TestCreateConfig_Defaults(t *testing.T) {
	cfg := plugin.CreateConfig()
	if !cfg.RewriteRemoteAddr {
		t.Error("CreateConfig().RewriteRemoteAddr should default to true")
	}
}

func TestXForwardedFor_MultipleHeaderLines(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Add("X-Forwarded-For", "64:ff9b::c000:201, 10.0.0.1")
	req.Header.Add("X-Forwarded-For", "64:ff9b::808:808")
	h.ServeHTTP(httptest.NewRecorder(), req)

	got := req.Header.Values("X-Forwarded-For")
	want := []string{"192.0.2.1, 10.0.0.1", "8.8.8.8"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("X-Forwarded-For = %q, want %q", got, want)
	}
}

func TestIPv4MappedConverted(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	req := request(t, h, map[string]string{
		"X-Real-IP":       "::ffff:192.0.2.1",
		"X-Forwarded-For": "::ffff:c000:202, 10.0.0.1",
	})
	if got := req.Header.Get("X-Real-IP"); got != "192.0.2.1" {
		t.Errorf("X-Real-IP = %q, want %q", got, "192.0.2.1")
	}
	if got := req.Header.Get("X-Forwarded-For"); got != "192.0.2.2, 10.0.0.1" {
		t.Errorf("X-Forwarded-For = %q, want %q", got, "192.0.2.2, 10.0.0.1")
	}
}

func TestInvalidEntriesAndWhitespaceKept(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	const in = "unknown,\t64:ff9b::c000:201,, 10.0.0.1"
	const want = "unknown,\t192.0.2.1,, 10.0.0.1"
	req := request(t, h, map[string]string{"X-Forwarded-For": in})
	if got := req.Header.Get("X-Forwarded-For"); got != want {
		t.Errorf("X-Forwarded-For = %q, want %q", got, want)
	}
}

func TestBracketsAndPorts(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	tests := map[string]string{
		"[64:ff9b::c000:201]:443": "192.0.2.1:443",
		"[64:ff9b::c000:201]":     "192.0.2.1",
		"[::ffff:192.0.2.1]:80":   "192.0.2.1:80",
		"192.0.2.1:80":            "192.0.2.1:80",
		"[2001:db8::1]:443":       "[2001:db8::1]:443",
	}
	for in, want := range tests {
		req := request(t, h, map[string]string{"X-Real-IP": in})
		if got := req.Header.Get("X-Real-IP"); got != want {
			t.Errorf("X-Real-IP %q = %q, want %q", in, got, want)
		}
	}
}

func TestRemoteAddr(t *testing.T) {
	tests := []struct {
		name    string
		rewrite bool
		in      string
		want    string
	}{
		{"nat64", true, "[64:ff9b::c000:201]:12345", "192.0.2.1:12345"},
		{"mapped", true, "[::ffff:192.0.2.1]:12345", "192.0.2.1:12345"},
		{"ipv4", true, "192.0.2.1:12345", "192.0.2.1:12345"},
		{"other ipv6", true, "[2001:db8::1]:12345", "[2001:db8::1]:12345"},
		{"disabled", false, "[64:ff9b::c000:201]:12345", "[64:ff9b::c000:201]:12345"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := plugin.CreateConfig()
			cfg.RewriteRemoteAddr = tt.rewrite
			h := newMiddleware(t, cfg)

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tt.in
			h.ServeHTTP(httptest.NewRecorder(), req)
			if req.RemoteAddr != tt.want {
				t.Errorf("RemoteAddr = %q, want %q", req.RemoteAddr, tt.want)
			}
		})
	}
}

// --- Debug mode tests ---

func TestCreateConfig_DebugDefaultFalse(t *testing.T) {
	if plugin.CreateConfig().Debug {
		t.Error("CreateConfig().Debug should default to false")
	}
}

func TestDebug_NoLog_WhenDisabled(t *testing.T) {
	cfg := plugin.CreateConfig() // Debug: false
	h := newMiddleware(t, cfg)

	out := captureStdout(t, func() {
		request(t, h, map[string]string{"X-Real-IP": "2001:db8::1"})
	})
	if out != "" {
		t.Errorf("expected no log output with debug disabled, got %q", out)
	}
}

func TestDebug_LogsConversion(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"64:ff9b::/96"},
		Debug:    true,
	}
	h := newMiddleware(t, cfg)

	out := captureStdout(t, func() {
		request(t, h, map[string]string{"X-Real-IP": "64:ff9b::c0a8:101"})
	})
	if !bytes.Contains([]byte(out), []byte("converted to 192.168.1.1")) {
		t.Errorf("expected conversion log, got %q", out)
	}
}

func TestDebug_LogsAbsentHeader(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"64:ff9b::/96"},
		Debug:    true,
	}
	h := newMiddleware(t, cfg)

	out := captureStdout(t, func() {
		request(t, h, nil)
	})
	if !bytes.Contains([]byte(out), []byte("absent")) {
		t.Errorf("expected absent-header log, got %q", out)
	}
}

func TestDebug_LogsNoMatchingPrefix(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"64:ff9b::/96"},
		Debug:    true,
	}
	h := newMiddleware(t, cfg)

	out := captureStdout(t, func() {
		request(t, h, map[string]string{"X-Real-IP": "2001:db8::1"})
	})
	if !bytes.Contains([]byte(out), []byte("did not match any configured prefix")) {
		t.Errorf("expected no-match log, got %q", out)
	}
}

func TestDebug_LogsAlreadyIPv4(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"64:ff9b::/96"},
		Debug:    true,
	}
	h := newMiddleware(t, cfg)

	out := captureStdout(t, func() {
		request(t, h, map[string]string{"X-Real-IP": "192.168.1.1"})
	})
	if !bytes.Contains([]byte(out), []byte("already IPv4")) {
		t.Errorf("expected already-IPv4 log, got %q", out)
	}
}

func TestDebug_LogsIPv4MappedWithName(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP"},
		Prefixes: []string{"64:ff9b::/96"},
		Debug:    true,
	}
	h := newMiddleware(t, cfg)

	out := captureStdout(t, func() {
		request(t, h, map[string]string{"X-Real-IP": "::ffff:192.0.2.1"})
	})
	if !bytes.Contains([]byte(out), []byte("traefik-real-ip46[test]")) {
		t.Errorf("expected middleware name in log, got %q", out)
	}
	if !bytes.Contains([]byte(out), []byte("IPv4-mapped, converted to 192.0.2.1")) {
		t.Errorf("expected IPv4-mapped log, got %q", out)
	}
}

func TestDottedNAT64Form(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	req := request(t, h, map[string]string{"X-Real-IP": "64:ff9b::192.0.2.1"})
	if got := req.Header.Get("X-Real-IP"); got != "192.0.2.1" {
		t.Errorf("X-Real-IP = %q, want %q", got, "192.0.2.1")
	}
}

func TestNextHandlerCalled(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		called = true
		if got := req.Header.Get("X-Real-IP"); got != "192.0.2.1" {
			t.Errorf("next saw X-Real-IP = %q, want %q", got, "192.0.2.1")
		}
	})
	h, err := plugin.New(context.Background(), next, plugin.CreateConfig(), "test")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Real-IP", "64:ff9b::c000:201")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !called {
		t.Error("next handler was not called")
	}
}

func TestEmptyValuesUnchanged(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header["X-Forwarded-For"] = []string{""}
	req.RemoteAddr = ""
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got := req.Header.Values("X-Forwarded-For"); len(got) != 1 || got[0] != "" {
		t.Errorf("X-Forwarded-For = %q, want [\"\"]", got)
	}
	if req.RemoteAddr != "" {
		t.Errorf("RemoteAddr = %q, want empty", req.RemoteAddr)
	}
}

func TestZoneStripped(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	tests := map[string]string{
		"64:ff9b::c000:201%eth0":       "192.0.2.1",
		"[64:ff9b::c000:201%eth0]:443": "192.0.2.1:443",
		"[2001:db8::1%eth0]:443":       "[2001:db8::1%eth0]:443",
	}
	for in, want := range tests {
		req := request(t, h, map[string]string{"X-Real-IP": in})
		if got := req.Header.Get("X-Real-IP"); got != want {
			t.Errorf("X-Real-IP %q = %q, want %q", in, got, want)
		}
	}
}

func TestEmptyPortKeepsColon(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	req := request(t, h, map[string]string{"X-Real-IP": "[64:ff9b::c000:201]:"})
	if got := req.Header.Get("X-Real-IP"); got != "192.0.2.1:" {
		t.Errorf("X-Real-IP = %q, want %q", got, "192.0.2.1:")
	}
}

func TestSurroundingWhitespaceKept(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	const in = "64:ff9b::c000:201 , 64:ff9b::c000:202 , 10.0.0.1"
	const want = "192.0.2.1 , 192.0.2.2 , 10.0.0.1"
	req := request(t, h, map[string]string{"X-Forwarded-For": in})
	if got := req.Header.Get("X-Forwarded-For"); got != want {
		t.Errorf("X-Forwarded-For = %q, want %q", got, want)
	}
}

func TestNonRoutableEmbeddedIPv4Skipped(t *testing.T) {
	h := newMiddleware(t, plugin.CreateConfig())

	for _, in := range []string{
		"64:ff9b::",          // 0.0.0.0
		"64:ff9b::7f00:1",    // 127.0.0.1
		"64:ff9b::a9fe:101",  // 169.254.1.1
		"64:ff9b::e000:1",    // 224.0.0.1
		"64:ff9b::ffff:ffff", // 255.255.255.255
	} {
		req := request(t, h, map[string]string{"X-Real-IP": in})
		if got := req.Header.Get("X-Real-IP"); got != in {
			t.Errorf("X-Real-IP %q = %q, want unchanged", in, got)
		}
	}

	// IPv4-mapped loopback really is 127.0.0.1, so it is still converted.
	req := request(t, h, map[string]string{"X-Real-IP": "::ffff:127.0.0.1"})
	if got := req.Header.Get("X-Real-IP"); got != "127.0.0.1" {
		t.Errorf("X-Real-IP = %q, want %q", got, "127.0.0.1")
	}
}

func TestConfigEntriesTrimmed(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{" X-Real-IP "},
		Prefixes: []string{"64:ff9b::/96", " 64:ff9b:1::/96"},
	}
	h := newMiddleware(t, cfg)

	req := request(t, h, map[string]string{"X-Real-IP": "64:ff9b:1::c000:201"})
	if got := req.Header.Get("X-Real-IP"); got != "192.0.2.1" {
		t.Errorf("X-Real-IP = %q, want %q", got, "192.0.2.1")
	}
}

func TestNew_EmptyHeaderName(t *testing.T) {
	cfg := &plugin.Config{
		Headers:  []string{"X-Real-IP", " "},
		Prefixes: []string{"64:ff9b::/96"},
	}
	if err := newErr(cfg); err == nil {
		t.Error("expected error for empty header name, got nil")
	}
}

func TestDebug_LogsRemoteAddr(t *testing.T) {
	cfg := plugin.CreateConfig()
	cfg.Debug = true
	h := newMiddleware(t, cfg)

	out := captureStdout(t, func() {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "[64:ff9b::c000:201]:1234"
		h.ServeHTTP(httptest.NewRecorder(), req)
	})
	if !bytes.Contains([]byte(out), []byte("RemoteAddr \"[64:ff9b::c000:201]:1234\" matched prefix 64:ff9b::/96, converted to 192.0.2.1:1234")) {
		t.Errorf("expected RemoteAddr log, got %q", out)
	}
}
