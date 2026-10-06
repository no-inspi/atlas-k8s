package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/no-inspi/cluster-atlas/internal/stream"
)

var static = fstest.MapFS{
	"index.html":      {Data: []byte("<!doctype html><title>atlas</title>")},
	"assets/app-1.js": {Data: []byte("console.log(1)")},
	"favicon.svg":     {Data: []byte("<svg/>")},
}

func newTest(fs fstest.MapFS) (http.Handler, *stream.Hub) {
	hub := stream.NewHub(stream.Options{})
	h := New(Config{ClusterName: "kind-atlas", Demo: true, User: "demo", Static: fs}, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h, hub
}

func get(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestHealthAndReadiness(t *testing.T) {
	h, hub := newTest(static)
	if rec := get(h, "/healthz"); rec.Code != 200 {
		t.Errorf("/healthz = %d", rec.Code)
	}
	if rec := get(h, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/readyz avant synchronisation = %d", rec.Code)
	}
	hub.MarkReady()
	if rec := get(h, "/readyz"); rec.Code != 200 {
		t.Errorf("/readyz après synchronisation = %d", rec.Code)
	}
}

func TestMe(t *testing.T) {
	h, _ := newTest(static)
	rec := get(h, "/api/me")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"cluster":"kind-atlas"`) ||
		!strings.Contains(rec.Body.String(), `"demo":true`) || !strings.Contains(rec.Body.String(), `"user":"demo"`) {
		t.Errorf("/api/me = %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h, _ := newTest(static)
	rec := get(h, "/")
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "script-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP sans %q : %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") {
		t.Errorf("CSP trop permissive : %s", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options absent")
	}
}

func TestSPAFallbackAndAssets(t *testing.T) {
	h, _ := newTest(static)
	if rec := get(h, "/pods/production/foo"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>atlas") {
		t.Errorf("route SPA = %d %q", rec.Code, rec.Body)
	}
	rec := get(h, "/assets/app-1.js")
	if rec.Code != 200 || rec.Body.String() != "console.log(1)" {
		t.Errorf("asset = %d %q", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset sans cache immutable : %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get(h, "/assets/absent.js"); rec.Code != 404 {
		t.Errorf("asset absent = %d, attendu 404", rec.Code)
	}
	if rec := get(h, "/api/inconnu"); rec.Code != 404 {
		t.Errorf("API inconnue = %d, attendu 404", rec.Code)
	}
}

func TestPrometheusMetrics(t *testing.T) {
	h, hub := newTest(static)
	if rec := get(h, "/metrics"); rec.Code == 200 && strings.Contains(rec.Body.String(), "go_goroutines") {
		t.Error("/metrics ne doit pas être servi sur le port public")
	}
	rec := get(MetricsHandler(hub), "/metrics")
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("/metrics = %d", rec.Code)
	}
	for _, want := range []string{"atlas_stream_clients 0", "atlas_stream_rev", "atlas_stream_messages_total", "go_goroutines"} {
		if !strings.Contains(body, want) {
			t.Errorf("/metrics sans %q", want)
		}
	}
}

func TestMissingFrontend(t *testing.T) {
	h, _ := newTest(fstest.MapFS{".gitkeep": {Data: nil}})
	rec := get(h, "/")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "make web") {
		t.Errorf("front absent = %d %q", rec.Code, rec.Body)
	}
}
