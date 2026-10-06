package access

import (
	"context"
	"sync"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// StreamFilter applique les droits d'un utilisateur au flux : un objet n'est
// envoyé que si l'utilisateur peut lister ce type dans ce namespace.
type StreamFilter struct {
	r    *Reviewer
	user User

	mu      sync.Mutex
	decided map[Attributes]bool
}

func NewStreamFilter(r *Reviewer, u User) *StreamFilter {
	return &StreamFilter{r: r, user: u, decided: map[Attributes]bool{}}
}

var workloadResources = map[string]Attributes{
	"Deployment":  {Group: "apps", Resource: "deployments"},
	"ReplicaSet":  {Group: "apps", Resource: "replicasets"},
	"StatefulSet": {Group: "apps", Resource: "statefulsets"},
	"DaemonSet":   {Group: "apps", Resource: "daemonsets"},
	"Job":         {Group: "batch", Resource: "jobs"},
}

// attributes : droit requis pour recevoir l'objet. Un namespace (qui ne porte
// qu'une couleur) suit le droit de lister ses pods.
func attributes(kind stream.Kind, obj any) (Attributes, bool) {
	switch o := obj.(type) {
	case model.Pod:
		return Attributes{Verb: "list", Resource: "pods", Namespace: o.Namespace}, true
	case model.Node:
		return Attributes{Verb: "list", Resource: "nodes"}, true
	case model.Namespace:
		return Attributes{Verb: "list", Resource: "pods", Namespace: o.Name}, true
	case model.Workload:
		a, ok := workloadResources[o.Kind]
		a.Verb, a.Namespace = "list", o.Namespace
		return a, ok
	}
	return Attributes{}, false
}

func (f *StreamFilter) Allow(ctx context.Context, kind stream.Kind, obj any) bool {
	a, ok := attributes(kind, obj)
	if !ok {
		return false
	}
	allowed := f.r.Allowed(ctx, f.user, a)
	f.mu.Lock()
	f.decided[a] = allowed
	f.mu.Unlock()
	return allowed
}

// Changed réévalue les décisions déjà prises pour ce client.
func (f *StreamFilter) Changed(ctx context.Context) bool {
	f.mu.Lock()
	prev := make(map[Attributes]bool, len(f.decided))
	for a, v := range f.decided {
		prev[a] = v
	}
	f.mu.Unlock()
	changed := false
	for a, was := range prev {
		now := f.r.Allowed(ctx, f.user, a)
		if now != was {
			changed = true
			f.mu.Lock()
			f.decided[a] = now
			f.mu.Unlock()
		}
	}
	return changed
}
