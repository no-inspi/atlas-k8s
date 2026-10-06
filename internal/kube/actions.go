package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/no-inspi/cluster-atlas/internal/access"
	"github.com/no-inspi/cluster-atlas/internal/actions"
)

// Actions applique les actions d'exploitation au nom de l'utilisateur.
type Actions struct {
	clients access.ClientSource
	// Un refus de PDB (429) est réessayé jusqu'à evictionTimeout, comme kubectl drain.
	evictionTimeout time.Duration
	retryEvery      time.Duration
	now             func() time.Time
	// self : le pod de Cluster Atlas. Drainer son node l'évincerait au milieu de
	// la requête ; il est évincé en dernier, après la réponse.
	self      *actions.PodRef
	selfDelay time.Duration
}

func NewActions(clients access.ClientSource) *Actions {
	return &Actions{clients: clients, evictionTimeout: 60 * time.Second, retryEvery: 5 * time.Second, now: time.Now, selfDelay: 2 * time.Second}
}

// WithSelf indique le pod qui exécute Cluster Atlas (downward API).
func (a *Actions) WithSelf(namespace, name string) *Actions {
	if namespace != "" && name != "" {
		a.self = &actions.PodRef{Namespace: namespace, Name: name}
	}
	return a
}

func (a *Actions) isSelf(p actions.PodRef) bool {
	return a.self != nil && a.self.Namespace == p.Namespace && a.self.Name == p.Name
}

var _ actions.Backend = (*Actions)(nil)

func (a *Actions) DeletePod(ctx context.Context, u access.User, ns, name string) error {
	kc, err := a.clients.Kube(u)
	if err != nil {
		return err
	}
	return kc.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{})
}

func mergePatch(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (a *Actions) Scale(ctx context.Context, u access.User, ns, kind, name string, replicas int32) error {
	kc, err := a.clients.Kube(u)
	if err != nil {
		return err
	}
	patch := mergePatch(map[string]any{"spec": map[string]any{"replicas": replicas}})
	switch kind {
	case "Deployment":
		_, err = kc.AppsV1().Deployments(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}, "scale")
	case "StatefulSet":
		_, err = kc.AppsV1().StatefulSets(ns).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{}, "scale")
	default:
		return actions.ErrUnsupportedKind
	}
	return err
}

// Restart fait comme `kubectl rollout restart` : une annotation horodatée sur
// le template déclenche le remplacement progressif des pods.
func (a *Actions) Restart(ctx context.Context, u access.User, ns, kind, name string) error {
	kc, err := a.clients.Kube(u)
	if err != nil {
		return err
	}
	patch := mergePatch(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{
		"annotations": map[string]string{"kubectl.kubernetes.io/restartedAt": a.now().Format(time.RFC3339)},
	}}}})
	opts := metav1.PatchOptions{}
	switch kind {
	case "Deployment":
		_, err = kc.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, opts)
	case "StatefulSet":
		_, err = kc.AppsV1().StatefulSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, opts)
	case "DaemonSet":
		_, err = kc.AppsV1().DaemonSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, opts)
	default:
		return actions.ErrUnsupportedKind
	}
	return err
}

func (a *Actions) SetUnschedulable(ctx context.Context, u access.User, node string, v bool) error {
	kc, err := a.clients.Kube(u)
	if err != nil {
		return err
	}
	return setUnschedulable(ctx, kc, node, v)
}

