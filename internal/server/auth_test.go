package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/cluster-atlas/internal/access"
	"github.com/no-inspi/cluster-atlas/internal/auth"
	"github.com/no-inspi/cluster-atlas/internal/auth/authtest"
	"github.com/no-inspi/cluster-atlas/internal/model"
	"github.com/no-inspi/cluster-atlas/internal/stream"
)

type fixedClients struct{ c kubernetes.Interface }

func (f fixedClients) For(access.User) (kubernetes.Interface, error) { return f.c, nil }

// Politique de test : le groupe oidc:dev ne voit que production.
func rbacClient() *fake.Clientset {
	c := fake.NewClientset()
	c.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		sar := a.(k8stesting.CreateAction).GetObject().(*authzv1.SubjectAccessReview)
		sar.Status.Allowed = sar.Spec.ResourceAttributes.Namespace == "production"
		return true, sar, nil
	})
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		s := a.(k8stesting.CreateAction).GetObject().(*authzv1.SelfSubjectAccessReview)
		s.Status.Allowed = s.Spec.ResourceAttributes.Verb == "get"
		return true, s, nil
	})
	return c
}

func TestAuthenticatedServer(t *testing.T) {
	idp := authtest.NewIdP(t)
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	defer srv.Close()

	a, err := auth.New(context.Background(), auth.Config{
		IssuerURL: idp.URL, ClientID: idp.ClientID, ClientSecret: idp.Secret, PublicURL: srv.URL,
		Scopes: []string{"openid", "email"}, UsernameClaim: "email", GroupsClaim: "groups", GroupsPrefix: "oidc:",
		SessionTTL: time.Hour, CookieKey: make([]byte, 32),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	hub := stream.NewHub(stream.Options{})
	hub.Upsert(stream.KindPod, "p1", model.Pod{UID: "p1", Name: "api", Namespace: "production"})
	hub.Upsert(stream.KindPod, "k1", model.Pod{UID: "k1", Name: "coredns", Namespace: "kube-system"})
	hub.Flush()
	client := rbacClient()
	srv.Config.Handler = New(Config{ClusterName: "kind", Static: static, Auth: a,
		Reviewer: access.NewReviewer(client), Clients: fixedClients{client}}, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))

	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Pages protégées, assets publics.
	resp, _ := browser.Get(srv.URL + "/")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(resp.Header.Get("Location"), "/auth/login") {
		t.Fatalf("page sans session = %d → %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := browser.Get(srv.URL + "/assets/app-1.js"); resp.StatusCode != 200 {
		t.Errorf("asset public = %d", resp.StatusCode)
	}
	if resp, _ := browser.Get(srv.URL + "/api/me"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/api/me sans session = %d", resp.StatusCode)
	}

	// Connexion de bob.
	resp, _ = browser.Get(srv.URL + "/auth/login")
	loc, _ := url.Parse(resp.Header.Get("Location"))
	q := loc.Query()
	idp.IssueCode("c", q.Get("code_challenge"), map[string]any{"email": "bob@example.com", "email_verified": true,
		"groups": []string{"dev"}, "nonce": q.Get("nonce")})
	resp, _ = browser.Get(srv.URL + "/auth/callback?code=c&state=" + url.QueryEscape(q.Get("state")))
	if resp.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("callback = %d %s", resp.StatusCode, body)
	}

	var me map[string]any
	resp, _ = browser.Get(srv.URL + "/api/me")
	_ = json.NewDecoder(resp.Body).Decode(&me)
	if me["user"] != "bob@example.com" || me["authenticated"] != true {
		t.Errorf("/api/me = %v", me)
	}

	// Flux filtré : bob ne reçoit pas kube-system.
	u, _ := url.Parse(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, "ws://"+u.Host+"/api/stream", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {cookieHeader(jar, u)}}})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	_, data, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snap stream.Message
	_ = json.Unmarshal(data, &snap)
	if len(snap.Pods) != 1 || snap.Pods[0].Namespace != "production" {
		t.Errorf("snapshot de bob = %+v", snap.Pods)
	}

	// Revue d'accès : CSRF exigé, réponse de l'API server au nom de bob.
	body := `{"checks":[{"verb":"get","resource":"pods"},{"verb":"delete","resource":"pods","namespace":"production"}]}`
	resp, _ = browser.Post(srv.URL+"/api/access-review", "application/json", strings.NewReader(body))
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST sans en-tête CSRF = %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/access-review", strings.NewReader(body))
	req.Header.Set(auth.CSRFHeader, "1")
	resp, _ = browser.Do(req)
	var rev struct{ Results []access.Result }
	_ = json.NewDecoder(resp.Body).Decode(&rev)
	if len(rev.Results) != 2 || !rev.Results[0].Allowed || rev.Results[1].Allowed {
		t.Errorf("access-review = %d %+v", resp.StatusCode, rev)
	}
}

func cookieHeader(jar http.CookieJar, u *url.URL) string {
	var parts []string
	for _, c := range jar.Cookies(u) {
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func TestAccessReviewInDemoAllowsEverything(t *testing.T) {
	h, _ := newTest(static)
	req := httptest.NewRequest("POST", "/api/access-review", strings.NewReader(`{"checks":[{"verb":"delete","resource":"pods"}]}`))
	req.Header.Set(auth.CSRFHeader, "1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"allowed":true`) {
		t.Errorf("démo = %d %s", rec.Code, rec.Body)
	}
}
