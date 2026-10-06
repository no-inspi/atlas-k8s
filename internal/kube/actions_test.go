package kube

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/cluster-atlas/internal/access"
	"github.com/no-inspi/cluster-atlas/internal/actions"
)

func ctlRef(kind, name string) []metav1.OwnerReference {
	yes := true
	return []metav1.OwnerReference{{Kind: kind, Name: name, Controller: &yes}}
}

func nodePod(ns, name, node string, owner []metav1.OwnerReference, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, OwnerReferences: owner, Labels: labels},
		Spec: corev1.PodSpec{NodeName: node}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
}

func TestSimpleActions(t *testing.T) {
	client := fake.NewClientset(
		nodePod("prod", "api-1", "n1", nil, nil),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "api"}, Spec: appsv1.DeploymentSpec{Replicas: i32(3)}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
	)
	a := NewActions(access.Static{K: client})
	ctx := context.Background()

	if err := a.DeletePod(ctx, bob, "prod", "api-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CoreV1().Pods("prod").Get(ctx, "api-1", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("le pod devrait être supprimé : %v", err)
	}

	client.ClearActions()
	if err := a.Scale(ctx, bob, "prod", "Deployment", "api", 5); err != nil {
		t.Fatal(err)
	}
	patch := client.Actions()[0].(k8stesting.PatchAction)
	if patch.GetSubresource() != "scale" || string(patch.GetPatch()) != `{"spec":{"replicas":5}}` {
		t.Errorf("scale = %s %s", patch.GetSubresource(), patch.GetPatch())
	}

	if err := a.Restart(ctx, bob, "prod", "Deployment", "api"); err != nil {
		t.Fatal(err)
	}
	d, _ := client.AppsV1().Deployments("prod").Get(ctx, "api", metav1.GetOptions{})
	if _, err := time.Parse(time.RFC3339, d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]); err != nil {
		t.Errorf("annotation restartedAt = %v", d.Spec.Template.Annotations)
	}
	if err := a.Restart(ctx, bob, "prod", "Job", "x"); err == nil {
		t.Error("un Job ne se redémarre pas")
	}

	if err := a.SetUnschedulable(ctx, bob, "n1", true); err != nil {
		t.Fatal(err)
	}
	n, _ := client.CoreV1().Nodes().Get(ctx, "n1", metav1.GetOptions{})
	if !n.Spec.Unschedulable {
		t.Error("node non cordonné")
	}
}

func drainFixtures() *fake.Clientset {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "api-pdb"},
		Spec:       policyv1.PodDisruptionBudgetSpec{MinAvailable: &intstr.IntOrString{IntVal: 2}, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "api"}}},
		Status:     policyv1.PodDisruptionBudgetStatus{DisruptionsAllowed: 0},
	}
	mirror := nodePod("kube-system", "etcd-n1", "n1", ctlRef("Node", "n1"), nil)
	mirror.Annotations = map[string]string{"kubernetes.io/config.mirror": "abc"}
	withEmptyDir := nodePod("prod", "cache-1", "n1", ctlRef("ReplicaSet", "cache-7f"), nil)
	withEmptyDir.Spec.Volumes = []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	return fake.NewClientset(
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1"}},
		nodePod("prod", "api-1", "n1", ctlRef("ReplicaSet", "api-7f"), map[string]string{"app": "api"}),
		nodePod("prod", "web-1", "n1", ctlRef("ReplicaSet", "web-7f"), map[string]string{"app": "web"}),
		withEmptyDir,
		nodePod("monitoring", "node-exporter-x", "n1", ctlRef("DaemonSet", "node-exporter"), nil),
		mirror,
		nodePod("prod", "lonely", "n1", nil, nil),
		nodePod("prod", "elsewhere", "n2", ctlRef("ReplicaSet", "web-7f"), nil),
		pdb,
	)
}

func evictNames(refs []actions.PodRef) string {
	var s []string
	for _, r := range refs {
		s = append(s, r.Name)
	}
	return strings.Join(s, ",")
}

func ignoredNames(refs []actions.Ignored) string {
	var s []string
	for _, r := range refs {
		s = append(s, r.Name)
	}
	return strings.Join(s, ",")
}

