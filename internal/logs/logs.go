// Package logs relaie pods/log vers le navigateur par WebSocket. Le backend lit
// le flux au nom de l'utilisateur ; si le client ne suit pas, les lignes en
// trop sont ignorées et leur nombre lui est signalé (pas de mémoire illimitée).
package logs

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/inspect"
)

const (
	defaultTail  = 500
	maxTail      = 5000
	maxLineBytes = 16 << 10
	writeTimeout = 10 * time.Second
	pingInterval = 30 * time.Second
	maxBatch     = 1000
	// StatusSessionExpired : même code que /api/stream.
	StatusSessionExpired websocket.StatusCode = 4401
)

// sessionCheck : fréquence de vérification de la session.
var sessionCheck = 30 * time.Second

type Options struct {
	BufferLines   int           // lignes en attente côté serveur (2 000)
	FlushInterval time.Duration // regroupement des lignes (100 ms)
}

// Line est une ligne de log ; TS est vide si la ligne n'avait pas d'horodatage.
type Line struct {
	TS   string `json:"ts,omitempty"`
	Text string `json:"text"`
}

type message struct {
	Type    string `json:"type"` // lines | dropped | end | error
	Lines   []Line `json:"lines,omitempty"`
	Count   int64  `json:"count,omitempty"`
	Message string `json:"message,omitempty"`
	Status  int32  `json:"status,omitempty"`
}

// SessionFunc donne l'utilisateur de la requête et un test d'expiration (nil : jamais).
type SessionFunc func(r *http.Request) (access.User, func() bool)

// Handler sert /api/namespaces/{ns}/pods/{pod}/logs?container=&previous=&tailLines=.
func Handler(b inspect.Backend, session SessionFunc, opts Options, log *slog.Logger) http.Handler {
	if opts.BufferLines == 0 {
		opts.BufferLines = 2000
	}
	if opts.FlushInterval == 0 {
		opts.FlushInterval = 100 * time.Millisecond
	}
	check := sessionCheck
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ns, pod := chi.URLParam(r, "ns"), chi.URLParam(r, "pod")
		q := r.URL.Query()
		previous := q.Get("previous") == "true"
		tail := int64(defaultTail)
		if v, err := strconv.ParseInt(q.Get("tailLines"), 10, 64); err == nil && v > 0 {
			tail = min(v, maxTail)
		}
		user, expired := session(r)

		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := c.CloseRead(r.Context())

		rc, err := b.Logs(ctx, user, ns, pod, inspect.LogOptions{
			Container: q.Get("container"), Previous: previous, Follow: !previous, TailLines: tail,
		})
		if err != nil {
			// L'erreur de l'API server est transmise telle quelle (spec).
			m := message{Type: "error", Message: err.Error()}
			var st apierrors.APIStatus
			if errors.As(err, &st) {
				m.Status = st.Status().Code
			}
			_ = write(ctx, c, m)
			c.Close(websocket.StatusNormalClosure, "")
			return
		}
		defer rc.Close()

		r2 := &relay{lines: make(chan Line, opts.BufferLines)}
		readDone := make(chan error, 1)
		go func() { readDone <- r2.read(rc) }()

		flush := time.NewTicker(opts.FlushInterval)
		defer flush.Stop()
		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		sess := time.NewTicker(check)
		defer sess.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case err := <-readDone:
				// Fin du flux (container arrêté, instance précédente lue en entier).
				if r2.send(ctx, c) != nil {
					return
				}
				if err != nil && !errors.Is(err, context.Canceled) {
					log.Debug("logs: flux interrompu", "pod", ns+"/"+pod, "err", err)
					_ = write(ctx, c, message{Type: "error", Message: err.Error()})
				} else {
					_ = write(ctx, c, message{Type: "end"})
				}
				c.Close(websocket.StatusNormalClosure, "")
				return
			case <-flush.C:
				if r2.send(ctx, c) != nil {
					return
				}
			case <-sess.C:
				if expired != nil && expired() {
					c.Close(StatusSessionExpired, "session expirée")
					return
				}
			case <-ping.C:
				pctx, cancel := context.WithTimeout(ctx, writeTimeout)
				err := c.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	})
}

// relay découple la lecture de pods/log de l'écriture vers le navigateur.
type relay struct {
	lines   chan Line
	dropped atomic.Int64
}

func (r *relay) read(rc io.Reader) error {
	br := bufio.NewReaderSize(rc, 64<<10)
	for {
		raw, err := readLine(br)
		if raw != "" || err == nil {
			select {
			case r.lines <- parse(raw):
			default:
				r.dropped.Add(1) // le client ne suit pas
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readLine lit une ligne entière en la tronquant au-delà de maxLineBytes.
func readLine(br *bufio.Reader) (string, error) {
	var b []byte
	truncated := false
	for {
		chunk, isPrefix, err := br.ReadLine()
		if len(b) < maxLineBytes {
			b = append(b, chunk...)
		} else {
			truncated = true
		}
		if err != nil {
			return string(b), err
		}
		if !isPrefix {
			break
		}
	}
	if len(b) > maxLineBytes || truncated {
		b = b[:min(len(b), maxLineBytes)]
		for !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
		return string(b) + "…", nil
	}
	return string(b), nil
}

func parse(raw string) Line {
	ts, text, ok := strings.Cut(raw, " ")
	if ok {
		if _, err := time.Parse(inspect.LogTimeLayout, ts); err == nil {
			return Line{TS: ts, Text: text}
		}
	}
	return Line{Text: raw}
}

// send écrit les lignes en attente par lots, puis signale les lignes ignorées.
func (r *relay) send(ctx context.Context, c *websocket.Conn) error {
	for {
		batch := make([]Line, 0, 64)
	drain:
		for len(batch) < maxBatch {
			select {
			case l := <-r.lines:
				batch = append(batch, l)
			default:
				break drain
			}
		}
		if len(batch) == 0 {
			break
		}
		if err := write(ctx, c, message{Type: "lines", Lines: batch}); err != nil {
			return err
		}
		if len(batch) < maxBatch {
			break
		}
	}
	if n := r.dropped.Swap(0); n > 0 {
		return write(ctx, c, message{Type: "dropped", Count: n})
	}
	return nil
}

func write(ctx context.Context, c *websocket.Conn, m message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return c.Write(wctx, websocket.MessageText, b)
}
