package exec

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/no-inspi/cluster-atlas/internal/access"
	"github.com/no-inspi/cluster-atlas/internal/audit"
)

type Options struct {
	Enabled          bool
	DeniedNamespaces []string
	IdleTimeout      time.Duration
}

// SessionFunc : utilisateur de la requête et test d'expiration de sa session.
type SessionFunc func(r *http.Request) (access.User, func() bool)

const (
	writeTimeout         = 10 * time.Second
	StatusSessionExpired = websocket.StatusCode(4401)
)

// idleCheck : fréquence de vérification de l'inactivité et de la session.
var idleCheck = 15 * time.Second

type control struct {
	Type    string `json:"type"` // info | error | exit
	Message string `json:"message,omitempty"`
	Code    int    `json:"code,omitempty"`
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
}

// Handler sert /api/namespaces/{ns}/pods/{pod}/exec?container=&command=.
// Chaque ouverture et fermeture de session est auditée ; les commandes tapées
// ne le sont pas.
func Handler(b Backend, session SessionFunc, opts Options, auditLog *audit.Logger, log *slog.Logger) http.Handler {
	check := idleCheck
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ns, pod := chi.URLParam(r, "ns"), chi.URLParam(r, "pod")
		container, command := r.URL.Query().Get("container"), r.URL.Query().Get("command")
		user, expired := session(r)

		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		// ctx suit la connexion ; execCtx, la session du shell. Annuler un contexte
		// de lecture fermerait le WebSocket : l'inactivité n'annule que execCtx,
		// pour pouvoir encore prévenir le navigateur.
		ctx := r.Context()
		execCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		fail := func(msg string) {
			_ = writeJSON(ctx, c, control{Type: "error", Message: msg})
			c.Close(websocket.StatusNormalClosure, "")
		}
		switch {
		case !opts.Enabled:
			fail("le terminal est désactivé sur cette installation (features.exec.enabled=false)")
			return
		case slices.Contains(opts.DeniedNamespaces, ns):
			fail("le terminal est interdit dans le namespace " + ns + " (features.exec.deniedNamespaces)")
			return
		case command != "" && !slices.Contains(Shells, command):
			fail("commande refusée : seuls /bin/bash et /bin/sh sont acceptés")
			return
		}

		var lastActivity atomic.Int64
		touch := func() { lastActivity.Store(time.Now().UnixNano()) }
		touch()

		// Navigateur → stdin et redimensionnements.
		stdin := make(chan []byte, 64)
		resize := make(chan Size, 1)
		var lastSize atomic.Pointer[Size]
		// browserGone : le navigateur a fermé la connexion (onglet fermé, autre pod).
		var browserGone atomic.Bool
		go func() {
			defer close(resize)
			for {
				typ, data, err := c.Read(ctx)
				if err != nil {
					browserGone.Store(true)
					cancel()
					return
				}
				touch()
				if typ == websocket.MessageBinary {
					select {
					case stdin <- data:
					case <-execCtx.Done():
						return
					}
					continue
				}
				var m control
				if json.Unmarshal(data, &m) == nil && m.Type == "resize" && m.Cols > 0 && m.Rows > 0 {
					sz := Size{Cols: m.Cols, Rows: m.Rows}
					lastSize.Store(&sz)
					select { // seule la dernière taille compte
					case <-resize:
					default:
					}
					resize <- sz
				}
			}
		}()

		out := &wsWriter{ctx: ctx, c: c, touch: touch}
		// Raison d'une fermeture décidée par le serveur (session expirée, inactivité).
		var closing atomic.Value
		closing.Store("")
		go func() {
			t := time.NewTicker(check)
			defer t.Stop()
			for {
				select {
				case <-execCtx.Done():
					return
				case <-t.C:
					if expired != nil && expired() {
						closing.Store("session")
						cancel()
						return
					}
					if opts.IdleTimeout > 0 && time.Since(time.Unix(0, lastActivity.Load())) > opts.IdleTimeout {
						_, _ = out.Write([]byte("\r\n[session fermée après " + opts.IdleTimeout.String() + " d'inactivité]\r\n"))
						closing.Store("idle")
						cancel()
						return
					}
				}
			}
		}()

		entry := audit.Entry{User: user.Name, Groups: user.Groups, Resource: "pods/exec", Namespace: ns, Name: pod,
			Detail: map[string]any{"container": container}}
		open := entry
		open.Verb, open.Result = "exec-open", "requested"
		auditLog.Record(open, nil)
		start := time.Now()

		shells := Shells
		if command != "" {
			shells = []string{command}
		}
		var runErr error
		for _, sh := range shells {
			// Un lecteur par tentative : celui d'une tentative échouée (bash absent)
			// rend EOF au lieu d'avaler les premières frappes destinées à la suivante.
			in := &attemptStdin{ch: stdin, done: make(chan struct{}), ctx: execCtx}
			sizes, stopSizes := attemptResize(resize, lastSize.Load())
			runErr = b.Exec(execCtx, user, ns, pod, container, []string{sh}, Streams{Stdin: in, Stdout: out, Resize: sizes})
			close(in.done)
			stopSizes()
			if !MissingExecutable(runErr) {
				break
			}
		}
		if MissingExecutable(runErr) {
			runErr = ErrNoShell
		}

		end := entry
		end.Verb, end.Duration = "exec-close", time.Since(start)
		var exit *ExitError
		switch closing.Load() {
		case "session":
			auditLog.Record(end, nil)
			c.Close(StatusSessionExpired, "session expirée")
			return
		case "idle":
			auditLog.Record(end, nil)
			_ = writeJSON(ctx, c, control{Type: "exit", Message: "inactivité"})
			c.Close(websocket.StatusNormalClosure, "")
			return
		}
		switch {
		case browserGone.Load() || ctx.Err() != nil:
			// Navigateur parti (onglet fermé, autre pod) : fin normale, rien à lui envoyer.
			auditLog.Record(end, nil)
			return
		case runErr == nil:
			auditLog.Record(end, nil)
			_ = writeJSON(ctx, c, control{Type: "exit"})
		case errors.As(runErr, &exit):
			auditLog.Record(end, nil)
			_ = writeJSON(ctx, c, control{Type: "exit", Code: exit.Code})
		default:
			auditLog.Record(end, runErr)
			log.Debug("exec: session terminée en erreur", "pod", ns+"/"+pod, "err", runErr)
			_ = writeJSON(ctx, c, control{Type: "error", Message: runErr.Error()})
		}
		c.Close(websocket.StatusNormalClosure, "")
	})
}