func TestDrainPlan(t *testing.T) {
	a := NewActions(access.Static{K: drainFixtures()})
	plan, err := a.DrainPlan(context.Background(), bob, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got := evictNames(plan.Evict); got != "api-1,cache-1,web-1" {
		t.Errorf("à évincer = %s", got)
	}
	if got := ignoredNames(plan.Ignored); got != "etcd-n1,lonely,node-exporter-x" {
		t.Errorf("ignorés = %s", got)
	}
	reasons := map[string]string{}
	for _, i := range plan.Ignored {
		reasons[i.Name] = i.Reason
	}
	if !strings.Contains(reasons["node-exporter-x"], "DaemonSet") || !strings.Contains(reasons["etcd-n1"], "statique") || !strings.Contains(reasons["lonely"], "--force") {
		t.Errorf("raisons = %v", reasons)
	}
	if len(plan.Blocking) != 1 || plan.Blocking[0].Name != "api-pdb" || plan.Blocking[0].Pods[0] != "api-1" {
		t.Errorf("PDB bloquants = %+v", plan.Blocking)
	}
	for _, p := range plan.Evict {
		if p.Name == "cache-1" && !p.EmptyDir {
			t.Error("cache-1 a un emptyDir")
		}
	}
}

func TestDrainEvictsAndRespectsPDB(t *testing.T) {
	client := drainFixtures()
	var apiTries atomic.Int32
	client.PrependReactor("create", "pods", func(act k8stesting.Action) (bool, runtime.Object, error) {
		if act.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		ev := act.(k8stesting.CreateAction).GetObject().(*policyv1.Eviction)
		if ev.Name == "api-1" {
			apiTries.Add(1)
			return true, nil, apierrors.NewTooManyRequests("Cannot evict pod as it would violate the pod's disruption budget.", 0)
		}
		if ev.Name == "web-1" {
			return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods/eviction"}, "web-1", nil)
		}
		_ = client.Tracker().Delete(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, ev.Namespace, ev.Name)
		return true, nil, nil
	})
	a := NewActions(access.Static{K: client})
	a.evictionTimeout, a.retryEvery = 200*time.Millisecond, 50*time.Millisecond

	res, err := a.Drain(context.Background(), bob, "n1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range res.Evictions {
		got[e.Name] = e.Result
	}
	if got["cache-1"] != "evicted" || got["api-1"] != "blocked" || got["web-1"] != "error" {
		t.Errorf("évictions = %+v", res.Evictions)
	}
	if apiTries.Load() < 2 {
		t.Errorf("un refus de PDB (429) doit être réessayé : %d essai(s)", apiTries.Load())
	}
	if _, ok := got["node-exporter-x"]; ok {
		t.Error("les pods de DaemonSet ne sont pas évincés")
	}
	n, _ := client.CoreV1().Nodes().Get(context.Background(), "n1", metav1.GetOptions{})
	if !n.Spec.Unschedulable {
		t.Error("le drain doit cordonner le node d'abord")
	}
}

func TestDrainEvictsItselfLast(t *testing.T) {
	client := drainFixtures()
	var order []string
	var mu sync.Mutex
	client.PrependReactor("create", "pods", func(act k8stesting.Action) (bool, runtime.Object, error) {
		if act.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		mu.Lock()
		order = append(order, act.(k8stesting.CreateAction).GetObject().(*policyv1.Eviction).Name)
		mu.Unlock()
		return true, nil, nil
	})
	a := NewActions(access.Static{K: client}).WithSelf("prod", "web-1")
	a.selfDelay = 20 * time.Millisecond
	res, err := a.Drain(context.Background(), bob, "n1")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	evictedBeforeResponse := append([]string(nil), order...)
	mu.Unlock()
	for _, n := range evictedBeforeResponse {
		if n == "web-1" {
			t.Fatal("Cluster Atlas ne doit pas s'évincer avant d'avoir répondu")
		}
	}
	var self actions.Eviction
	for _, e := range res.Evictions {
		if e.Name == "web-1" {
			self = e
		}
	}
	if self.Result != "evicted" || !strings.Contains(self.Message, "Cluster Atlas") {
		t.Errorf("résultat pour Cluster Atlas = %+v", self)
	}
	eventually(t, "éviction différée de Cluster Atlas", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(order) > 0 && order[len(order)-1] == "web-1"
	})
}

func TestDrainPlanWarnsAboutPodsWithNowhereToGo(t *testing.T) {
	gpuPod := nodePod("prod", "ml-1", "gpu1", ctlRef("ReplicaSet", "ml-7f"), nil)
	gpuPod.Spec.NodeSelector = map[string]string{"pool": "gpu"}
	gpuPod.Spec.Tolerations = []corev1.Toleration{{Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule}}
	web := nodePod("prod", "web-1", "gpu1", ctlRef("ReplicaSet", "web-7f"), nil)
	gpuTaint := []corev1.Taint{{Key: "nvidia.com/gpu", Value: "present", Effect: corev1.TaintEffectNoSchedule}}
	client := fake.NewClientset(gpuPod, web,
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu1", Labels: map[string]string{"pool": "gpu"}}, Spec: corev1.NodeSpec{Taints: gpuTaint}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "gpu2", Labels: map[string]string{"pool": "gpu"}}, Spec: corev1.NodeSpec{Taints: gpuTaint, Unschedulable: true}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "std1", Labels: map[string]string{"pool": "std"}}},
	)
	plan, err := NewActions(access.Static{K: client}).DrainPlan(context.Background(), bob, "gpu1")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Stranded) != 1 || plan.Stranded[0].Name != "ml-1" || !strings.Contains(plan.Stranded[0].Reason, "Pending") {
		t.Errorf("stranded = %+v (web-1 peut aller sur std1, ml-1 n'a aucun node GPU disponible)", plan.Stranded)
	}
}

func TestTolerates(t *testing.T) {
	gpu := corev1.Taint{Key: "nvidia.com/gpu", Value: "present", Effect: corev1.TaintEffectNoSchedule}
	cases := []struct {
		tol  corev1.Toleration
		want bool
	}{
		{corev1.Toleration{Operator: corev1.TolerationOpExists}, true},
		{corev1.Toleration{Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists}, true},
		{corev1.Toleration{Key: "nvidia.com/gpu", Value: "present"}, true},
		{corev1.Toleration{Key: "nvidia.com/gpu", Value: "absent"}, false},
		{corev1.Toleration{Key: "nvidia.com/gpu", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute}, false},
		{corev1.Toleration{Key: "autre", Operator: corev1.TolerationOpExists}, false},
	}
	for _, c := range cases {
		if got := tolerates(c.tol, gpu); got != c.want {
			t.Errorf("%+v : %v, attendu %v", c.tol, got, c.want)
		}
	}
}
