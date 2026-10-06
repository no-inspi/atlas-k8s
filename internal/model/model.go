// Package model définit le modèle réduit envoyé au front : seulement les champs
// dont la vue et l'inspecteur ont besoin, jamais l'objet Kubernetes complet.
// CPU en millicores, mémoire en octets.
package model

import "time"

// Resources regroupe des quantités CPU (millicores), mémoire (octets) et pods.
type Resources struct {
	CPU    int64 `json:"cpu"`
	Memory int64 `json:"memory"`
	Pods   int64 `json:"pods,omitempty"`
}

type Condition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type Taint struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}

type Node struct {
	Name           string      `json:"name"`
	Pool           string      `json:"pool"`
	InstanceType   string      `json:"instanceType"`
	Zone           string      `json:"zone"`
	Spot           bool        `json:"spot"`
	GPU            int         `json:"gpu"`
	Allocatable    Resources   `json:"allocatable"`
	Requested      Resources   `json:"requested"`
	Conditions     []Condition `json:"conditions"`
	Taints         []Taint     `json:"taints"`
	Unschedulable  bool        `json:"unschedulable"`
	KubeletVersion string      `json:"kubeletVersion"`
	CreatedAt      time.Time   `json:"createdAt"`
}

type ContainerStatus struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Ready    bool   `json:"ready"`
	State    string `json:"state"` // waiting | running | terminated
	Reason   string `json:"reason,omitempty"`
	Restarts int32  `json:"restarts"`
	Init     bool   `json:"init,omitempty"`
}

// OwnerRef désigne le workload racine du pod (Deployment plutôt que ReplicaSet).
type OwnerRef struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

type Pod struct {
	UID       string `json:"uid"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	NodeName  string `json:"nodeName"`
	Phase     string `json:"phase"`
	// DisplayStatus est le statut tel que l'afficherait `kubectl get pods`.
	DisplayStatus string `json:"displayStatus"`
	// StatusMessage explique un état bloqué, par exemple le message de la
	// condition PodScheduled=False (« 0/6 nodes are available: … »).
	StatusMessage string            `json:"statusMessage,omitempty"`
	Ready         bool              `json:"ready"`
	Restarts      int32             `json:"restarts"`
	Containers    []ContainerStatus `json:"containers"`
	Owner         OwnerRef          `json:"owner"`
	Requests      Resources         `json:"requests"`
	Limits        Resources         `json:"limits"`
	PodIP         string            `json:"podIP"`
	CreatedAt     time.Time         `json:"createdAt"`
	QOSClass      string            `json:"qosClass"`
}

type ArgoInfo struct {
	Application string `json:"application"`
	SyncStatus  string `json:"syncStatus"`
}

type Workload struct {
	Kind          string    `json:"kind"`
	Name          string    `json:"name"`
	Namespace     string    `json:"namespace"`
	Replicas      int32     `json:"replicas"`
	ReadyReplicas int32     `json:"readyReplicas"`
	Argo          *ArgoInfo `json:"argocd,omitempty"`
}

// Namespace porte la couleur choisie par l'annotation atlas.io/color, si elle existe.
type Namespace struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// Usage est un instantané metrics-server.
type Usage struct {
	CPU    int64 `json:"cpu"`
	Memory int64 `json:"memory"`
}

// Metrics est indexé par UID de pod et par nom de node.
type Metrics struct {
	Pods  map[string]Usage `json:"pods"`
	Nodes map[string]Usage `json:"nodes"`
}

func NodeKey(n Node) string         { return n.Name }
func PodKey(p Pod) string           { return p.UID }
func WorkloadKey(w Workload) string { return w.Kind + "/" + w.Namespace + "/" + w.Name }
