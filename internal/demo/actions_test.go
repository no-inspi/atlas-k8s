package demo

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/no-inspi/cluster-atlas/internal/actions"
	"github.com/no-inspi/cluster-atlas/internal/inspect"
	"github.com/no-inspi/cluster-atlas/internal/model"
)

var _ actions.Backend = (*Sim)(nil)

func TestDemoDeletePodIsReplaced(t *testing.T) {
	s, sink := start(21)
	api := findPod(sink, "production", "api-gateway", "Running")
	if err := s.DeletePod(context.Background(), anyone, "production", api.Name); err != nil {
		t.Fatal(err)
	}
	advance(s, t0, 5*time.Second)
	if _, ok := sink.pods[api.UID]; ok {
		t.Error("le pod supprimé doit disparaître")
	}
	if n := len(podsOf(sink, "production", "api-gateway")); n != 3 {
		t.Errorf("le ReplicaSet doit le remplacer : %d pods", n)
	}
	if err := s.DeletePod(context.Background(), anyone, "production", "absent"); !apierrors.IsNotFound(err) {
		t.Errorf("pod absent : %v", err)
	}
}

func TestDemoScaleAndRestart(t *testing.T) {
	s, sink := start(22)
	ctx := context.Background()
	if err := s.Scale(ctx, anyone, "production", "Deployment", "frontend", 4); err != nil {
		t.Fatal(err)
	}
	if err := s.Scale(ctx, anyone, "production", "Job", "db-backup", 2); err == nil {
		t.Error("un Job ne se scale pas")
	}
	now := advance(s, t0, 5*time.Second)
	if n := len(podsOf(sink, "production", "frontend")); n != 4 {
		t.Fatalf("scale à 4 : %d pods", n)
	}

	before := map[string]bool{}
	for _, p := range podsOf(sink, "production", "frontend") {
		before[p.Name] = true
	}
	if err := s.Restart(ctx, anyone, "production", "Deployment", "frontend"); err != nil {
		t.Fatal(err)
	}
	advance(s, now, 30*time.Second)
	pods := podsOf(sink, "production", "frontend")
	if len(pods) != 4 {
		t.Fatalf("après restart : %d pods", len(pods))
	}
	for _, p := range pods {
		if before[p.Name] || p.DisplayStatus != "Running" {
			t.Errorf("tous les pods doivent avoir été remplacés : %s %s", p.Name, p.DisplayStatus)
		}
	}
	if !strings.HasPrefix(pods[0].Name, "frontend-") {
		t.Errorf("nom inattendu %s", pods[0].Name)
	}
}

func TestDemoRestartStatefulSetKeepsOrdinals(t *testing.T) {
	s, sink := start(23)
	uids := map[string]string{}
	for _, p := range podsOf(sink, "production", "postgres-payments") {
		uids[p.Name] = p.UID
	}
	_ = s.Restart(context.Background(), anyone, "production", "StatefulSet", "postgres-payments")
	advance(s, t0, 30*time.Second)
	for _, p := range podsOf(sink, "production", "postgres-payments") {
		if uids[p.Name] == "" || uids[p.Name] == p.UID {
			t.Errorf("%s doit être recréé sous le même nom", p.Name)
		}
	}
}

func TestDemoCordonAndDrain(t *testing.T) {
	s, sink := start(24)
	ctx := context.Background()
	var node string
	for name, n := range sink.nodes {
		if n.Pool == "spot-pool" {
			node = name
		}
	}
	plan, err := s.DrainPlan(ctx, anyone, node)
	if err != nil || len(plan.Evict) == 0 || len(plan.Ignored) != 1 || !strings.Contains(plan.Ignored[0].Reason, "DaemonSet") {
		t.Fatalf("plan = %+v (%v)", plan, err)
	}
	res, err := s.Drain(ctx, anyone, node)
	if err != nil || len(res.Evictions) != len(plan.Evict) || res.Evictions[0].Result != "evicted" {
		t.Fatalf("drain = %+v (%v)", res, err)
	}
	advance(s, t0, 10*time.Second)
	if !sink.nodes[node].Unschedulable {
		t.Error("le node drainé doit être cordonné")
	}
	for _, p := range sink.pods {
		if p.NodeName == node && p.Owner.Kind != "DaemonSet" {
			t.Errorf("%s est resté sur le node drainé", p.Name)
		}
	}
	if err := s.SetUnschedulable(ctx, anyone, node, false); err != nil {
		t.Fatal(err)
	}
	advance(s, t0.Add(10*time.Second), time.Second)
	if sink.nodes[node].Unschedulable {
		t.Error("uncordon sans effet")
	}
}

// Le pod qui crashe (ou qui n'est jamais prêt) garde son rôle à travers les
// remplacements : après sa suppression, un autre replica le reprend.
func TestDemoRolesSurviveReplacement(t *testing.T) {
	s, sink := start(25)
	ctx := context.Background()
	crashy := findPod(sink, "production", "payment-worker", "CrashLoopBackOff")
	notReady := model.Pod{}
	for _, p := range podsOf(sink, "staging", "orders-service") {
		if p.DisplayStatus == "Running" && !p.Ready {
			notReady = p
		}
	}
	_ = s.DeletePod(ctx, anyone, "production", crashy.Name)
	_ = s.DeletePod(ctx, anyone, "staging", notReady.Name)
	advance(s, t0, 40*time.Second)

	var heir model.Pod
	for _, p := range podsOf(sink, "production", "payment-worker") {
		if p.Restarts > 0 {
			heir = p
		}
	}
	if heir.UID == "" {
		t.Fatal("un replica de payment-worker doit reprendre le rôle de pod qui crashe")
	}
	rc, err := s.Logs(ctx, anyone, "production", heir.Name, inspect.LogOptions{Previous: true})
	if err != nil {
		t.Fatal(err)
	}
	if prev, _ := io.ReadAll(rc); !strings.Contains(string(prev), "connection refused") {
		t.Errorf("l'instance précédente de l'héritier doit montrer le crash :\n%s", prev)
	}
	unready := 0
	for _, p := range podsOf(sink, "staging", "orders-service") {
		if p.DisplayStatus == "Running" && !p.Ready {
			unready++
		}
	}
	if unready != 1 {
		t.Errorf("attendu 1 orders-service non ready après remplacement, reçu %d", unready)
	}
}

func TestDemoDrainPlanFlagsGPUPods(t *testing.T) {
	s, sink := start(26)
	var gpu string
	for name, n := range sink.nodes {
		if n.GPU > 0 {
			gpu = name
		}
	}
	plan, _ := s.DrainPlan(context.Background(), anyone, gpu)
	if len(plan.Stranded) != 1 || !strings.HasPrefix(plan.Stranded[0].Name, "ml-inference-") {
		t.Errorf("stranded = %+v", plan.Stranded)
	}
}
