package logs

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/inspect"
	"github.com/no-inspi/atlas-k8s/internal/model"
)

type fakeBackend struct {
	body    func() io.ReadCloser
	err     error
	gotOpts inspect.LogOptions
}

func (f *fakeBackend) Owners(context.Context, access.User, string, string) ([]inspect.Ref, error) {
	return nil, nil
}
func (f *fakeBackend) YAML(context.Context, access.User, inspect.Ref) (inspect.Doc, error) {
	return inspect.Doc{}, nil
}
func (f *fakeBackend) Events(context.Context, access.User, string, string, string) ([]model.Event, error) {
	return nil, nil
}
func (f *fakeBackend) Logs(_ context.Context, _ access.User, _, _ string, o inspect.LogOptions) (io.ReadCloser, error) {
	f.gotOpts = o
	if f.err != nil {
		return nil, f.err
	}
	return f.body(), nil
}

func serve(t *testing.T, b inspect.Backend, opts Options, expired func() bool) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Handle("/api/namespaces/{ns}/pods/{pod}/logs", Handler(b, func(*http.Request) (access.User, func() bool) {
		return access.User{Name: "bob"}, expired
	}, opts, slog.New(slog.NewTextHandler(io.Discard, nil))))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

type msg struct {
	Type    string `json:"type"`
	Lines   []Line `json:"lines"`
	Count   int    `json:"count"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func collect(t *testing.T, srv *httptest.Server, query string) ([]msg, websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/namespaces/prod/pods/api/logs"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	var out []msg
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			return out, websocket.CloseStatus(err)
		}
		var m msg
		_ = json.Unmarshal(b, &m)
		out = append(out, m)
	}
}

func body(s string) func() io.ReadCloser {
	return func() io.ReadCloser { return io.NopCloser(strings.NewReader(s)) }
}

func TestLinesThenEnd(t *testing.T) {
	b := &fakeBackend{body: body("2026-10-06T09:00:00.123456789Z first line\n2026-10-06T09:00:01Z second\nno timestamp here\n")}
	msgs, code := collect(t, serve(t, b, Options{}, nil), "?container=api&previous=true&tailLines=50")
	var lines []Line
	for _, m := range msgs {
		lines = append(lines, m.Lines...)
	}
	if len(lines) != 3 || lines[0].TS != "2026-10-06T09:00:00.123456789Z" || lines[0].Text != "first line" || lines[2].TS != "" || lines[2].Text != "no timestamp here" {
		t.Errorf("lignes = %+v", lines)
	}
	if last := msgs[len(msgs)-1]; last.Type != "end" || code != websocket.StatusNormalClosure {
		t.Errorf("fin = %+v / %d", last, code)
	}
	if o := b.gotOpts; o.Container != "api" || !o.Previous || o.Follow || o.TailLines != 50 {
		t.Errorf("options = %+v (instance précédente : pas de suivi)", o)
	}
}

func TestTailLinesBounds(t *testing.T) {
	b := &fakeBackend{body: body("")}
	srv := serve(t, b, Options{}, nil)
	collect(t, srv, "")
	if b.gotOpts.TailLines != 500 || !b.gotOpts.Follow {
		t.Errorf("défauts = %+v", b.gotOpts)
	}
	collect(t, srv, "?tailLines=999999")
	if b.gotOpts.TailLines != 5000 {
		t.Errorf("tailLines doit être borné à 5000 : %d", b.gotOpts.TailLines)
	}
}

func TestAPIErrorIsReportedAsIs(t *testing.T) {
	b := &fakeBackend{err: apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "api",
		errors.New(`User "bob" cannot get resource "pods/log" in the namespace "kube-system"`))}
	msgs, _ := collect(t, serve(t, b, Options{}, nil), "")
	if len(msgs) != 1 || msgs[0].Type != "error" || msgs[0].Status != 403 || !strings.Contains(msgs[0].Message, `cannot get resource "pods/log"`) {
		t.Errorf("messages = %+v", msgs)
	}
}

// slowReader produit beaucoup de lignes d'un coup : le tampon de 10 lignes déborde.
func TestOverflowIsCountedAndReported(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5000; i++ {
		sb.WriteString("2026-10-06T09:00:00Z line\n")
	}
	b := &fakeBackend{body: body(sb.String())}
	msgs, _ := collect(t, serve(t, b, Options{BufferLines: 10, FlushInterval: 50 * time.Millisecond}, nil), "")
	received, dropped := 0, 0
	for _, m := range msgs {
		received += len(m.Lines)
		dropped += m.Count
	}
	if dropped == 0 || received+dropped != 5000 {
		t.Errorf("reçues %d + ignorées %d, attendu 5000 au total avec des lignes ignorées", received, dropped)
	}
}

func TestLongLinesAreTruncated(t *testing.T) {
	b := &fakeBackend{body: body("2026-10-06T09:00:00Z " + strings.Repeat("x", 100_000) + "\nnext\n")}
	msgs, _ := collect(t, serve(t, b, Options{}, nil), "")
	var lines []Line
	for _, m := range msgs {
		lines = append(lines, m.Lines...)
	}
	if len(lines) != 2 || len(lines[0].Text) > maxLineBytes+10 || !strings.HasSuffix(lines[0].Text, "…") {
		t.Errorf("ligne longue : %d lignes, %d octets", len(lines), len(lines[0].Text))
	}
}

func TestExpiredSessionCloses(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	b := &fakeBackend{body: func() io.ReadCloser { return pr }}
	old := sessionCheck
	sessionCheck = 20 * time.Millisecond
	defer func() { sessionCheck = old }()
	_, code := collect(t, serve(t, b, Options{}, func() bool { return true }), "")
	if code != 4401 {
		t.Errorf("fermeture %d, attendu 4401", code)
	}
}
