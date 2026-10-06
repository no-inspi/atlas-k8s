package exec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/audit"
)

// echoBackend : renvoie stdin en majuscules, note les tailles, s'arrête sur « exit ».
type echoBackend struct {
	mu       sync.Mutex
	commands [][]string
	sizes    []Size
	missing  map[string]bool // shells absents de l'image
	exitCode int
}

func (b *echoBackend) Exec(ctx context.Context, _ access.User, _, _, _ string, cmd []string, s Streams) error {
	b.mu.Lock()
	b.commands = append(b.commands, cmd)
	b.mu.Unlock()
	if b.missing[cmd[0]] {
		return errors.New(`exec: "` + cmd[0] + `": stat ` + cmd[0] + `: no such file or directory`)
	}
	go func() {
		for sz := range s.Resize {
			b.mu.Lock()
			b.sizes = append(b.sizes, sz)
			b.mu.Unlock()
		}
	}()
	buf := make([]byte, 1024)
	for {
		n, err := s.Stdin.Read(buf)
		if err != nil {
			return ctx.Err() // comme remotecommand : « context canceled » si le navigateur part
		}
		in := string(buf[:n])
		if strings.TrimSpace(in) == "exit" {
			if b.exitCode != 0 {
				return &ExitError{Code: b.exitCode}
			}
			return nil
		}
		_, _ = s.Stdout.Write([]byte(strings.ToUpper(in)))
	}
}

func serve(t *testing.T, b Backend, opts Options, auditBuf io.Writer) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Handle("/api/namespaces/{ns}/pods/{pod}/exec", Handler(b, func(*http.Request) (access.User, func() bool) {
		return access.User{Name: "bob", Groups: []string{"oidc:dev"}}, nil
	}, opts, audit.New(auditBuf), slog.New(slog.NewTextHandler(io.Discard, nil))))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func dial(t *testing.T, srv *httptest.Server, ns, query string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/namespaces/"+ns+"/pods/api/exec"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

type ctrlMsg struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// readUntil lit jusqu'à un message de contrôle ; renvoie la sortie binaire cumulée.
func readUntil(t *testing.T, c *websocket.Conn) (string, ctrlMsg) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out strings.Builder
	for {
		typ, b, err := c.Read(ctx)
		if err != nil {
			return out.String(), ctrlMsg{Type: "closed", Message: err.Error()}
		}
		if typ == websocket.MessageBinary {
			out.Write(b)
			continue
		}
		var m ctrlMsg
		_ = json.Unmarshal(b, &m)
		if m.Type != "info" {
			return out.String(), m
		}
	}
}

var opts = Options{Enabled: true, DeniedNamespaces: []string{"kube-system"}, IdleTimeout: time.Minute}

func TestSessionRelaysStdinStdoutAndResize(t *testing.T) {
	b := &echoBackend{}
	var auditBuf strings.Builder
	c := dial(t, serve(t, b, opts, &auditBuf), "prod", "?container=api")
	ctx := context.Background()
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":120,"rows":40}`))
	_ = c.Write(ctx, websocket.MessageBinary, []byte("ls -la\r"))
	time.Sleep(100 * time.Millisecond)
	_ = c.Write(ctx, websocket.MessageBinary, []byte("exit"))
	out, ctl := readUntil(t, c)
	if out != "LS -LA\r" || ctl.Type != "exit" || ctl.Code != 0 {
		t.Errorf("sortie %q, contrôle %+v", out, ctl)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.sizes) == 0 || b.sizes[0] != (Size{Cols: 120, Rows: 40}) {
		t.Errorf("redimensionnement = %+v", b.sizes)
	}
	if b.commands[0][0] != "/bin/bash" {
		t.Errorf("bash doit être essayé en premier : %v", b.commands)
	}
	if !strings.Contains(auditBuf.String(), `"verb":"exec-open","resource":"pods/exec","namespace":"prod","name":"api","result":"requested"`) ||
		!strings.Contains(auditBuf.String(), `"verb":"exec-close"`) {
		t.Errorf("audit = %s", auditBuf.String())
	}
	if strings.Contains(auditBuf.String(), "ls -la") {
		t.Error("les commandes tapées ne doivent pas être auditées")
	}
}

// Une tentative échouée laisse un lecteur de stdin bloqué (comme remotecommand) :
// il ne doit pas avaler la première frappe destinée au shell suivant.
type greedyBackend struct{ echoBackend }

