package auth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type fixture struct {
	idp  *testIdP
	auth *Auth
	h    http.Handler
}

func newFixture(t *testing.T, mutate ...func(*Config)) *fixture {
	t.Helper()
	idp := newTestIdP(t)
	cfg := Config{
		IssuerURL: idp.srv.URL, ClientID: idp.client, ClientSecret: idp.secret,
		PublicURL: "https://atlas.test", Scopes: []string{"openid", "email", "profile"},
		UsernameClaim: "email", GroupsClaim: "groups", GroupsPrefix: "oidc:",
		SessionTTL: 8 * time.Hour, CookieKey: testKey(9),
	}
	for _, m := range mutate {
		m(&cfg)
	}
	a, err := New(context.Background(), cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	a.Routes(r)
	r.Route("/api", func(r chi.Router) {
		r.Use(a.RequireAPI, CSRF)
		r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
			s := FromContext(r.Context())
			_, _ = io.WriteString(w, s.User+"|"+strings.Join(s.Groups, ","))
		})
		r.Post("/do", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "fait") })
	})
	r.With(a.RequirePage).Get("/*", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "page") })
	return &fixture{idp: idp, auth: a, h: r}
}

func (f *fixture) do(req *http.Request, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	return rec
}

func cookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// login suit le parcours complet et renvoie la réponse du callback.
func (f *fixture) login(t *testing.T, returnTo string, claims map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	rec := f.do(httptest.NewRequest("GET", "/auth/login?return="+url.QueryEscape(returnTo), nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("/auth/login = %d", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	q := loc.Query()
	if !strings.HasPrefix(loc.String(), f.idp.srv.URL+"/authorize") || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != f.auth.cfg.PublicURL+"/auth/callback" || q.Get("state") == "" || q.Get("nonce") == "" {
		t.Fatalf("redirection vers l'IdP inattendue : %s", loc)
	}
	if _, ok := claims["nonce"]; !ok {
		claims["nonce"] = q.Get("nonce")
	}
	f.idp.issueCode("code-1", q.Get("code_challenge"), claims)
	cb := httptest.NewRequest("GET", "/auth/callback?code=code-1&state="+url.QueryEscape(q.Get("state")), nil)
	return f.do(cb, cookie(rec, oauthCookie))
}

var alice = func() map[string]any {
	return map[string]any{"email": "alice@example.com", "email_verified": true, "groups": []string{"sre", "system:masters"}}
}

func TestLoginFlow(t *testing.T) {
	f := newFixture(t)
	rec := f.login(t, "/pods/production/api", alice())
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/pods/production/api" {
		t.Fatalf("callback = %d → %q : %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	sess := cookie(rec, sessionCookie)
	if sess == nil || !sess.HttpOnly || !sess.Secure || sess.SameSite != http.SameSiteLaxMode || sess.Path != "/" {
		t.Fatalf("cookie de session = %+v", sess)
	}
	if c := cookie(rec, oauthCookie); c == nil || c.MaxAge >= 0 {
		t.Error("le cookie de login doit être effacé après le callback")
	}

	me := f.do(httptest.NewRequest("GET", "/api/me", nil), sess)
	if me.Body.String() != "alice@example.com|oidc:sre" {
		t.Errorf("/api/me = %q (groupes préfixés, system:* ignorés)", me.Body)
	}
}

func TestReturnToMustBeLocal(t *testing.T) {
	for _, evil := range []string{"https://evil.example", "//evil.example/x", "/\\evil.example"} {
		f := newFixture(t)
		if loc := f.login(t, evil, alice()).Header().Get("Location"); loc != "/" {
			t.Errorf("retour vers %q accepté : %q", evil, loc)
		}
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	f := newFixture(t)
	rec := f.do(httptest.NewRequest("GET", "/auth/login", nil))
	cb := httptest.NewRequest("GET", "/auth/callback?code=x&state=forged", nil)
	if got := f.do(cb, cookie(rec, oauthCookie)).Code; got != http.StatusBadRequest {
		t.Errorf("state forgé = %d, attendu 400", got)
	}
	if got := f.do(httptest.NewRequest("GET", "/auth/callback?code=x&state=y", nil)).Code; got != http.StatusBadRequest {
		t.Errorf("sans cookie de login = %d, attendu 400", got)
	}
}

func TestCallbackRejectsBadNonceAndIdentities(t *testing.T) {
	cases := map[string]map[string]any{
		"nonce forgé":       {"email": "a@x", "email_verified": true, "nonce": "forged"},
		"email non vérifié": {"email": "a@x", "email_verified": false},
		"utilisateur system": {"email": "system:admin", "email_verified": true},
		"claim absent":      {"email_verified": true},
	}
	for name, claims := range cases {
		f := newFixture(t)
		rec := f.login(t, "/", claims)
		if rec.Code < 400 || cookie(rec, sessionCookie) != nil {
			t.Errorf("%s : callback = %d, session %v", name, rec.Code, cookie(rec, sessionCookie))
		}
	}
}

func TestIdPErrorIsReported(t *testing.T) {
	f := newFixture(t)
	rec := f.do(httptest.NewRequest("GET", "/auth/callback?error=access_denied&error_description=refus", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "access_denied") {
		t.Errorf("erreur IdP = %d %q", rec.Code, rec.Body)
	}
}

func TestProtection(t *testing.T) {
	f := newFixture(t)
	api := f.do(httptest.NewRequest("GET", "/api/me", nil))
	if api.Code != http.StatusUnauthorized || !strings.Contains(api.Header().Get("Content-Type"), "json") {
		t.Errorf("API sans session = %d", api.Code)
	}
	page := f.do(httptest.NewRequest("GET", "/pods/x?y=1", nil))
	if page.Code != http.StatusFound || page.Header().Get("Location") != "/auth/login?return=%2Fpods%2Fx%3Fy%3D1" {
		t.Errorf("page sans session = %d → %q", page.Code, page.Header().Get("Location"))
	}
	bogus := &http.Cookie{Name: sessionCookie, Value: "abc"}
	if got := f.do(httptest.NewRequest("GET", "/api/me", nil), bogus).Code; got != http.StatusUnauthorized {
		t.Errorf("cookie invalide = %d", got)
	}
}

func TestSessionExpires(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.SessionTTL = time.Hour })
	sess := cookie(f.login(t, "/", alice()), sessionCookie)
	f.auth.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if got := f.do(httptest.NewRequest("GET", "/api/me", nil), sess).Code; got != http.StatusUnauthorized {
		t.Errorf("session expirée = %d", got)
	}
}

func TestCSRF(t *testing.T) {
	f := newFixture(t)
	sess := cookie(f.login(t, "/", alice()), sessionCookie)
	if got := f.do(httptest.NewRequest("POST", "/api/do", nil), sess).Code; got != http.StatusForbidden {
		t.Errorf("POST sans en-tête = %d, attendu 403", got)
	}
	req := httptest.NewRequest("POST", "/api/do", nil)
	req.Header.Set(CSRFHeader, "1")
	if got := f.do(req, sess).Code; got != http.StatusOK {
		t.Errorf("POST avec en-tête = %d", got)
	}
}

func TestLogout(t *testing.T) {
	f := newFixture(t)
	rec := f.do(httptest.NewRequest("GET", "/auth/logout", nil))
	if c := cookie(rec, sessionCookie); c == nil || c.MaxAge >= 0 {
		t.Errorf("logout doit effacer la session : %+v", c)
	}
	if rec.Code != http.StatusFound {
		t.Errorf("logout = %d", rec.Code)
	}
}

func TestHTTPPublicURLGivesNonSecureCookie(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.PublicURL = "http://localhost:5173" })
	if c := cookie(f.login(t, "/", alice()), sessionCookie); c == nil || c.Secure {
		t.Errorf("en http local, le cookie ne peut pas être Secure : %+v", c)
	}
}
