package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2"
)

const (
	sessionCookie = "atlas_session"
	oauthCookie   = "atlas_oauth"
	loginTimeout  = 10 * time.Minute
)

type Config struct {
	IssuerURL, ClientID, ClientSecret string
	// PublicURL est l'URL de l'application vue du navigateur (sans « / » final) :
	// elle donne l'URL de retour OIDC et le caractère Secure des cookies.
	PublicURL     string
	Scopes        []string
	UsernameClaim string
	GroupsClaim   string
	GroupsPrefix  string
	SessionTTL    time.Duration
	CookieKey     []byte
}

type Auth struct {
	cfg      Config
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
	sealer   *Sealer
	secure   bool
	log      *slog.Logger
	now      func() time.Time
}

// loginState voyage dans le cookie atlas_oauth entre /auth/login et le callback.
type loginState struct {
	State    string    `json:"s"`
	Nonce    string    `json:"n"`
	Verifier string    `json:"v"`
	ReturnTo string    `json:"r"`
	Expires  time.Time `json:"e"`
}

// New interroge la découverte OIDC de l'issuer (avec quelques essais : au
// démarrage du pod, le réseau ou l'IdP peuvent ne pas être encore joignables).
func New(ctx context.Context, cfg Config, log *slog.Logger) (*Auth, error) {
	pub, err := url.Parse(cfg.PublicURL)
	if err != nil || pub.Host == "" || (pub.Scheme != "http" && pub.Scheme != "https") {
		return nil, fmt.Errorf("URL publique invalide %q (ATLAS_PUBLIC_URL) : elle sert d'URL de retour OIDC", cfg.PublicURL)
	}
	cfg.PublicURL = strings.TrimSuffix(cfg.PublicURL, "/")
	if cfg.ClientID == "" || cfg.UsernameClaim == "" {
		return nil, errors.New("OIDC : client ID et claim d'utilisateur requis")
	}
	sealer, err := NewSealer(cfg.CookieKey)
	if err != nil {
		return nil, err
	}

	var provider *oidc.Provider
	for attempt := 1; ; attempt++ {
		provider, err = oidc.NewProvider(ctx, cfg.IssuerURL)
		if err == nil || attempt == 5 {
			break
		}
		log.Warn("découverte OIDC impossible, nouvel essai", "issuer", cfg.IssuerURL, "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 2 * time.Second):
		}
	}
	if err != nil {
		return nil, fmt.Errorf("découverte OIDC de %s : %w", cfg.IssuerURL, err)
	}

	return &Auth{
		cfg:      cfg,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth: oauth2.Config{
			ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: provider.Endpoint(),
			RedirectURL: cfg.PublicURL + "/auth/callback", Scopes: cfg.Scopes,
		},
		sealer: sealer,
		secure: pub.Scheme == "https",
		log:    log,
		now:    time.Now,
	}, nil
}

func (a *Auth) Routes(r chi.Router) {
	r.Get("/auth/login", a.login)
	r.Get("/auth/callback", a.callback)
	r.Get("/auth/logout", a.logout)
	r.Get("/auth/logged-out", loggedOut)
}

func randomString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// safeReturn n'accepte qu'un chemin local, pour qu'un lien de connexion ne
// puisse pas renvoyer vers un autre site.
func safeReturn(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return "/"
	}
	return p
}

func (a *Auth) login(w http.ResponseWriter, r *http.Request) {
	st := loginState{
		State: randomString(), Nonce: randomString(), Verifier: oauth2.GenerateVerifier(),
		ReturnTo: safeReturn(r.URL.Query().Get("return")), Expires: a.now().Add(loginTimeout),
	}
	v, err := a.sealer.Seal(oauthCookie, st)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.setCookie(w, oauthCookie, v, loginTimeout)
	http.Redirect(w, r, a.oauth.AuthCodeURL(st.State, oidc.Nonce(st.Nonce), oauth2.S256ChallengeOption(st.Verifier)), http.StatusFound)
}