func setUnschedulable(ctx context.Context, kc kubernetes.Interface, node string, v bool) error {
	patch := mergePatch(map[string]any{"spec": map[string]any{"unschedulable": v}})
	_, err := kc.CoreV1().Nodes().Patch(ctx, node, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

func (a *Actions) DrainPlan(ctx context.Context, u access.User, node string) (actions.DrainPlan, error) {
	kc, err := a.clients.Kube(u)
	if err != nil {
		return actions.DrainPlan{}, err
	}
	return drainPlan(ctx, kc, node)
}

// drainPlan classe les pods du node comme le fait kubectl drain : les pods de
// DaemonSet et les pods statiques restent, un pod sans contrôleur exigerait
// --force (on ne l'évince pas), les autres sont évincés.
func drainPlan(ctx context.Context, kc kubernetes.Interface, node string) (actions.DrainPlan, error) {
	pods, err := kc.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "spec.nodeName=" + node})
	if err != nil {
		return actions.DrainPlan{}, err
	}
	plan := actions.DrainPlan{Node: node, Evict: []actions.PodRef{}, Ignored: []actions.Ignored{}, Blocking: []actions.Blocking{}}
	evict := map[string][]corev1.Pod{} // par namespace, pour les PDB
	for _, p := range pods.Items {
		if p.Spec.NodeName != node { // garde : le field selector suffit sur un vrai API server
			continue
		}
		ref := actions.PodRef{Namespace: p.Namespace, Name: p.Name}
		ctl := metav1.GetControllerOf(&p)
		if ctl != nil {
			ref.Owner = ctl.Kind + " " + ctl.Name
		}
		switch {
		case p.Annotations[corev1.MirrorPodAnnotationKey] != "":
			plan.Ignored = append(plan.Ignored, actions.Ignored{PodRef: ref, Reason: "pod statique (miroir), géré par le kubelet"})
		case ctl != nil && ctl.Kind == "DaemonSet":
			plan.Ignored = append(plan.Ignored, actions.Ignored{PodRef: ref, Reason: "pod de DaemonSet : il reste sur le node"})
		case ctl == nil:
			plan.Ignored = append(plan.Ignored, actions.Ignored{PodRef: ref, Reason: "pod sans contrôleur : il serait perdu (kubectl drain exige --force)"})
		default:
			for _, v := range p.Spec.Volumes {
				ref.EmptyDir = ref.EmptyDir || v.EmptyDir != nil
			}
			plan.Evict = append(plan.Evict, ref)
			evict[p.Namespace] = append(evict[p.Namespace], p)
		}
	}
	sort.Slice(plan.Evict, func(i, j int) bool { return plan.Evict[i].Name < plan.Evict[j].Name })
	sort.Slice(plan.Ignored, func(i, j int) bool { return plan.Ignored[i].Name < plan.Ignored[j].Name })

	for ns, nsPods := range evict {
		pdbs, err := kc.PolicyV1().PodDisruptionBudgets(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			plan.PDBUnknown = "PodDisruptionBudgets illisibles : " + err.Error()
			continue
		}
		for _, pdb := range pdbs.Items {
			sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
			if err != nil || sel.Empty() {
				continue
			}
			var matched []string
			for _, p := range nsPods {
				if sel.Matches(labels.Set(p.Labels)) {
					matched = append(matched, p.Name)
				}
			}
			if len(matched) > int(pdb.Status.DisruptionsAllowed) {
				sort.Strings(matched)
				plan.Blocking = append(plan.Blocking, actions.Blocking{Namespace: ns, Name: pdb.Name, DisruptionsAllowed: pdb.Status.DisruptionsAllowed, Pods: matched})
			}
		}
	}
	sort.Slice(plan.Blocking, func(i, j int) bool {
		return plan.Blocking[i].Namespace+plan.Blocking[i].Name < plan.Blocking[j].Namespace+plan.Blocking[j].Name
	})
	return plan, nil
}

// Drain cordonne le node puis évince ses pods en parallèle. Un refus de PDB
// est réessayé jusqu'à evictionTimeout ; le pod est alors signalé bloqué.
func (a *Actions) Drain(ctx context.Context, u access.User, node string) (actions.DrainResult, error) {
	kc, err := a.clients.Kube(u)
	if err != nil {
		return actions.DrainResult{}, err
	}
	if err := setUnschedulable(ctx, kc, node, true); err != nil {
		return actions.DrainResult{}, err
	}
	plan, err := drainPlan(ctx, kc, node)
	if err != nil {
		return actions.DrainResult{}, err
	}
	res := actions.DrainResult{Node: node, Evictions: make([]actions.Eviction, len(plan.Evict)), Ignored: plan.Ignored}
	var wg sync.WaitGroup
	for i, p := range plan.Evict {
		if a.isSelf(p) {
			res.Evictions[i] = actions.Eviction{PodRef: p, Result: "evicted",
				Message: "Cluster Atlas lui-même : évincé juste après cette réponse, la page se reconnectera"}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			res.Evictions[i] = a.evict(ctx, kc, p)
		}()
	}
	wg.Wait()
	for _, p := range plan.Evict {
		if a.isSelf(p) {
			// Après la réponse : le contexte de la requête sera fermé d'ici là.
			go func() {
				time.Sleep(a.selfDelay)
				_ = a.evict(context.Background(), kc, p)
			}()
		}
	}
	return res, nil
}

func (a *Actions) evict(ctx context.Context, kc kubernetes.Interface, p actions.PodRef) actions.Eviction {
	deadline := a.now().Add(a.evictionTimeout)
	ev := &policyv1.Eviction{ObjectMeta: metav1.ObjectMeta{Namespace: p.Namespace, Name: p.Name}}
	for {
		err := kc.PolicyV1().Evictions(p.Namespace).Evict(ctx, ev)
		switch {
		case err == nil, apierrors.IsNotFound(err):
			return actions.Eviction{PodRef: p, Result: "evicted"}
		case apierrors.IsTooManyRequests(err):
			if a.now().After(deadline) {
				return actions.Eviction{PodRef: p, Result: "blocked", Message: err.Error()}
			}
		default:
			return actions.Eviction{PodRef: p, Result: "error", Message: err.Error()}
		}
		select {
		case <-ctx.Done():
			return actions.Eviction{PodRef: p, Result: "error", Message: fmt.Sprintf("drain interrompu : %v", ctx.Err())}
		case <-time.After(a.retryEvery):
		}
	}
}