func (g *greedyBackend) Exec(ctx context.Context, u access.User, ns, pod, c string, cmd []string, s Streams) error {
	if cmd[0] == "/bin/bash" {
		go func() { buf := make([]byte, 64); _, _ = s.Stdin.Read(buf) }()
		go func() { <-s.Resize }() // comme remotecommand : lit la première taille
		time.Sleep(20 * time.Millisecond)
		return errors.New(`exec: "/bin/bash": stat /bin/bash: no such file or directory`)
	}
	return g.echoBackend.Exec(ctx, u, ns, pod, c, cmd, s)
}

func TestFailedAttemptDoesNotEatKeystrokesOrSize(t *testing.T) {
	g := &greedyBackend{}
	c := dial(t, serve(t, g, opts, io.Discard), "prod", "")
	_ = c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"resize","cols":90,"rows":30}`))
	time.Sleep(50 * time.Millisecond)
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("e"))
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("cho\r"))
	time.Sleep(50 * time.Millisecond)
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("exit"))
	if out, _ := readUntil(t, c); !strings.HasPrefix(out, "ECHO") {
		t.Errorf("première frappe perdue : %q", out)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.sizes) == 0 || g.sizes[0] != (Size{Cols: 90, Rows: 30}) {
		t.Errorf("la taille initiale doit parvenir au shell retenu : %+v", g.sizes)
	}
}

func TestShellFallbackAndDistroless(t *testing.T) {
	b := &echoBackend{missing: map[string]bool{"/bin/bash": true}}
	c := dial(t, serve(t, b, opts, io.Discard), "prod", "")
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("exit"))
	if _, ctl := readUntil(t, c); ctl.Type != "exit" {
		t.Errorf("repli sur /bin/sh attendu : %+v", ctl)
	}
	b.mu.Lock()
	if len(b.commands) != 2 || b.commands[1][0] != "/bin/sh" {
		t.Errorf("commandes = %v", b.commands)
	}
	b.mu.Unlock()

	none := &echoBackend{missing: map[string]bool{"/bin/bash": true, "/bin/sh": true}}
	_, ctl := readUntil(t, dial(t, serve(t, none, opts, io.Discard), "prod", ""))
	if ctl.Type != "error" || !strings.Contains(ctl.Message, "distroless") {
		t.Errorf("image sans shell : %+v", ctl)
	}
}

func TestExitCodeAndGuards(t *testing.T) {
	b := &echoBackend{exitCode: 3}
	c := dial(t, serve(t, b, opts, io.Discard), "prod", "")
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("exit"))
	if _, ctl := readUntil(t, c); ctl.Type != "exit" || ctl.Code != 3 {
		t.Errorf("code de sortie : %+v", ctl)
	}

	_, ctl := readUntil(t, dial(t, serve(t, &echoBackend{}, opts, io.Discard), "kube-system", ""))
	if ctl.Type != "error" || !strings.Contains(ctl.Message, "kube-system") {
		t.Errorf("namespace interdit : %+v", ctl)
	}
	disabled := opts
	disabled.Enabled = false
	_, ctl = readUntil(t, dial(t, serve(t, &echoBackend{}, disabled, io.Discard), "prod", ""))
	if ctl.Type != "error" || !strings.Contains(ctl.Message, "désactivé") {
		t.Errorf("terminal désactivé : %+v", ctl)
	}
	_, ctl = readUntil(t, dial(t, serve(t, &echoBackend{}, opts, io.Discard), "prod", "?command=/usr/bin/python3"))
	if ctl.Type != "error" {
		t.Errorf("seuls /bin/bash et /bin/sh sont acceptés : %+v", ctl)
	}
}

func TestIdleTimeout(t *testing.T) {
	short := opts
	short.IdleTimeout = 150 * time.Millisecond
	old := idleCheck
	idleCheck = 20 * time.Millisecond
	defer func() { idleCheck = old }()
	c := dial(t, serve(t, &echoBackend{}, short, io.Discard), "prod", "")
	out, ctl := readUntil(t, c)
	if !strings.Contains(out, "inactivité") || ctl.Type != "exit" {
		t.Errorf("sortie %q, contrôle %+v", out, ctl)
	}
}

// Fermer l'onglet du terminal est une fin normale de session, pas un échec.
func TestBrowserDisconnectIsANormalClose(t *testing.T) {
	var auditBuf syncBuffer
	c := dial(t, serve(t, &echoBackend{}, opts, &auditBuf), "prod", "")
	time.Sleep(50 * time.Millisecond)
	c.Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(auditBuf.String(), "exec-close") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if out := auditBuf.String(); !strings.Contains(out, `"verb":"exec-close"`) || strings.Contains(out, `"result":"failure"`) {
		t.Errorf("audit = %s", out)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }
