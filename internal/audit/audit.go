// Package audit écrit une ligne JSON sur stdout pour chaque action en écriture
// et chaque ouverture ou fermeture de session exec (spec, « Audit »). Les
// commandes tapées dans le shell ne sont pas enregistrées.
package audit

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type Entry struct {
	User      string
	Groups    []string
	Verb      string // delete, scale, restart, cordon, uncordon, drain, exec-open, exec-close
	Resource  string
	Namespace string
	Name      string
	Duration  time.Duration
	// Detail complète l'entrée (replicas demandés, container, nombre de pods évincés…).
	Detail map[string]any
	// Result impose le résultat (sinon déduit de l'erreur) : « requested » pour
	// une ouverture d'exec, dont l'issue est portée par exec-close.
	Result string
}

type line struct {
	Time       string         `json:"time"`
	Audit      bool           `json:"audit"`
	User       string         `json:"user"`
	Groups     []string       `json:"groups"`
	Verb       string         `json:"verb"`
	Resource   string         `json:"resource"`
	Namespace  string         `json:"namespace,omitempty"`
	Name       string         `json:"name"`
	Result     string         `json:"result"`
	DurationMs int64          `json:"durationMs"`
	Error      string         `json:"error,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
}

type Logger struct {
	mu  sync.Mutex
	enc *json.Encoder
	now func() time.Time
}

func New(w io.Writer) *Logger { return &Logger{enc: json.NewEncoder(w), now: time.Now} }

// Result classe une erreur : success, forbidden ou failure.
func Result(err error) string {
	switch {
	case err == nil:
		return "success"
	case apierrors.IsForbidden(err):
		return "forbidden"
	}
	return "failure"
}

func (l *Logger) Record(e Entry, err error) {
	groups := e.Groups
	if groups == nil {
		groups = []string{}
	}
	ln := line{Time: l.now().UTC().Format(time.RFC3339Nano), Audit: true, User: e.User, Groups: groups, Verb: e.Verb,
		Resource: e.Resource, Namespace: e.Namespace, Name: e.Name, Result: Result(err), DurationMs: e.Duration.Milliseconds(), Detail: e.Detail}
	if e.Result != "" {
		ln.Result = e.Result
	}
	if err != nil {
		ln.Error = err.Error()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(ln)
}
