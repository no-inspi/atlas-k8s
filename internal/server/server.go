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

	"github.com/no-inspi/cluster-atlas/internal/stream"
)

type Config struct {
	ClusterName string
	Demo        bool
	Static      fs.FS // contenu de web/dist
}

// csp interdit tout script, style ou police externe : le front est servi
// entièrement par le backend.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
	"font-src 'self'; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

func New(cfg Config, hub *stream.Hub, log *slog.Logger) http.Handler {
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

	r.Route("/api", func(r chi.Router) {
		r.Get("/me", func(w http.ResponseWriter, _ *http.Request) {
			// Au jalon 4, l'utilisateur et ses groupes viendront de la session OIDC.
			writeJSON(w, map[string]any{"user": "demo", "groups": []string{"demo"}, "cluster": cfg.ClusterName, "demo": cfg.Demo})
		})
		r.Handle("/stream", stream.Handler(hub, log))
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "route inconnue", http.StatusNotFound) })
	})

	r.NotFound(spa(cfg.Static))
	return r
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
