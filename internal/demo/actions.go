package demo

import (
	"context"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/no-inspi/cluster-atlas/internal/access"
	"github.com/no-inspi/cluster-atlas/internal/actions"
)

// Les actions de l'inspecteur agissent sur le cluster simulé.

var _ actions.Backend = (*Sim)(nil)

func (s *Sim) workload(ns, kind, name string) *simWorkload {
	for _, w := range s.workloads {
		if w.def.NS == ns && w.def.Kind == kind && w.def.Name == name {
			return w
		}
	}
	return nil
}

func (s *Sim) findNode(name string) *simNode {
	for _, n := range s.nodes {
		if n.node.Name == name {
			return n
		}
	}
	return nil
}

func (s *Sim) DeletePod(_ context.Context, _ access.User, ns, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findPod(ns, name)
	if p == nil {
		return notFound("pods", name)
	}
	s.terminate(p)
	return nil
}

func (s *Sim) Scale(_ context.Context, _ access.User, ns, kind, name string, replicas int32) error {
	if kind != "Deployment" && kind != "StatefulSet" {
		return actions.ErrUnsupportedKind
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workload(ns, kind, name)
	if w == nil {
		return notFound(strings.ToLower(kind)+"s", name)
	}
	w.replicas = replicas
	return nil
}

func (s *Sim) Restart(_ context.Context, _ access.User, ns, kind, name string) error {
	if _, ok := map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true}[kind]; !ok {
		return actions.ErrUnsupportedKind
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.workload(ns, kind, name)
	if w == nil {
		return notFound(strings.ToLower(kind)+"s", name)
	}
	if kind == "Deployment" {
		w.hash = s.rid(10) // nouveau ReplicaSet : les pods sont remplacés un à un
	} else {
		w.restartedAt = s.now
	}
	return nil
}

func (s *Sim) SetUnschedulable(_ context.Context, _ access.User, node string, v bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.findNode(node)
	if n == nil {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, node)
	}
	n.node.Unschedulable, n.dirty = v, true
	return nil
}

func (s *Sim) drainPlanLocked(node string) actions.DrainPlan {
	plan := actions.DrainPlan{Node: node, Evict: []actions.PodRef{}, Ignored: []actions.Ignored{}, Blocking: []actions.Blocking{}}
	for _, p := range s.pods {
		if p.pod.NodeName != node || p.pod.DisplayStatus == "Terminating" {
			continue
		}
		ref := actions.PodRef{Namespace: p.pod.Namespace, Name: p.pod.Name, Owner: p.wl.def.Kind + " " + p.wl.def.Name}
		if p.wl.def.Kind == "DaemonSet" {
			plan.Ignored = append(plan.Ignored, actions.Ignored{PodRef: ref, Reason: "pod de DaemonSet : il reste sur le node"})
			continue
		}
		plan.Evict = append(plan.Evict, ref)
	}
	sort.Slice(plan.Evict, func(i, j int) bool { return plan.Evict[i].Name < plan.Evict[j].Name })
	return plan
}

func (s *Sim) DrainPlan(_ context.Context, _ access.User, node string) (actions.DrainPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.findNode(node) == nil {
		return actions.DrainPlan{}, apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, node)
	}
	return s.drainPlanLocked(node), nil
}

func (s *Sim) Drain(_ context.Context, _ access.User, node string) (actions.DrainResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.findNode(node)
	if n == nil {
		return actions.DrainResult{}, apierrors.NewNotFound(schema.GroupResource{Resource: "nodes"}, node)
	}
	n.node.Unschedulable, n.dirty = true, true
	plan := s.drainPlanLocked(node)
	res := actions.DrainResult{Node: node, Evictions: []actions.Eviction{}, Ignored: plan.Ignored}
	for _, ref := range plan.Evict {
		if p := s.findPod(ref.Namespace, ref.Name); p != nil {
			s.event(p, "Normal", "Evicted", "Evicted by drain of node "+node, "atlas")
			s.terminate(p)
		}
		res.Evictions = append(res.Evictions, actions.Eviction{PodRef: ref, Result: "evicted"})
	}
	return res, nil
}

/* ---------- rollout restart ---------- */

// rollDeployment remplace progressivement les pods d'un ancien ReplicaSet
// (maxSurge 1, maxUnavailable 0). Renvoie false s'il n'y a pas de rollout.
func (s *Sim) rollDeployment(w *simWorkload, live []*simPod) bool {
	prefix := w.def.Name + "-" + w.hash + "-"
	var old, cur []*simPod
	for _, p := range live {
		if strings.HasPrefix(p.pod.Name, prefix) {
			cur = append(cur, p)
		} else {
			old = append(old, p)
		}
	}
	if len(old) == 0 {
		return false
	}
	for _, p := range cur {
		if p.pod.DisplayStatus != "Running" {
			return true // on attend que le nouveau pod soit prêt
		}
	}
	if len(cur) < int(w.replicas) && len(live) <= int(w.replicas) {
		s.newPod(w, 0)
		return true
	}
	sort.Slice(old, func(i, j int) bool { return old[i].pod.CreatedAt.Before(old[j].pod.CreatedAt) })
	s.terminate(old[0])
	return true
}

// rollRestart recrée un à un les pods antérieurs au restart, quand tous les
// autres sont prêts (StatefulSet par ordinal décroissant, DaemonSet par node).
func (s *Sim) rollRestart(w *simWorkload, desired int) {
	if w.restartedAt.IsZero() {
		return
	}
	live := s.livePods(w)
	if len(live) < desired {
		return
	}
	var stale []*simPod
	for _, p := range live {
		if p.pod.DisplayStatus != "Running" {
			return
		}
		if p.pod.CreatedAt.Before(w.restartedAt) {
			stale = append(stale, p)
		}
	}
	if len(stale) == 0 {
		w.restartedAt = time.Time{} // restart terminé
		return
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].ordinal > stale[j].ordinal })
	s.terminate(stale[0])
}
