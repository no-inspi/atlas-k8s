// Package actions définit les six actions d'exploitation de la spec. Chaque
// action est faite au nom de l'utilisateur (impersonation) : c'est l'API server
// qui accepte ou refuse, son message d'erreur est rendu tel quel.
package actions

import (
	"context"
	"errors"

	"github.com/no-inspi/cluster-atlas/internal/access"
)

type PodRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Owner : workload racine, pour l'affichage (« Deployment api »).
	Owner string `json:"owner,omitempty"`
	// EmptyDir : les données emptyDir du pod seront perdues.
	EmptyDir bool `json:"emptyDir,omitempty"`
}

type Ignored struct {
	PodRef
	Reason string `json:"reason"`
}

// Blocking : PodDisruptionBudget qui refusera (au moins une partie) des évictions.
type Blocking struct {
	Namespace          string   `json:"namespace"`
	Name               string   `json:"name"`
	DisruptionsAllowed int32    `json:"disruptionsAllowed"`
	Pods               []string `json:"pods"`
}

// DrainPlan est le récapitulatif affiché avant confirmation.
type DrainPlan struct {
	Node     string     `json:"node"`
	Evict    []PodRef   `json:"evict"`
	Ignored  []Ignored  `json:"ignored"`
	Blocking []Blocking `json:"blocking"`
	// PDBUnknown explique pourquoi les PDB n'ont pas pu être lus.
	PDBUnknown string `json:"pdbUnknown,omitempty"`
}

type Eviction struct {
	PodRef
	Result  string `json:"result"` // evicted | blocked | error
	Message string `json:"message,omitempty"`
}

type DrainResult struct {
	Node      string     `json:"node"`
	Evictions []Eviction `json:"evictions"`
	Ignored   []Ignored  `json:"ignored"`
}

type Backend interface {
	DeletePod(ctx context.Context, u access.User, namespace, name string) error
	// Scale : Deployment ou StatefulSet.
	Scale(ctx context.Context, u access.User, namespace, kind, name string, replicas int32) error
	// Restart : Deployment, StatefulSet ou DaemonSet (annotation restartedAt).
	Restart(ctx context.Context, u access.User, namespace, kind, name string) error
	// SetUnschedulable : cordon (true) ou uncordon (false).
	SetUnschedulable(ctx context.Context, u access.User, node string, unschedulable bool) error
	DrainPlan(ctx context.Context, u access.User, node string) (DrainPlan, error)
	// Drain cordonne le node puis évince ses pods par l'API Eviction.
	Drain(ctx context.Context, u access.User, node string) (DrainResult, error)
}

var ErrUnsupportedKind = errors.New("action non prise en charge pour ce type de workload")

// Kinds acceptés dans les URL (ressource au pluriel) pour chaque action.
var (
	ScalableKinds    = map[string]string{"deployments": "Deployment", "statefulsets": "StatefulSet"}
	RestartableKinds = map[string]string{"deployments": "Deployment", "statefulsets": "StatefulSet", "daemonsets": "DaemonSet"}
)
