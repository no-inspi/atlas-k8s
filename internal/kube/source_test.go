package kube

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

type sink struct {
	mu      sync.Mutex
	objs    map[stream.Kind]map[string]any
	deleted []string
	ready   bool
}

func newSink() *sink { return &sink{objs: map[stream.Kind]map[string]any{}} }

func (s *sink) Upsert(k stream.Kind, key string, obj any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objs[k] == nil {
		s.objs[k] = map[string]any{}
	}
	s.objs[k][key] = obj
}

func (s *sink) Delete(k stream.Kind, key string, _ any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objs[k], key)
	s.deleted = append(s.deleted, string(k)+"/"+key)
}

func (s *sink) MarkReady() { s.mu.Lock(); s.ready = true; s.mu.Unlock() }

func (s *sink) get(k stream.Kind, key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objs[k][key]
	return o, ok
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("délai dépassé : %s", what)
}

func fixtures() []runtime.Object {
	yes := true
	ctl := func(kind, name string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{Kind: kind, Name: name, UID: types.UID(name), Controller: &yes}}
	}
	return []runtime.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod", Annotations: map[string]string{"atlas.io/color": "#3D6FB6"}}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{"cloud.google.com/gke-nodepool": "default-pool"}},
			Status: corev1.NodeStatus{Allocatable: rl("4", "8Gi"), Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod", UID: "api"}, Spec: appsv1.DeploymentSpec{Replicas: i32(1)}},
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "api-7f9", Namespace: "prod", UID: "api-7f9", OwnerReferences: ctl("Deployment", "api")}},
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "lonely", Namespace: "prod", UID: "lonely"}},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "api-7f9-x", Namespace: "prod", UID: "pod-1", OwnerReferences: ctl("ReplicaSet", "api-7f9"),
				ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "kubectl"}}},
			Spec: corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "api", Image: "api:1",
				Resources: corev1.ResourceRequirements{Requests: rl("250m", "256Mi")}}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	}
}

// lastSource : source démarrée par le dernier startSource (tests des événements).
var lastSource *Source

func startSource(t *testing.T, objs ...runtime.Object) (*fake.Clientset, *sink) {
	t.Helper()
	return startSourceWith(t, fake.NewClientset(objs...), Options{})
}

// startSourceWith démarre une source sur un client préparé (réacteurs, découverte).
func startSourceWith(t *testing.T, client *fake.Clientset, opts Options) (*fake.Clientset, *sink) {
	t.Helper()
	sk := newSink()
	opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	opts.ReconcileInterval = 10 * time.Millisecond
	src := NewSource(client, sk, opts)
	lastSource = src
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = src.Run(ctx) }()
	eventually(t, "source prête", func() bool { sk.mu.Lock(); defer sk.mu.Unlock(); return sk.ready })
	return client, sk
}

func TestInitialStateIsPublishedBeforeReady(t *testing.T) {
	_, sk := startSource(t, fixtures()...)
	o, ok := sk.get(stream.KindPod, "pod-1")
	if !ok {
		t.Fatal("pod absent de l'état initial")
	}
	if p := o.(model.Pod); p.Owner != (model.OwnerRef{Kind: "Deployment", Name: "api"}) || p.DisplayStatus != "Running" {
		t.Errorf("pod = %+v", p)
	}
	n, _ := sk.get(stream.KindNode, "n1")
	if n.(model.Node).Requested.CPU != 250 || n.(model.Node).Pool != "default-pool" {
		t.Errorf("node = %+v", n)
	}
	if _, ok := sk.get(stream.KindWorkload, "Deployment/prod/api"); !ok {
		t.Error("Deployment absent")
	}
	if _, ok := sk.get(stream.KindWorkload, "ReplicaSet/prod/api-7f9"); ok {
		t.Error("un ReplicaSet géré par un Deployment ne doit pas être publié")
	}
	if _, ok := sk.get(stream.KindWorkload, "ReplicaSet/prod/lonely"); !ok {
		t.Error("ReplicaSet orphelin absent")
	}
	if ns, _ := sk.get(stream.KindNamespace, "prod"); ns.(model.Namespace).Color != "#3D6FB6" {
		t.Errorf("namespace = %+v", ns)
	}
}

