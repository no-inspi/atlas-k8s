// Package server assemble le routeur HTTP : probes, API et front embarqué.
package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/actions"
	"github.com/no-inspi/atlas-k8s/internal/audit"
	"github.com/no-inspi/atlas-k8s/internal/auth"
	"github.com/no-inspi/atlas-k8s/internal/exec"
	"github.com/no-inspi/atlas-k8s/internal/inspect"
	"github.com/no-inspi/atlas-k8s/internal/logs"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

type Config struct {
	ClusterName string
	Demo        bool
	// User est l'utilisateur affiché sans authentification (démo, auth none).
	User   string
	Static fs.FS // contenu de web/dist

	// Auth est nil sans authentification : pas d'impersonation ni de filtrage.
	Auth     *auth.Auth
	Reviewer *access.Reviewer    // filtrage du flux (avec Auth)
	Clients  access.ClientSource // clients impersonnés (avec Auth)
	// Kube : client du backend, utilisé sans authentification (auth none) ;
	// nil en démo.
	Kube kubernetes.Interface
	// Inspect répond à l'inspecteur (propriétaires, YAML, événements, logs).
	Inspect inspect.Backend
	// Actions applique les actions d'exploitation ; Audit les journalise.
	Actions  actions.Backend
	Audit    *audit.Logger
	Features Features
	// Exec ouvre les terminaux (gardé par Features.Exec*).
	Exec exec.Backend
}

// Features : options du chart (features.exec, features.actions).
type Features struct {
	ActionsEnabled       bool
	ExecEnabled          bool
	ExecDeniedNamespaces []string
	ExecIdleTimeout      time.Duration
}

// csp interdit tout script et toute police externes : le front est servi
// entièrement par le backend. Les styles en ligne sont autorisés pour Monaco
// (onglet YAML), qui crée ses balises <style> sans prise en charge de nonce ;
// les scripts, eux, restent limités à 'self'.
const csp = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
	"font-src 'self'; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

type server struct {
	cfg Config
	hub *stream.Hub
	log *slog.Logger
}