func (a *Auth) callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		a.log.Warn("connexion refusée par l'IdP", "error", e, "description", q.Get("error_description"))
		http.Error(w, "connexion refusée par le fournisseur d'identité : "+e, http.StatusForbidden)
		return
	}
	var st loginState
	c, err := r.Cookie(oauthCookie)
	if err != nil || a.sealer.Open(oauthCookie, c.Value, &st) != nil || a.now().After(st.Expires) || q.Get("state") != st.State {
		http.Error(w, "tentative de connexion expirée ou invalide : recommencez depuis la page d'accueil", http.StatusBadRequest)
		return
	}
	a.clearCookie(w, oauthCookie)

	tok, err := a.oauth.Exchange(r.Context(), q.Get("code"), oauth2.VerifierOption(st.Verifier))
	if err != nil {
		a.log.Warn("échange du code OIDC impossible", "err", err)
		http.Error(w, "échange du code d'autorisation impossible", http.StatusUnauthorized)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := a.verifier.Verify(r.Context(), raw)
	if err != nil || idt.Nonce != st.Nonce {
		a.log.Warn("ID token refusé", "err", err)
		http.Error(w, "jeton d'identité invalide", http.StatusUnauthorized)
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		http.Error(w, "jeton d'identité illisible", http.StatusUnauthorized)
		return
	}
	sess, err := a.identity(claims)
	if err != nil {
		a.log.Warn("identité refusée", "err", err)
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	v, err := a.sealer.Seal(sessionCookie, sess)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	a.setCookie(w, sessionCookie, v, a.cfg.SessionTTL)
	a.log.Info("connexion", "user", sess.User, "groups", sess.Groups)
	http.Redirect(w, r, st.ReturnTo, http.StatusFound)
}

// identity construit l'identité impersonnée. Un utilisateur system:* est
// refusé et les groupes system:* ignorés : l'IdP ne doit jamais pouvoir
// obtenir system:masters.
func (a *Auth) identity(claims map[string]any) (Session, error) {
	user, _ := claims[a.cfg.UsernameClaim].(string)
	if user == "" {
		return Session{}, fmt.Errorf("le jeton n'a pas de claim %q", a.cfg.UsernameClaim)
	}
	if a.cfg.UsernameClaim == "email" {
		if v, ok := claims["email_verified"].(bool); ok && !v {
			return Session{}, errors.New("adresse email non vérifiée par le fournisseur d'identité")
		}
	}
	if strings.HasPrefix(user, "system:") {
		return Session{}, fmt.Errorf("nom d'utilisateur réservé : %q", user)
	}
	var groups []string
	if a.cfg.GroupsClaim != "" {
		for _, g := range stringList(claims[a.cfg.GroupsClaim]) {
			if g == "" || strings.HasPrefix(g, "system:") {
				continue
			}
			groups = append(groups, a.cfg.GroupsPrefix+g)
		}
	}
	return Session{User: user, Groups: groups, Expires: a.now().Add(a.cfg.SessionTTL)}, nil
}

func stringList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (a *Auth) logout(w http.ResponseWriter, r *http.Request) {
	a.clearCookie(w, sessionCookie)
	http.Redirect(w, r, "/auth/logged-out", http.StatusFound)
}

var loggedOutPage = template.Must(template.New("").Parse(`<!doctype html><html lang="fr"><meta charset="utf-8">
<title>Déconnecté · Cluster Atlas</title><body><p>Vous êtes déconnecté de Cluster Atlas.</p><p><a href="/auth/login">Se reconnecter</a></p></body></html>`))

func loggedOut(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loggedOutPage.Execute(w, nil)
}

func (a *Auth) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(ttl.Seconds()),
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode})
}

func (a *Auth) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode})
}

/* ---------- protection des routes ---------- */

type ctxKey struct{}

// FromContext renvoie la session posée par RequireAPI/RequirePage (nil sinon).
func FromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxKey{}).(*Session)
	return s
}

// WithSession place une session dans le contexte (tests, mode none).
func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// Session lit et vérifie le cookie de session.
func (a *Auth) Session(r *http.Request) (*Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	var s Session
	if a.sealer.Open(sessionCookie, c.Value, &s) != nil || a.now().After(s.Expires) {
		return nil, false
	}
	return &s, true
}

// Expired indique si la session est arrivée à échéance (flux longs).
func (a *Auth) Expired(s *Session) bool { return a.now().After(s.Expires) }

func (a *Auth) RequireAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := a.Session(r)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "non authentifié", "login": "/auth/login"})
			return
		}
		next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), s)))
	})
}

func (a *Auth) RequirePage(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, ok := a.Session(r)
		if !ok {
			http.Redirect(w, r, "/auth/login?return="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), s)))
	})
}

// CSRFHeader doit accompagner toute requête mutante : un site tiers ne peut pas
// le poser sans pré-vol CORS, que l'application n'autorise jamais.
const CSRFHeader = "X-Atlas-Request"

func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get(CSRFHeader) != "1" {
				http.Error(w, "en-tête "+CSRFHeader+" manquant", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
