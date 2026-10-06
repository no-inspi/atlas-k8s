package kube

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appslisters "k8s.io/client-go/listers/apps/v1"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// RootOwner remonte au workload racine : Pod › ReplicaSet › Deployment, ou le
// contrôleur direct (StatefulSet, DaemonSet, Job…). Un pod sans contrôleur a un
// owner vide. Un ReplicaSet absent du cache reste la racine jusqu'à ce qu'il
// arrive (la source re-traite alors ses pods).
func RootOwner(pod *corev1.Pod, rs appslisters.ReplicaSetLister) model.OwnerRef {
	ref := metav1.GetControllerOf(pod)
	if ref == nil {
		return model.OwnerRef{}
	}
	if ref.Kind == "ReplicaSet" && rs != nil {
		if r, err := rs.ReplicaSets(pod.Namespace).Get(ref.Name); err == nil {
			if d := metav1.GetControllerOf(r); d != nil && d.Kind == "Deployment" {
				return model.OwnerRef{Kind: "Deployment", Name: d.Name}
			}
		}
	}
	return model.OwnerRef{Kind: ref.Kind, Name: ref.Name}
}
