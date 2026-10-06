package demo

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// fakeSink garde l'état final et le journal des événements.
type fakeSink struct {
	nodes      map[string]model.Node
	pods       map[string]model.Pod
	workloads  map[string]model.Workload
	namespaces []string
	log        []string
	metrics    *model.Metrics
	statuses   map[string][]string // uid -> statuts successifs
}

func newSink() *fakeSink {
	return &fakeSink{nodes: map[string]model.Node{}, pods: map[string]model.Pod{},
		workloads: map[string]model.Workload{}, statuses: map[string][]string{}}
}

func (f *fakeSink) Upsert(kind stream.Kind, key string, obj any) {
	switch o := obj.(type) {
	case model.Node:
		f.nodes[key] = o
	case model.Pod:
		f.pods[key] = o
		st := f.statuses[key]
		if len(st) == 0 || st[len(st)-1] != o.DisplayStatus {
			f.statuses[key] = append(st, o.DisplayStatus)
		}
		f.log = append(f.log, fmt.Sprintf("upsert %s %s %s", o.Name, o.DisplayStatus, o.NodeName))
	case model.Workload:
		f.workloads[key] = o
	case model.Namespace:
		f.namespaces = append(f.namespaces, o.Name)
	}
}

func (f *fakeSink) Delete(kind stream.Kind, key string, obj any) {
	if kind == stream.KindPod {
		f.log = append(f.log, "delete "+obj.(model.Pod).Name)
		delete(f.pods, key)
	}
}

func (f *fakeSink) SetMetrics(m model.Metrics) { f.metrics = &m }

var t0 = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func start(seed uint64) (*Sim, *fakeSink) {
	sink := newSink()
	s := New(sink, Options{Seed: seed, Now: t0})
	return s, sink
}

// advance simule d seconds à pas de 200 ms, comme Run.
func advance(s *Sim, from time.Time, d time.Duration) time.Time {
	end := from.Add(d)
	t := from
	for t.Before(end) {
		t = t.Add(200 * time.Millisecond)
		s.Step(t)
	}
	return t
}

func podsOf(sink *fakeSink, ns, owner string) []model.Pod {
	var out []model.Pod
	for _, p := range sink.pods {
		if p.Namespace == ns && p.Owner.Name == owner {
			out = append(out, p)
		}
	}
	return out
}

func TestInitialClusterIsPlaced(t *testing.T) {
	s, sink := start(1)
	_ = s
	if len(sink.nodes) != 6 {
		t.Fatalf("attendu 6 nodes, reçu %d", len(sink.nodes))
	}
	api := podsOf(sink, "production", "api-gateway")
	if len(api) != 3 {
		t.Fatalf("api-gateway : %d pods", len(api))
	}
	seen := map[string]bool{}
	for _, p := range api {
		if p.DisplayStatus != "Running" || p.NodeName == "" {
			t.Errorf("%s : %s sur %q", p.Name, p.DisplayStatus, p.NodeName)
		}
		seen[p.NodeName] = true
	}
	if len(seen) != 3 {
		t.Errorf("les 3 replicas devraient être sur 3 nodes distincts : %v", seen)
	}
	for _, p := range sink.pods {
		if p.Owner.Kind == "Deployment" && p.Namespace == "production" && p.Owner.Name == "api-gateway" &&
			!strings.HasPrefix(p.Name, "api-gateway-") {
			t.Errorf("nom de pod inattendu %s", p.Name)
		}
	}
	if sink.workloads["Deployment/production/api-gateway"].Argo == nil {
		t.Error("api-gateway devrait porter l'information ArgoCD")
	}
	if len(sink.namespaces) != 5 {
		t.Errorf("namespaces publiés : %v", sink.namespaces)
	}
	if sink.metrics == nil || len(sink.metrics.Pods) == 0 {
		t.Error("métriques initiales absentes")
	}
}

func nodeByName(sink *fakeSink, name string) model.Node { return sink.nodes[name] }

func TestGPUIsolation(t *testing.T) {
	_, sink := start(2)
	ml := podsOf(sink, "production", "ml-inference")
	if len(ml) != 1 || nodeByName(sink, ml[0].NodeName).GPU == 0 {
		t.Fatalf("ml-inference doit tourner sur le node GPU : %+v", ml)
	}
	for _, p := range sink.pods {
		if p.NodeName == "" || p.Owner.Name == "ml-inference" || p.Owner.Kind == "DaemonSet" {
			continue
		}
		if nodeByName(sink, p.NodeName).GPU > 0 {
			t.Errorf("%s ne tolère pas le taint GPU mais tourne sur %s", p.Name, p.NodeName)
		}
	}
}

func TestDaemonSetHasOnePodPerNode(t *testing.T) {
	_, sink := start(3)
	perNode := map[string]int{}
	for _, p := range podsOf(sink, "monitoring", "node-exporter") {
		perNode[p.NodeName]++
	}
	if len(perNode) != len(sink.nodes) {
		t.Fatalf("node-exporter sur %d nodes sur %d", len(perNode), len(sink.nodes))
	}
	for n, c := range perNode {
		if c != 1 {
			t.Errorf("%s : %d pods node-exporter", n, c)
		}
	}
}

func TestStatefulSetOrdinals(t *testing.T) {
	_, sink := start(4)
	names := map[string]bool{}
	for _, p := range podsOf(sink, "production", "postgres-payments") {
		names[p.Name] = true
		if p.Owner.Kind != "StatefulSet" {
			t.Errorf("%s : owner %s", p.Name, p.Owner.Kind)
		}
	}
	if !names["postgres-payments-0"] || !names["postgres-payments-1"] {
		t.Errorf("ordinals attendus -0 et -1 : %v", names)
	}
}