// attemptResize donne à une tentative son propre canal de tailles, prérempli
// avec la dernière taille connue : une tentative échouée ne doit pas consommer
// la taille envoyée à l'ouverture. stop ferme le canal (fin de la tentative).
func attemptResize(src <-chan Size, last *Size) (<-chan Size, func()) {
	out := make(chan Size, 1)
	if last != nil {
		out <- *last
	}
	done, exited := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(exited)
		for {
			select {
			case sz, ok := <-src:
				if !ok {
					return
				}
				select {
				case <-out:
				default:
				}
				out <- sz
			case <-done:
				return
			}
		}
	}()
	return out, func() {
		close(done)
		<-exited
		close(out)
	}
}

// attemptStdin lit les frappes du navigateur pour une tentative d'exec.
type attemptStdin struct {
	ch   <-chan []byte
	done chan struct{}
	ctx  context.Context
	rest []byte
}

func (a *attemptStdin) Read(p []byte) (int, error) {
	if len(a.rest) == 0 {
		select {
		case b := <-a.ch:
			a.rest = b
		case <-a.done:
			return 0, io.EOF
		case <-a.ctx.Done():
			return 0, io.EOF
		}
	}
	n := copy(p, a.rest)
	a.rest = a.rest[n:]
	return n, nil
}

// wsWriter envoie la sortie du terminal en messages binaires.
type wsWriter struct {
	mu    sync.Mutex
	ctx   context.Context
	c     *websocket.Conn
	touch func()
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.touch()
	ctx, cancel := context.WithTimeout(w.ctx, writeTimeout)
	defer cancel()
	if err := w.c.Write(ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func writeJSON(ctx context.Context, c *websocket.Conn, m control) error {
	b, _ := json.Marshal(m)
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, b)
}
