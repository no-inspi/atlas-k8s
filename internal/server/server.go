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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"k8s.io/client-go/kubernetes"

	"github.com/no-inspi/cluster-atlas/internal/access"
	"github.com/no-inspi/cluster-atlas/internal/auth"
	"github.com/no-inspi/cluster-atlas/internal/stream"
)

type Config struct {
	ClusterName string
	Demo        bool
	// User est l'utilisateur affiché sans authentification (démo, auth none).
	User   string
	Static fs.FS // contenu de web/dist

	// Auth est nil sans authentification : pas d'impersonation ni de filtrage.
	Auth     *auth.Auth
	Reviewer *access.Reviewer // filtrage du flux (avec Auth)
	Clients  ClientsFor       // clients impersonnés (avec Auth)
	// Kube : client du backend, utilisé sans authentification (auth none) ;
	// nil en démo.
	Kube kubernetes.Interface
}

// ClientsFor fournit le client Kubernetes impersonné d'un utilisateur
// (*access.Clients).
type ClientsFor interface {
	For(u access.User) (kubernetes.Interface, error)
}

// csp interdit tout script, style ou police externe : le front est servi
// entièrement par le backend.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
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
	})
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
		c, err := s.cfg.Clients.For(s.user(r))
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
