package kube

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	appslisters "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

func controller(kind, name string) []metav1.OwnerReference {
	yes := true
	return []metav1.OwnerReference{{Kind: kind, Name: name, Controller: &yes, UID: types.UID("uid-" + name)}}
}

func TestRootOwner(t *testing.T) {
	idx := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	_ = idx.Add(&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "api-7f9", Namespace: "prod", OwnerReferences: controller("Deployment", "api")}})
	_ = idx.Add(&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: "lonely", Namespace: "prod"}})
	lister := appslisters.NewReplicaSetLister(idx)

	pod := func(owners []metav1.OwnerReference) *corev1.Pod {
		return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "prod", OwnerReferences: owners}}
	}
	cases := []struct {
		name string
		pod  *corev1.Pod
		want model.OwnerRef
	}{
		{"deployment via replicaset", pod(controller("ReplicaSet", "api-7f9")), model.OwnerRef{Kind: "Deployment", Name: "api"}},
		{"replicaset orphelin", pod(controller("ReplicaSet", "lonely")), model.OwnerRef{Kind: "ReplicaSet", Name: "lonely"}},
		{"replicaset pas encore en cache", pod(controller("ReplicaSet", "absent")), model.OwnerRef{Kind: "ReplicaSet", Name: "absent"}},
		{"statefulset", pod(controller("StatefulSet", "pg")), model.OwnerRef{Kind: "StatefulSet", Name: "pg"}},
		{"job", pod(controller("Job", "backup-123")), model.OwnerRef{Kind: "Job", Name: "backup-123"}},
		{"sans propriétaire", pod(nil), model.OwnerRef{}},
		{"référence non contrôleur ignorée", pod([]metav1.OwnerReference{{Kind: "ConfigMap", Name: "x"}}), model.OwnerRef{}},
	}
	for _, c := range cases {
		if got := RootOwner(c.pod, lister); got != c.want {
			t.Errorf("%s : %+v, attendu %+v", c.name, got, c.want)
		}
	}
}
