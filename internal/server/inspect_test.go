package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/no-inspi/atlas-k8s/internal/demo"
	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// Les routes de l'inspecteur, servies par le simulateur (mode démo).
func demoServer(t *testing.T) (http.Handler, model.Pod) {
	t.Helper()
	hub := stream.NewHub(stream.Options{})
	sim := demo.New(hub, demo.Options{Seed: 3, Now: time.Now()})
	hub.Flush()
	init, sub := hub.Subscribe(0)
	sub.Close()
	var api model.Pod
	for _, p := range init[0].Pods {
		if p.Owner.Name == "api-gateway" && p.Namespace == "production" {
			api = p
		}
	}
	h := New(Config{ClusterName: "demo", Demo: true, User: "demo", Static: static, Inspect: sim}, hub, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return h, api
}

func TestInspectorRoutes(t *testing.T) {
	h, api := demoServer(t)

	rec := get(h, "/api/namespaces/production/pods/"+api.Name+"/owner")
	var owner struct {
		Chain []struct{ Kind, Name, Group string }
	}
	_ = json.NewDecoder(rec.Body).Decode(&owner)
	if rec.Code != 200 || len(owner.Chain) != 3 || owner.Chain[0].Kind != "Deployment" || owner.Chain[0].Group != "apps" {
		t.Errorf("owner = %d %+v", rec.Code, owner)
	}

	rec = get(h, "/api/yaml/apps/v1/Deployment/production/api-gateway")
	var doc struct {
		YAML   string
		Argocd *model.ArgoInfo
	}
	_ = json.NewDecoder(rec.Body).Decode(&doc)
	if rec.Code != 200 || !strings.Contains(doc.YAML, "kind: Deployment") || doc.Argocd == nil {
		t.Errorf("yaml = %d %+v", rec.Code, doc)
	}
	if rec := get(h, "/api/yaml/core/v1/Pod/production/"+api.Name); rec.Code != 200 || !strings.Contains(rec.Body.String(), "kind: Pod") {
		t.Errorf("yaml du pod (groupe core) = %d", rec.Code)
	}
	if rec := get(h, "/api/yaml/core/v1/Secret/production/x"); rec.Code != 400 {
		t.Errorf("Secret = %d, attendu 400", rec.Code)
	}
	if rec := get(h, "/api/yaml/apps/v1/Deployment/production/absent"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("absent = %d %s", rec.Code, rec.Body)
	}

	rec = get(h, "/api/namespaces/production/pods/"+api.Name+"/events")
	var evs struct{ Events []model.Event }
	_ = json.NewDecoder(rec.Body).Decode(&evs)
	if rec.Code != 200 || len(evs.Events) == 0 {
		t.Errorf("events = %d %+v", rec.Code, evs)
	}
}