func TestBrokenImageAndNotReady(t *testing.T) {
	s, sink := start(5)
	advance(s, t0, 10*time.Second)
	cp := podsOf(sink, "staging", "checkout-preview")
	if len(cp) != 1 || cp[0].DisplayStatus != "ImagePullBackOff" || cp[0].Ready {
		t.Fatalf("checkout-preview : %+v", cp)
	}
	notReady := 0
	for _, p := range podsOf(sink, "staging", "orders-service") {
		if p.DisplayStatus == "Running" && !p.Ready {
			notReady++
		}
	}
	if notReady != 1 {
		t.Errorf("attendu 1 pod orders-service Running non ready, reçu %d", notReady)
	}
}

func TestCrashLoopCycle(t *testing.T) {
	s, sink := start(6)
	var crashy model.Pod
	for _, p := range podsOf(sink, "production", "payment-worker") {
		if p.DisplayStatus == "CrashLoopBackOff" {
			crashy = p
		}
	}
	if crashy.UID == "" {
		t.Fatal("un pod payment-worker doit démarrer en CrashLoopBackOff")
	}
	before := crashy.Restarts
	advance(s, t0, 60*time.Second)
	after, ok := sink.pods[crashy.UID]
	if !ok {
		t.Fatal("le pod crashy a disparu")
	}
	if after.Restarts <= before {
		t.Errorf("redémarrages %d -> %d", before, after.Restarts)
	}
	seq := strings.Join(sink.statuses[crashy.UID], ",")
	for _, want := range []string{"Running", "Error", "CrashLoopBackOff"} {
		if !strings.Contains(seq, want) {
			t.Errorf("statut %s jamais vu : %s", want, seq)
		}
	}
}

func TestScaleBeyondCapacityLeavesPodsPending(t *testing.T) {
	s, sink := start(7)
	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "ml-inference", 3)
	advance(s, t0, 5*time.Second)
	pending := 0
	for _, p := range podsOf(sink, "production", "ml-inference") {
		if p.DisplayStatus == "Pending" {
			pending++
			if p.NodeName != "" || !strings.Contains(p.StatusMessage, "nodes are available") {
				t.Errorf("pod pending mal décrit : %+v", p)
			}
		}
	}
	if pending != 2 {
		t.Errorf("attendu 2 pods Pending (un seul GPU), reçu %d", pending)
	}
	if w := sink.workloads["Deployment/production/ml-inference"]; w.Replicas != 3 || w.ReadyReplicas != 1 {
		t.Errorf("workload = %+v", w)
	}

	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "ml-inference", 1)
	advance(s, t0.Add(5*time.Second), 3*time.Second)
	if n := len(podsOf(sink, "production", "ml-inference")); n != 1 {
		t.Errorf("après scale down : %d pods", n)
	}
}

func TestNewPodGoesThroughContainerCreating(t *testing.T) {
	s, sink := start(8)
	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "frontend", 3)
	advance(s, t0, 5*time.Second)
	var fresh string
	for uid, st := range sink.statuses {
		if sink.pods[uid].Owner.Name == "frontend" && len(st) > 1 {
			fresh = strings.Join(st, ",")
		}
	}
	if fresh != "Pending,ContainerCreating,Running" {
		t.Errorf("cycle d'un nouveau pod = %q", fresh)
	}
}

func TestJobCompletesThenDisappears(t *testing.T) {
	s, sink := start(9)
	advance(s, t0, 90*time.Second)
	completed := false
	for _, st := range sink.statuses {
		if strings.HasSuffix(strings.Join(st, ","), "Running,Completed") {
			completed = true
		}
	}
	if !completed {
		t.Fatal("aucun pod de Job n'est passé à Completed")
	}
	deleted := 0
	for _, l := range sink.log {
		if strings.HasPrefix(l, "delete db-backup-") {
			deleted++
		}
	}
	if deleted == 0 {
		t.Error("les pods de Job terminés devraient être supprimés après 30 s")
	}
}

func TestNodeRequestedTracksPods(t *testing.T) {
	_, sink := start(10)
	for name, n := range sink.nodes {
		var cpu int64
		for _, p := range sink.pods {
			if p.NodeName == name && p.DisplayStatus != "Completed" {
				cpu += p.Requests.CPU
			}
		}
		if n.Requested.CPU != cpu {
			t.Errorf("%s : requested %d, somme des pods %d", name, n.Requested.CPU, cpu)
		}
		if n.Requested.CPU > n.Allocatable.CPU {
			t.Errorf("%s surréservé", name)
		}
	}
}

func TestDeterministic(t *testing.T) {
	run := func() string {
		s, sink := start(42)
		advance(s, t0, 40*time.Second)
		return strings.Join(sink.log, "\n")
	}
	if run() != run() {
		t.Fatal("même seed et même horloge doivent produire la même séquence")
	}
}

func TestScaledCluster(t *testing.T) {
	sink := newSink()
	s := New(sink, Options{Seed: 1, Now: t0, Scale: Scale{Nodes: 100, PodsPerNode: 30}})
	advance(s, t0, 5*time.Second)
	if len(sink.nodes) != 100 {
		t.Fatalf("nodes = %d", len(sink.nodes))
	}
	if n := len(sink.pods); n < 2700 || n > 3300 {
		t.Errorf("pods = %d, attendu environ 3 000", n)
	}
	pending := 0
	for _, p := range sink.pods {
		if p.NodeName == "" {
			pending++
		}
	}
	if pending > 30 {
		t.Errorf("%d pods sans node : la capacité simulée doit suffire", pending)
	}
	if len(sink.namespaces) < 50 {
		t.Errorf("namespaces = %d", len(sink.namespaces))
	}
}
