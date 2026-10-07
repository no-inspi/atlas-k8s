package demo

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/inspect"
	"github.com/no-inspi/atlas-k8s/internal/model"
)

var anyone = access.User{Name: "demo"}

func findPod(sink *fakeSink, ns, owner, status string) model.Pod {
	for _, p := range sink.pods {
		if p.Namespace == ns && p.Owner.Name == owner && (status == "" || p.DisplayStatus == status) {
			return p
		}
	}
	return model.Pod{}
}

func TestDemoOwners(t *testing.T) {
	s, sink := start(11)
	api := findPod(sink, "production", "api-gateway", "")
	refs, err := s.Owners(context.Background(), anyone, "production", api.Name)
	if err != nil || len(refs) != 3 || refs[0].Kind != "Deployment" || refs[1].Kind != "ReplicaSet" || refs[2].Name != api.Name {
		t.Fatalf("chaîne = %+v (%v)", refs, err)
	}
	if !strings.HasPrefix(api.Name, refs[1].Name+"-") {
		t.Errorf("le ReplicaSet %q doit préfixer le pod %q", refs[1].Name, api.Name)
	}
	pg, _ := s.Owners(context.Background(), anyone, "production", "postgres-payments-0")
	if len(pg) != 2 || pg[0].Kind != "StatefulSet" {
		t.Errorf("chaîne StatefulSet = %+v", pg)
	}
	if _, err := s.Owners(context.Background(), anyone, "production", "absent"); !apierrors.IsNotFound(err) {
		t.Errorf("pod absent : %v", err)
	}
}

func TestDemoYAML(t *testing.T) {
	s, sink := start(12)
	doc, err := s.YAML(context.Background(), anyone, inspect.RefFor("Deployment", "production", "api-gateway"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"apiVersion: apps/v1", "kind: Deployment", "replicas: 3", "image: europe-west1-docker.pkg.dev/prod/apps/api-gateway:2.14.1", "status:"} {
		if !strings.Contains(doc.YAML, want) {
			t.Errorf("YAML sans %q :\n%s", want, doc.YAML)
		}
	}
	if strings.Contains(doc.YAML, "managedFields") || doc.Argo == nil || doc.Argo.Application != "production-apps" {
		t.Errorf("ArgoCD = %+v", doc.Argo)
	}
	api := findPod(sink, "production", "api-gateway", "")
	pod, err := s.YAML(context.Background(), anyone, inspect.RefFor("Pod", "production", api.Name))
	if err != nil || !strings.Contains(pod.YAML, "kind: Pod") || !strings.Contains(pod.YAML, "nodeName: "+api.NodeName) {
		t.Errorf("YAML du pod (%v) :\n%s", err, pod.YAML)
	}
	if _, err := s.YAML(context.Background(), anyone, inspect.RefFor("Deployment", "production", "absent")); !apierrors.IsNotFound(err) {
		t.Errorf("objet absent : %v", err)
	}
}

func TestDemoEvents(t *testing.T) {
	s, sink := start(13)
	crashy := findPod(sink, "production", "payment-worker", "CrashLoopBackOff")
	evs, _ := s.Events(context.Background(), anyone, "Pod", "production", crashy.Name)
	reasons := map[string]bool{}
	for _, e := range evs {
		reasons[e.Reason] = true
	}
	for _, r := range []string{"Scheduled", "Started", "BackOff"} {
		if !reasons[r] {
			t.Errorf("événement %s absent : %+v", r, evs)
		}
	}
	if len(evs) > 1 && evs[0].LastSeen.Before(evs[len(evs)-1].LastSeen) {
		t.Error("le plus récent doit venir en premier")
	}

	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "ml-inference", 2)
	advance(s, t0, 3*time.Second)
	var pending model.Pod
	for _, p := range sink.pods {
		if p.Owner.Name == "ml-inference" && p.DisplayStatus == "Pending" {
			pending = p
		}
	}
	evs, _ = s.Events(context.Background(), anyone, "Pod", "production", pending.Name)
	if len(evs) == 0 || evs[0].Reason != "FailedScheduling" || evs[0].Type != "Warning" {
		t.Errorf("FailedScheduling attendu : %+v", evs)
	}
}