func New(cfg Config, hub *stream.Hub, log *slog.Logger) http.Handler {
	s := &server{cfg: cfg, hub: hub, log: log}
	r := chi.NewRouter()
	r.Use(middleware.Recoverer, securityHeaders)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !hub.Ready() {
			http.Error(w, "caches en cours de synchronisation", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	if cfg.Auth != nil {
		cfg.Auth.Routes(r)
	}

	r.Route("/api", func(r chi.Router) {
		if cfg.Auth != nil {
			r.Use(cfg.Auth.RequireAPI)
		}
		r.Use(auth.CSRF)
		r.Get("/me", s.me)
		r.Handle("/stream", stream.Handler(hub, log, s.streamClient))
		r.Post("/access-review", s.accessReview)
		if cfg.Inspect != nil {
			r.Get("/namespaces/{ns}/pods/{pod}/owner", s.owner)
			r.Get("/namespaces/{ns}/{resource}/{name}/events", s.events)
			r.Get("/yaml/{group}/{version}/{kind}/{ns}/{name}", s.yaml)
			r.Handle("/namespaces/{ns}/pods/{pod}/logs", logs.Handler(cfg.Inspect, s.session, logs.Options{}, log))
		}
		if cfg.Exec != nil {
			r.Handle("/namespaces/{ns}/pods/{pod}/exec", exec.Handler(cfg.Exec, s.session, exec.Options{
				Enabled: cfg.Features.ExecEnabled, DeniedNamespaces: cfg.Features.ExecDeniedNamespaces, IdleTimeout: cfg.Features.ExecIdleTimeout,
			}, cfg.Audit, log))
		}
		// Console en lecture seule (features.actions.enabled=false) : routes absentes.
		if cfg.Actions != nil && cfg.Features.ActionsEnabled {
			r.Delete("/namespaces/{ns}/pods/{pod}", s.deletePod)
			r.Patch("/namespaces/{ns}/{kind}/{name}/scale", s.scale)
			r.Post("/namespaces/{ns}/{kind}/{name}/restart", s.restart)
			r.Post("/nodes/{node}/cordon", s.cordon(true))
			r.Post("/nodes/{node}/uncordon", s.cordon(false))
			r.Post("/nodes/{node}/drain", s.drain)
		}
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "route inconnue", http.StatusNotFound) })
	})

	pages := spa(cfg.Static)
	if cfg.Auth != nil {
		protected := cfg.Auth.RequirePage(pages)
		pages = func(w http.ResponseWriter, r *http.Request) {
			// Le code du front n'est pas un secret : seules les pages exigent une session.
			if isPublicAsset(r.URL.Path) {
				spa(cfg.Static)(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		}
	}
	r.NotFound(pages)
	return r
}

func isPublicAsset(p string) bool {
	return strings.HasPrefix(p, "/assets/") || p == "/favicon.svg"
}

// user : identité de la requête (session OIDC, ou utilisateur fixe sans auth).
func (s *server) user(r *http.Request) access.User {
	if sess := auth.FromContext(r.Context()); sess != nil {
		return access.User{Name: sess.User, Groups: sess.Groups}
	}
	return access.User{Name: s.cfg.User}
}

func (s *server) me(w http.ResponseWriter, r *http.Request) {
	u := s.user(r)
	groups := u.Groups
	if groups == nil {
		groups = []string{}
	}
	writeJSON(w, map[string]any{
		"user": u.Name, "groups": groups, "cluster": s.cfg.ClusterName, "demo": s.cfg.Demo,
		"authenticated": s.cfg.Auth != nil,
		"features": map[string]any{
			"actions": s.cfg.Actions != nil && s.cfg.Features.ActionsEnabled,
			"exec":    s.cfg.Exec != nil && s.cfg.Features.ExecEnabled, "execDeniedNamespaces": nonNil(s.cfg.Features.ExecDeniedNamespaces),
		},
	})
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// act exécute une action, la journalise (audit) et répond : 200 avec le
// résultat, ou l'erreur de l'API server telle quelle.
func (s *server) act(w http.ResponseWriter, r *http.Request, e audit.Entry, run func(u access.User) (any, error)) {
	u := s.user(r)
	e.User, e.Groups = u.Name, u.Groups
	start := time.Now()
	res, err := run(u)
	e.Duration = time.Since(start)
	if s.cfg.Audit != nil {
		s.cfg.Audit.Record(e, err)
	}
	if err != nil {
		apiError(w, err)
		return
	}
	if res == nil {
		res = map[string]bool{"ok": true}
	}
	writeJSON(w, res)
}

func (s *server) deletePod(w http.ResponseWriter, r *http.Request) {
	ns, name := chi.URLParam(r, "ns"), chi.URLParam(r, "pod")
	s.act(w, r, audit.Entry{Verb: "delete", Resource: "pods", Namespace: ns, Name: name}, func(u access.User) (any, error) {
		return nil, s.cfg.Actions.DeletePod(r.Context(), u, ns, name)
	})
}

func badRequest(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *server) scale(w http.ResponseWriter, r *http.Request) {
	ns, resource, name := chi.URLParam(r, "ns"), chi.URLParam(r, "kind"), chi.URLParam(r, "name")
	kind, ok := actions.ScalableKinds[resource]
	if !ok {
		badRequest(w, "scale possible seulement sur deployments et statefulsets")
		return
	}
	var body struct {
		Replicas *int32 `json:"replicas"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || body.Replicas == nil ||
		*body.Replicas < 0 || *body.Replicas > 1000 {
		badRequest(w, `corps attendu : {"replicas": 0 à 1000}`)
		return
	}
	n := *body.Replicas
	s.act(w, r, audit.Entry{Verb: "scale", Resource: resource, Namespace: ns, Name: name, Detail: map[string]any{"replicas": n}},
		func(u access.User) (any, error) { return nil, s.cfg.Actions.Scale(r.Context(), u, ns, kind, name, n) })
}

func (s *server) restart(w http.ResponseWriter, r *http.Request) {
	ns, resource, name := chi.URLParam(r, "ns"), chi.URLParam(r, "kind"), chi.URLParam(r, "name")
	kind, ok := actions.RestartableKinds[resource]
	if !ok {
		badRequest(w, "rollout restart possible seulement sur deployments, statefulsets et daemonsets")
		return
	}
	s.act(w, r, audit.Entry{Verb: "restart", Resource: resource, Namespace: ns, Name: name},
		func(u access.User) (any, error) { return nil, s.cfg.Actions.Restart(r.Context(), u, ns, kind, name) })
}

func (s *server) cordon(unschedulable bool) http.HandlerFunc {
	verb := "uncordon"
	if unschedulable {
		verb = "cordon"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		node := chi.URLParam(r, "node")
		s.act(w, r, audit.Entry{Verb: verb, Resource: "nodes", Name: node},
			func(u access.User) (any, error) {
				return nil, s.cfg.Actions.SetUnschedulable(r.Context(), u, node, unschedulable)
			})
	}
}

// drain : ?dryRun=true renvoie le récapitulatif (lecture, pas d'audit).
func (s *server) drain(w http.ResponseWriter, r *http.Request) {
	node := chi.URLParam(r, "node")
	if r.URL.Query().Get("dryRun") == "true" {
		plan, err := s.cfg.Actions.DrainPlan(r.Context(), s.user(r), node)
		if err != nil {
			apiError(w, err)
			return
		}
		writeJSON(w, plan)
		return
	}
	s.act(w, r, audit.Entry{Verb: "drain", Resource: "nodes", Name: node}, func(u access.User) (any, error) {
		res, err := s.cfg.Actions.Drain(r.Context(), u, node)
		return res, err
	})
}

// session : utilisateur et test d'expiration, pour les WebSockets longs.
func (s *server) session(r *http.Request) (access.User, func() bool) {
	sess := auth.FromContext(r.Context())
	if s.cfg.Auth == nil || sess == nil {
		return s.user(r), nil
	}
	return s.user(r), func() bool { return s.cfg.Auth.Expired(sess) }
}

// apiError transmet l'erreur de l'API server telle quelle (code et message).
func apiError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	var st apierrors.APIStatus
	switch {
	case errors.Is(err, inspect.ErrUnsupportedKind), errors.Is(err, actions.ErrUnsupportedKind):
		code = http.StatusBadRequest
	case errors.As(err, &st) && st.Status().Code != 0:
		code = int(st.Status().Code)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func (s *server) owner(w http.ResponseWriter, r *http.Request) {
	chain, err := s.cfg.Inspect.Owners(r.Context(), s.user(r), chi.URLParam(r, "ns"), chi.URLParam(r, "pod"))
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"chain": chain})
}

// nsParam : namespace de l'URL ; « _ » désigne un objet sans namespace
// (PersistentVolume, GatewayClass). Un namespace Kubernetes ne peut pas
// s'appeler « _ » (noms DNS-1123), la traduction est sans ambiguïté.
func nsParam(r *http.Request) string {
	if ns := chi.URLParam(r, "ns"); ns != "_" {
		return ns
	}
	return ""
}

// events : /api/namespaces/{ns}/{resource}/{name}/events (pods, services, persistentvolumeclaims, ingresses, ingressroutes…).
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	kind, ok := inspect.KindForResource(chi.URLParam(r, "resource"))
	if !ok {
		apiError(w, inspect.ErrUnsupportedKind)
		return
	}
	evs, err := s.cfg.Inspect.Events(r.Context(), s.user(r), kind, nsParam(r), chi.URLParam(r, "name"))
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"events": evs})
}

// yaml : /api/yaml/{group}/{version}/{kind}/{ns}/{name}, « core » pour le groupe vide.
func (s *server) yaml(w http.ResponseWriter, r *http.Request) {
	group := chi.URLParam(r, "group")
	if group == "core" {
		group = ""
	}
	doc, err := s.cfg.Inspect.YAML(r.Context(), s.user(r), inspect.Ref{
		Group: group, Version: chi.URLParam(r, "version"), Kind: chi.URLParam(r, "kind"),
		Namespace: nsParam(r), Name: chi.URLParam(r, "name"),
	})
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, doc)
}

func (s *server) streamClient(r *http.Request) (stream.Client, error) {
	sess := auth.FromContext(r.Context())
	if s.cfg.Auth == nil || sess == nil {
		return stream.Unfiltered(r)
	}
	u := access.User{Name: sess.User, Groups: sess.Groups}
	return stream.Client{
		Filter:  access.NewStreamFilter(s.cfg.Reviewer, u),
		Expired: func() bool { return s.cfg.Auth.Expired(sess) },
	}, nil
}

// accessReview répond à un lot de vérifications pour griser les actions.
// C'est l'API server qui décide, au nom de l'utilisateur ; son refus final
// reste celui de l'appel réel.
func (s *server) accessReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Checks []access.Check `json:"checks"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "corps JSON invalide", http.StatusBadRequest)
		return
	}
	var client kubernetes.Interface
	switch {
	case s.cfg.Auth != nil:
		c, err := s.cfg.Clients.Kube(s.user(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		client = c
	case s.cfg.Kube != nil:
		client = s.cfg.Kube
	default: // démo : tout est permis, rien n'est réel
		res := make([]access.Result, len(body.Checks))
		for i := range res {
			res[i] = access.Result{Allowed: true, Reason: "mode démo"}
		}
		writeJSON(w, map[string]any{"results": res})
		return
	}
	res, err := access.Review(r.Context(), client, body.Checks)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"results": res})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// spa sert les fichiers du front et renvoie index.html pour toute autre route,
// sauf sous /assets/ où un fichier absent est une vraie 404.
func spa(static fs.FS) http.HandlerFunc {
	files := http.FileServerFS(static)
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if st, err := fs.Stat(static, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		index, err := fs.ReadFile(static, "index.html")
		if errors.Is(err, fs.ErrNotExist) {
			http.Error(w, "front non compilé : lancez `make web`", http.StatusServiceUnavailable)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
