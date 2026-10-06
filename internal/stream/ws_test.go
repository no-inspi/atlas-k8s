package stream

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

func dial(t *testing.T, srv *httptest.Server, query string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	t.Cleanup(cancel)
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/stream" + query
	return websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: hdr})
}

func read(t *testing.T, c *websocket.Conn) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("lecture : %v", err)
	}
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("JSON : %v", err)
	}
	return m
}

func newServer(h *Hub) *httptest.Server {
	mux := http.NewServeMux()
	mux.Handle("/api/stream", Handler(h, slog.New(slog.NewTextHandler(io.Discard, nil)), Unfiltered))
	return httptest.NewServer(mux)
}

func TestStreamSendsSnapshotThenDeltas(t *testing.T) {
	h := newTestHub(16)
	h.Upsert(KindPod, "a", pod("a", "Running"))
	h.Flush()
	srv := newServer(h)
	defer srv.Close()

	c, _, err := dial(t, srv, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	snap := read(t, c)
	if snap.Type != "snapshot" || len(snap.Pods) != 1 || snap.Rev != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}

	h.Upsert(KindPod, "b", pod("b", "Pending"))
	h.Flush()
	up := read(t, c)
	if up.Type != "upsert" || up.Kind != KindPod || up.Rev != 2 {
		t.Fatalf("delta = %+v", up)
	}
}

func TestStreamResumesFromRev(t *testing.T) {
	h := newTestHub(16)
	h.Upsert(KindPod, "a", pod("a", "Running"))
	h.Flush()
	last := h.Rev()
	h.Upsert(KindPod, "b", pod("b", "Running"))
	h.Flush()
	srv := newServer(h)
	defer srv.Close()

	c, _, err := dial(t, srv, "?rev="+strconv.FormatUint(last, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	m := read(t, c)
	if m.Type != "upsert" || m.Rev != last+1 {
		t.Fatalf("reprise attendue au rev %d, reçu %+v", last+1, m)
	}
}

func TestStreamRejectsForeignOrigin(t *testing.T) {
	srv := newServer(newTestHub(16))
	defer srv.Close()
	_, resp, err := dial(t, srv, "", http.Header{"Origin": []string{"https://evil.example"}})
	if err == nil {
		t.Fatal("connexion acceptée depuis une origine étrangère")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("attendu 403, reçu %v", resp)
	}
}

// denyNS refuse un namespace et les nodes ; ses décisions peuvent « changer ».
type denyNS struct {
	ns      string
	changed atomic.Bool
}

func (d *denyNS) Allow(_ context.Context, _ Kind, obj any) bool {
	switch o := obj.(type) {
	case model.Pod:
		return o.Namespace != d.ns
	case model.Node:
		return false
	}
	return true
}

func (d *denyNS) Changed(context.Context) bool { return d.changed.Load() }

func filteredServer(h *Hub, c Client) *httptest.Server {
	mux := http.NewServeMux()
	mux.Handle("/api/stream", Handler(h, slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(*http.Request) (Client, error) { return c, nil }))
	return httptest.NewServer(mux)
}

func TestStreamIsFilteredPerClient(t *testing.T) {
	h := newTestHub(16)
	h.Upsert(KindPod, "a", model.Pod{UID: "a", Namespace: "prod"})
	h.Upsert(KindPod, "s", model.Pod{UID: "s", Namespace: "kube-system"})
	h.Upsert(KindNode, "n1", model.Node{Name: "n1"})
	h.Flush()
	srv := filteredServer(h, Client{Filter: &denyNS{ns: "kube-system"}})
	defer srv.Close()
	c, _, err := dial(t, srv, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()

	snap := read(t, c)
	if len(snap.Pods) != 1 || snap.Pods[0].UID != "a" || len(snap.Nodes) != 0 {
		t.Fatalf("snapshot filtré = %+v", snap)
	}
	h.Upsert(KindPod, "s2", model.Pod{UID: "s2", Namespace: "kube-system"})
	h.Upsert(KindPod, "b", model.Pod{UID: "b", Namespace: "prod"})
	h.Flush()
	if m := read(t, c); m.Type != "upsert" || m.Obj.(map[string]any)["uid"] != "b" {
		t.Fatalf("seul le pod autorisé doit arriver : %+v", m)
	}
	h.SetMetrics(model.Metrics{Pods: map[string]model.Usage{"a": {CPU: 1}, "s": {CPU: 2}, "b": {CPU: 3}},
		Nodes: map[string]model.Usage{"n1": {CPU: 9}}})
	m := read(t, c)
	if m.Type != "metrics" || len(m.Metrics.Pods) != 2 || m.Metrics.Pods["s"] != (model.Usage{}) || len(m.Metrics.Nodes) != 0 {
		t.Fatalf("métriques filtrées = %+v", m.Metrics)
	}
}

func TestStreamClosesWhenRightsChangeOrSessionExpires(t *testing.T) {
	for _, c := range []struct {
		name   string
		client func() Client
		code   websocket.StatusCode
	}{
		{"droits modifiés", func() Client { d := &denyNS{}; d.changed.Store(true); return Client{Filter: d} }, StatusResync},
		{"session expirée", func() Client { return Client{Filter: AllowAll{}, Expired: func() bool { return true }} }, StatusSessionExpired},
	} {
		h := newTestHub(16)
		old := recheckInterval
		recheckInterval = 20 * time.Millisecond
		srv := filteredServer(h, c.client())
		conn, _, err := dial(t, srv, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var closeErr error
		for closeErr == nil {
			_, _, closeErr = conn.Read(ctx)
		}
		cancel()
		if got := websocket.CloseStatus(closeErr); got != c.code {
			t.Errorf("%s : fermeture %d, attendu %d (%v)", c.name, got, c.code, closeErr)
		}
		srv.Close()
		recheckInterval = old
	}
}
