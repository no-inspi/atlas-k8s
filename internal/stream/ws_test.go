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
	"testing"
	"time"

	"github.com/coder/websocket"
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
	mux.Handle("/api/stream", Handler(h, slog.New(slog.NewTextHandler(io.Discard, nil))))
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
