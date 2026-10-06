// Package inspect définit ce que l'inspecteur demande au backend : chaîne de
// propriétaires, YAML d'un objet, événements et logs d'un pod. Deux
// implémentations : le cluster réel (internal/kube, au nom de l'utilisateur)
// et le simulateur (internal/demo).
package inspect

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/model"
)

// Ref désigne un objet Kubernetes. Group vaut "" pour le groupe core.
type Ref struct {
	Group     string `json:"group"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// Doc est le YAML d'un objet, nettoyé de ses managedFields.
type Doc struct {
	Ref
	YAML string          `json:"yaml"`
	Argo *model.ArgoInfo `json:"argocd,omitempty"`
}

type LogOptions struct {
	Container string
	Previous  bool
	Follow    bool
	TailLines int64
}

// Backend : chaque méthode agit avec les droits de l'utilisateur u.
type Backend interface {
	// Owners renvoie la chaîne du workload racine jusqu'au pod (Deployment, ReplicaSet, Pod).
	Owners(ctx context.Context, u access.User, namespace, pod string) ([]Ref, error)
	YAML(ctx context.Context, u access.User, ref Ref) (Doc, error)
	// Events renvoie les événements du pod, du plus récent au plus ancien.
	Events(ctx context.Context, u access.User, namespace, pod string) ([]model.Event, error)
	// Logs renvoie le flux brut de pods/log, lignes préfixées d'un horodatage RFC 3339.
	Logs(ctx context.Context, u access.User, namespace, pod string, o LogOptions) (io.ReadCloser, error)
}

// Kind lisible par l'onglet YAML.
type Kind struct {
	Group, Version, Kind, Resource string
}

// Kinds : objets dont l'inspecteur affiche le YAML.
var Kinds = []Kind{
	{"", "v1", "Pod", "pods"},
	{"apps", "v1", "Deployment", "deployments"},
	{"apps", "v1", "ReplicaSet", "replicasets"},
	{"apps", "v1", "StatefulSet", "statefulsets"},
	{"apps", "v1", "DaemonSet", "daemonsets"},
	{"batch", "v1", "Job", "jobs"},
}

// ErrUnsupportedKind : kind absent de Kinds.
var ErrUnsupportedKind = errors.New("type d'objet non pris en charge par l'inspecteur")

func Lookup(group, version, kind string) (Kind, error) {
	for _, k := range Kinds {
		if k.Group == group && k.Version == version && k.Kind == kind {
			return k, nil
		}
	}
	return Kind{}, ErrUnsupportedKind
}

// RefFor construit la Ref d'un kind connu.
func RefFor(kind, namespace, name string) Ref {
	for _, k := range Kinds {
		if k.Kind == kind {
			return Ref{Group: k.Group, Version: k.Version, Kind: kind, Namespace: namespace, Name: name}
		}
	}
	return Ref{Kind: kind, Namespace: namespace, Name: name}
}

// SortEvents trie du plus récent au plus ancien.
func SortEvents(evs []model.Event) {
	for i := 1; i < len(evs); i++ {
		for j := i; j > 0 && evs[j].LastSeen.After(evs[j-1].LastSeen); j-- {
			evs[j], evs[j-1] = evs[j-1], evs[j]
		}
	}
}

// LogTimeLayout : format des horodatages ajoutés par timestamps=true.
const LogTimeLayout = time.RFC3339Nano