func readLines(t *testing.T, rc io.ReadCloser, n int) []string {
	t.Helper()
	sc := bufio.NewScanner(rc)
	var out []string
	for len(out) < n && sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

func TestDemoLogs(t *testing.T) {
	s, sink := start(14)
	api := findPod(sink, "production", "api-gateway", "Running")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rc, err := s.Logs(ctx, anyone, "production", api.Name, inspect.LogOptions{TailLines: 5})
	if err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, rc, 100)
	rc.Close()
	if len(lines) != 5 {
		t.Fatalf("tailLines=5 : %d lignes", len(lines))
	}
	ts, _, ok := strings.Cut(lines[0], " ")
	if _, err := time.Parse(inspect.LogTimeLayout, ts); !ok || err != nil {
		t.Errorf("ligne sans horodatage RFC 3339 : %q", lines[0])
	}

	follow, _ := s.Logs(ctx, anyone, "production", api.Name, inspect.LogOptions{TailLines: 1, Follow: true})
	go func() {
		for now := t0; ; now = now.Add(time.Second) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Millisecond):
				s.Step(now)
			}
		}
	}()
	if got := readLines(t, follow, 3); len(got) != 3 {
		t.Errorf("suivi : %d lignes reçues", len(got))
	}
}

func TestDemoPreviousLogs(t *testing.T) {
	s, sink := start(15)
	crashy := findPod(sink, "production", "payment-worker", "CrashLoopBackOff")
	rc, err := s.Logs(context.Background(), anyone, "production", crashy.Name, inspect.LogOptions{Previous: true})
	if err != nil {
		t.Fatal(err)
	}
	all, _ := io.ReadAll(rc)
	if !strings.Contains(string(all), "connection refused") || !strings.Contains(string(all), "FATAL") {
		t.Errorf("les logs précédents doivent montrer le crash :\n%s", all)
	}
	api := findPod(sink, "production", "api-gateway", "Running")
	if _, err := s.Logs(context.Background(), anyone, "production", api.Name, inspect.LogOptions{Previous: true}); !apierrors.IsBadRequest(err) {
		t.Errorf("pas d'instance précédente : %v", err)
	}
	if _, err := s.Logs(context.Background(), anyone, "production", api.Name, inspect.LogOptions{Container: "nope"}); !apierrors.IsBadRequest(err) {
		t.Errorf("container inconnu : %v", err)
	}
}

var _ inspect.Backend = (*Sim)(nil)

func TestDemoNetworkInspector(t *testing.T) {
	s, _ := start(5)
	ctx := context.Background()
	yamlOf := func(r inspect.Ref) string {
		t.Helper()
		doc, err := s.YAML(ctx, anyone, r)
		if err != nil {
			t.Fatalf("%+v : %v", r, err)
		}
		return doc.YAML
	}
	if y := yamlOf(inspect.Ref{Version: "v1", Kind: "Service", Namespace: "production", Name: "api-gateway"}); !strings.Contains(y, "kind: Service") || !strings.Contains(y, "type: LoadBalancer") {
		t.Errorf("Service :\n%s", y)
	}
	if y := yamlOf(inspect.Ref{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress", Namespace: "production", Name: "storefront"}); !strings.Contains(y, "ingressClassName: nginx") {
		t.Errorf("Ingress :\n%s", y)
	}
	if y := yamlOf(inspect.Ref{Group: "traefik.io", Version: "v1alpha1", Kind: "IngressRoute", Namespace: "monitoring", Name: "grafana"}); !strings.Contains(y, "Host(`grafana.example.com`)") {
		t.Errorf("IngressRoute :\n%s", y)
	}
	if y := yamlOf(inspect.Ref{Version: "v1", Kind: "PersistentVolumeClaim", Namespace: "staging", Name: "uploads-preview"}); !strings.Contains(y, "phase: Pending") {
		t.Errorf("PVC :\n%s", y)
	}
	if y := yamlOf(inspect.Ref{Version: "v1", Kind: "Service", Namespace: "kube-system", Name: "kube-dns"}); !strings.Contains(y, "app.kubernetes.io/name: coredns") {
		t.Errorf("le sélecteur vise le workload, pas le Service :\n%s", y)
	}
	if _, err := s.YAML(ctx, anyone, inspect.Ref{Group: "traefik.containo.us", Version: "v1alpha1", Kind: "IngressRoute", Namespace: "monitoring", Name: "grafana"}); err == nil {
		t.Error("IngressRoute d'un autre groupe : erreur attendue")
	}
	if _, err := s.YAML(ctx, anyone, inspect.Ref{Version: "v1", Kind: "Service", Namespace: "production", Name: "absent"}); err == nil {
		t.Error("Service absent : erreur attendue")
	}
	evs, _ := s.Events(ctx, anyone, "PersistentVolumeClaim", "staging", "uploads-preview")
	if len(evs) != 1 || evs[0].Reason != "WaitForFirstConsumer" {
		t.Errorf("événements du PVC en attente = %+v", evs)
	}
}