func TestLiveChanges(t *testing.T) {
	client, sk := startSource(t, fixtures()...)
	ctx := context.Background()

	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "extra", Namespace: "prod", UID: "pod-2"},
		Spec:   corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "c", Resources: corev1.ResourceRequirements{Requests: rl("500m", "")}}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending}}
	if _, err := client.CoreV1().Pods("prod").Create(ctx, p, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "pod créé et node mis à jour", func() bool {
		_, ok := sk.get(stream.KindPod, "pod-2")
		n, _ := sk.get(stream.KindNode, "n1")
		return ok && n.(model.Node).Requested.CPU == 750
	})

	if err := client.CoreV1().Pods("prod").Delete(ctx, "extra", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "pod supprimé", func() bool {
		_, ok := sk.get(stream.KindPod, "pod-2")
		n, _ := sk.get(stream.KindNode, "n1")
		return !ok && n.(model.Node).Requested.CPU == 250
	})

	n, _ := client.CoreV1().Nodes().Get(ctx, "n1", metav1.GetOptions{})
	n.Spec.Unschedulable = true
	if _, err := client.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "cordon visible", func() bool {
		n, _ := sk.get(stream.KindNode, "n1")
		return n.(model.Node).Unschedulable
	})
}

func TestReplicaSetArrivingLaterFixesOwner(t *testing.T) {
	objs := fixtures()
	// Retire le ReplicaSet api-7f9 de l'état initial : le pod pointe vers un RS inconnu.
	objs = append(objs[:3], objs[4:]...)
	client, sk := startSource(t, objs...)
	o, _ := sk.get(stream.KindPod, "pod-1")
	if o.(model.Pod).Owner.Kind != "ReplicaSet" {
		t.Fatalf("owner initial = %+v", o.(model.Pod).Owner)
	}
	yes := true
	rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "api-7f9", Namespace: "prod", UID: "api-7f9",
		OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "api", UID: "api", Controller: &yes}}}}
	if _, err := client.AppsV1().ReplicaSets("prod").Create(context.Background(), rs, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "owner résolu en Deployment", func() bool {
		o, _ := sk.get(stream.KindPod, "pod-1")
		return o.(model.Pod).Owner.Kind == "Deployment"
	})
}

func TestTransformStripsUnusedFields(t *testing.T) {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "x"}},
			Annotations: map[string]string{"kubectl.kubernetes.io/last-applied-configuration": "{}", "keep": "1"}},
		Spec: corev1.PodSpec{Volumes: []corev1.Volume{{Name: "v"}}, Containers: []corev1.Container{{Name: "c", Image: "i",
			Env: []corev1.EnvVar{{Name: "SECRET", Value: "x"}}, Command: []string{"run"}}}},
	}
	out, err := transform(p)
	if err != nil {
		t.Fatal(err)
	}
	q := out.(*corev1.Pod)
	if q.ManagedFields != nil || q.Annotations["kubectl.kubernetes.io/last-applied-configuration"] != "" || q.Annotations["keep"] != "1" {
		t.Errorf("métadonnées = %+v", q.ObjectMeta)
	}
	if q.Spec.Volumes != nil || q.Spec.Containers[0].Env != nil || q.Spec.Containers[0].Command != nil || q.Spec.Containers[0].Image != "i" {
		t.Errorf("spec = %+v", q.Spec)
	}
	withClaims, _ := transform(&corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{
		{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "c", ReadOnly: true}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{Ephemeral: &corev1.EphemeralVolumeSource{VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{}}}},
		{Name: "secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "s"}}},
	}}})
	vs := withClaims.(*corev1.Pod).Spec.Volumes
	if len(vs) != 2 || vs[0].PersistentVolumeClaim.ClaimName != "c" || vs[0].PersistentVolumeClaim.ReadOnly || vs[1].Ephemeral.VolumeClaimTemplate != nil {
		t.Errorf("volumes gardés = %+v", vs)
	}
	d, _ := transform(&appsv1.Deployment{Spec: appsv1.DeploymentSpec{Replicas: i32(2), Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}}}}})
	if dd := d.(*appsv1.Deployment); dd.Spec.Template.Spec.Containers != nil || *dd.Spec.Replicas != 2 {
		t.Errorf("deployment = %+v", dd.Spec)
	}
	u := &unstructured.Unstructured{Object: map[string]any{"metadata": map[string]any{"name": "r",
		"managedFields": []any{map[string]any{"manager": "x"}},
		"annotations":   map[string]any{corev1.LastAppliedConfigAnnotation: "{}", "keep": "1"}}}}
	uo, _ := transform(u)
	if a := uo.(*unstructured.Unstructured).GetAnnotations(); a[corev1.LastAppliedConfigAnnotation] != "" || a["keep"] != "1" {
		t.Errorf("annotations unstructured = %v", a)
	}
	if mf := uo.(*unstructured.Unstructured).GetManagedFields(); mf != nil {
		t.Errorf("managedFields unstructured = %v", mf)
	}
}
