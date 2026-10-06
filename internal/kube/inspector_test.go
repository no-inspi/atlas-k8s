package kube

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/cluster-atlas/internal/access"
	"github.com/no-inspi/cluster-atlas/internal/inspect"
	"github.com/no-inspi/cluster-atlas/internal/model"
)

var bob = access.User{Name: "bob", Groups: []string{"oidc:dev"}}

func ownedPod(name string, owners []metav1.OwnerReference) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prod", OwnerReferences: owners}}
}

func TestInspectorOwners(t *testing.T) {
	yes := true
	ctl := func(kind, name string) []metav1.OwnerReference {
		return []metav1.OwnerReference{{Kind: kind, Name: name, Controller: &yes}}
	}
	client := fake.NewClientset(
		ownedPod("api-7f9-x", ctl("ReplicaSet", "api-7f9")),
		&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "api-7f9", Namespace: "prod", OwnerReferences: ctl("Deployment", "api")}},
		ownedPod("pg-0", ctl("StatefulSet", "pg")),
		ownedPod("lonely", nil),
	)
	in := NewInspector(access.Static{K: client}, nil, nil)
	ctx := context.Background()
	kinds := func(refs []inspect.Ref) string {
		var s []string
		for _, r := range refs {
			s = append(s, r.Group+"/"+r.Kind+"/"+r.Name)
		}
		return strings.Join(s, " › ")
	}
	cases := map[string]string{
		"api-7f9-x": "apps/Deployment/api › apps/ReplicaSet/api-7f9 › /Pod/api-7f9-x",
		"pg-0":      "apps/StatefulSet/pg › /Pod/pg-0",
		"lonely":    "/Pod/lonely",
	}
	for pod, want := range cases {
		refs, err := in.Owners(ctx, bob, "prod", pod)
		if err != nil || kinds(refs) != want {
			t.Errorf("%s : %q (%v), attendu %q", pod, kinds(refs), err, want)
		}
	}
}

func TestInspectorPassesForbiddenThrough(t *testing.T) {
	client := fake.NewClientset()
	client.PrependReactor("get", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "x", errors.New(`User "bob" cannot get resource "pods"`))
	})
	_, err := NewInspector(access.Static{K: client}, nil, nil).Owners(context.Background(), bob, "kube-system", "x")
	if !apierrors.IsForbidden(err) || !strings.Contains(err.Error(), `User "bob"`) {
		t.Errorf("erreur = %v", err)
	}
}

func TestInspectorYAML(t *testing.T) {
	dep := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "argocd-controller"}},
			Annotations:   map[string]string{"argocd.argoproj.io/tracking-id": "prod-apps:apps/Deployment:prod/api"}},
		Spec: appsv1.DeploymentSpec{Replicas: i32(3)},
	}
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme, dep)
	in := NewInspector(access.Static{D: dyn}, nil, nil)
	doc, err := in.YAML(context.Background(), bob, inspect.Ref{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "prod", Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(doc.YAML, "managedFields") || !strings.Contains(doc.YAML, "kind: Deployment") || !strings.Contains(doc.YAML, "replicas: 3") {
		t.Errorf("YAML =\n%s", doc.YAML)
	}
	if !strings.HasPrefix(doc.YAML, "apiVersion: apps/v1\nkind: Deployment\nmetadata:") {
		t.Errorf("ordre des clés inattendu :\n%s", doc.YAML)
	}
	if doc.Argo == nil || doc.Argo.Application != "prod-apps" {
		t.Errorf("ArgoCD = %+v", doc.Argo)
	}
	if _, err := in.YAML(context.Background(), bob, inspect.Ref{Group: "", Version: "v1", Kind: "Secret", Namespace: "prod", Name: "x"}); !errors.Is(err, inspect.ErrUnsupportedKind) {
		t.Errorf("Secret doit être refusé : %v", err)
	}
}

func TestInspectorLogs(t *testing.T) {
	client := fake.NewClientset(ownedPod("api", nil))
	rc, err := NewInspector(access.Static{K: client}, nil, nil).Logs(context.Background(), bob, "prod", "api", inspect.LogOptions{Follow: true, TailLines: 10})
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	if string(b) != "fake logs" {
		t.Errorf("logs = %q", b)
	}
}

type staticEvents []model.Event

func (s staticEvents) PodEvents(string, string) []model.Event { return s }

func TestInspectorEventsChecksRights(t *testing.T) {
	now := time.Now()
	evs := staticEvents{{Reason: "Scheduled", LastSeen: now.Add(-time.Minute)}, {Reason: "BackOff", Type: "Warning", LastSeen: now}}
	deny := func(context.Context, access.User, access.Attributes) bool { return false }
	allow := func(_ context.Context, _ access.User, a access.Attributes) bool {
		return a.Verb == "list" && a.Resource == "events" && a.Namespace == "prod"
	}
	if _, err := NewInspector(nil, evs, deny).Events(context.Background(), bob, "prod", "api"); !apierrors.IsForbidden(err) {
		t.Errorf("refus attendu : %v", err)
	}
	got, err := NewInspector(nil, evs, allow).Events(context.Background(), bob, "prod", "api")
	if err != nil || len(got) != 2 || got[0].Reason != "BackOff" {
		t.Errorf("événements = %+v (%v), le plus récent d'abord", got, err)
	}
}

func TestSourceIndexesPodEvents(t *testing.T) {
	ev := func(name, reason string, last time.Time) *corev1.Event {
		return &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "prod"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "prod", Name: "api"},
			Reason:         reason, Type: "Normal", Count: 2, LastTimestamp: metav1.NewTime(last), Source: corev1.EventSource{Component: "kubelet"}}
	}
	other := ev("o", "Other", time.Now())
	other.InvolvedObject.Name = "autre"
	_, sk := startSource(t, append(fixtures(), ev("e1", "Pulled", time.Now()), other)...)
	_ = sk
	src := lastSource
	eventually(t, "événements indexés", func() bool { return len(src.PodEvents("prod", "api")) == 1 })
	got := src.PodEvents("prod", "api")[0]
	if got.Reason != "Pulled" || got.Count != 2 || got.Source != "kubelet" {
		t.Errorf("événement = %+v", got)
	}
}
