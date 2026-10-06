package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/no-inspi/atlas-k8s/internal/audit"
	"github.com/no-inspi/atlas-k8s/internal/auth"
	"github.com/no-inspi/atlas-k8s/internal/demo"
	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

func actionServer(t *testing.T, features Features) (http.Handler, *bytes.Buffer, []model.Pod, []model.Node) {
	t.Helper()
	hub := stream.NewHub(stream.Options{})
	sim := demo.New(hub, demo.Options{Seed: 5, Now: time.Now()})
	hub.Flush()
	init, sub := hub.Subscribe(0)
	sub.Close()
	var buf bytes.Buffer
	h := New(Config{ClusterName: "demo", Demo: true, User: "demo", Static: static, Inspect: sim, Actions: sim,
		Audit: audit.New(&buf), Features: features}, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h, &buf, init[0].Pods, init[0].Nodes
}

var allFeatures = Features{ActionsEnabled: true, ExecEnabled: true, ExecDeniedNamespaces: []string{"kube-system"}}

func do(h http.Handler, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if csrf {
		req.Header.Set(auth.CSRFHeader, "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestActionsAreAudited(t *testing.T) {
	h, buf, pods, nodes := actionServer(t, allFeatures)
	var api model.Pod
	for _, p := range pods {
		if p.Owner.Name == "api-gateway" && p.Namespace == "production" {
			api = p
		}
	}
	cases := []struct {
		method, path, body string
		want               int
		verb               string
	}{
		{"DELETE", "/api/namespaces/production/pods/" + api.Name, "", 200, "delete"},
		{"PATCH", "/api/namespaces/production/deployments/frontend/scale", `{"replicas":3}`, 200, "scale"},
		{"POST", "/api/namespaces/production/deployments/frontend/restart", "", 200, "restart"},
		{"POST", "/api/nodes/" + nodes[0].Name + "/cordon", "", 200, "cordon"},
		{"POST", "/api/nodes/" + nodes[0].Name + "/uncordon", "", 200, "uncordon"},
		{"POST", "/api/nodes/" + nodes[1].Name + "/drain", "", 200, "drain"},
		{"DELETE", "/api/namespaces/production/pods/absent", "", 404, "delete"},
	}
	for _, c := range cases {
		if rec := do(h, c.method, c.path, c.body, true); rec.Code != c.want {
			t.Errorf("%s %s = %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != len(cases) {
		t.Fatalf("attendu %d lignes d'audit, reçu %d :\n%s", len(cases), len(lines), buf)
	}
	var first, last map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	_ = json.Unmarshal([]byte(lines[len(lines)-1]), &last)
	if first["verb"] != "delete" || first["resource"] != "pods" || first["name"] != api.Name || first["result"] != "success" || first["user"] != "demo" {
		t.Errorf("audit = %v", first)
	}
	if last["result"] != "failure" {
		t.Errorf("échec non audité : %v", last)
	}
}

func TestActionValidation(t *testing.T) {
	h, buf, _, nodes := actionServer(t, allFeatures)
	for _, c := range []struct{ method, path, body string }{
		{"PATCH", "/api/namespaces/production/deployments/frontend/scale", `{"replicas":-1}`},
		{"PATCH", "/api/namespaces/production/deployments/frontend/scale", `pas du json`},
		{"PATCH", "/api/namespaces/production/jobs/db-backup/scale", `{"replicas":2}`},
		{"POST", "/api/namespaces/production/jobs/db-backup/restart", ``},
	} {
		if rec := do(h, c.method, c.path, c.body, true); rec.Code != 400 {
			t.Errorf("%s %s %s = %d, attendu 400", c.method, c.path, c.body, rec.Code)
		}
	}
	if rec := do(h, "DELETE", "/api/namespaces/production/pods/x", "", false); rec.Code != http.StatusForbidden {
		t.Errorf("sans en-tête CSRF = %d", rec.Code)
	}
	rec := do(h, "POST", "/api/nodes/"+nodes[0].Name+"/drain?dryRun=true", "", true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"evict"`) {
		t.Errorf("plan de drain = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(buf.String(), `"verb":"drain"`) {
		t.Error("un plan de drain (dryRun) n'est pas une écriture : pas d'audit")
	}
}

func TestReadOnlyConsole(t *testing.T) {
	h, _, _, _ := actionServer(t, Features{})
	if rec := do(h, "DELETE", "/api/namespaces/production/pods/x", "", true); rec.Code != 404 {
		t.Errorf("actions désactivées : %d, attendu 404", rec.Code)
	}
	rec := get(h, "/api/me")
	if !strings.Contains(rec.Body.String(), `"actions":false`) || !strings.Contains(rec.Body.String(), `"exec":false`) {
		t.Errorf("/api/me = %s", rec.Body)
	}
}
