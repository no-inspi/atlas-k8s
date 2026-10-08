package kube

import (
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// NodeOptions règle la détection du pool.
type NodeOptions struct {
	// PoolLabel force le label qui désigne le pool (ui.poolLabel du chart).
	PoolLabel string
}

// Labels de pool par ordre de préférence (spec, « Modèle envoyé au front »).
var poolLabels = []string{"cloud.google.com/gke-nodepool", "karpenter.sh/nodepool", "eks.amazonaws.com/nodegroup", corev1.LabelInstanceTypeStable}

const gpuResource corev1.ResourceName = "nvidia.com/gpu"

// Conditions montrées par l'inspecteur, dans cet ordre.
var nodeConditions = []corev1.NodeConditionType{corev1.NodeReady, corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure}

// ConvertNode traduit un node ; requested est la somme des requests de ses pods.
func ConvertNode(n *corev1.Node, requested model.Resources, opts NodeOptions) model.Node {
	alloc := n.Status.Allocatable
	capa := n.Status.Capacity
	gpu := alloc[gpuResource]
	m := model.Node{
		Name:         n.Name,
		Pool:         nodePool(n.Labels, opts),
		InstanceType: n.Labels[corev1.LabelInstanceTypeStable],
		Zone:         n.Labels[corev1.LabelTopologyZone],
		Spot:         isSpot(n.Labels),
		GPU:          int(gpu.Value()),
		Capacity: model.Resources{
			CPU: capa.Cpu().MilliValue(), Memory: capa.Memory().Value(), Pods: capa.Pods().Value(),
		},
		Allocatable: model.Resources{
			CPU: alloc.Cpu().MilliValue(), Memory: alloc.Memory().Value(), Pods: alloc.Pods().Value(),
		},
		Requested:      requested,
		Conditions:     []model.Condition{},
		Taints:         []model.Taint{},
		Unschedulable:  n.Spec.Unschedulable,
		KubeletVersion: n.Status.NodeInfo.KubeletVersion,
		CreatedAt:      n.CreationTimestamp.Time,
	}
	for _, t := range nodeConditions {
		for _, c := range n.Status.Conditions {
			if c.Type == t {
				m.Conditions = append(m.Conditions, model.Condition{Type: string(c.Type), Status: string(c.Status), Reason: c.Reason, Message: c.Message})
			}
		}
	}
	for _, t := range n.Spec.Taints {
		m.Taints = append(m.Taints, model.Taint{Key: t.Key, Value: t.Value, Effect: string(t.Effect)})
	}
	return m
}

func nodePool(labels map[string]string, opts NodeOptions) string {
	if opts.PoolLabel != "" && labels[opts.PoolLabel] != "" {
		return labels[opts.PoolLabel]
	}
	for _, l := range poolLabels {
		if v := labels[l]; v != "" {
			return v
		}
	}
	return "default"
}

func isSpot(l map[string]string) bool {
	return l["cloud.google.com/gke-spot"] == "true" || l["cloud.google.com/gke-preemptible"] == "true" ||
		l["karpenter.sh/capacity-type"] == "spot" || strings.EqualFold(l["eks.amazonaws.com/capacityType"], "SPOT")
}

// PodResources calcule requests et limits effectifs comme kube-scheduler :
// max(somme des containers et sidecars, pic d'un init container) + overhead.
func PodResources(p *corev1.Pod) (requests, limits model.Resources) {
	requests = aggregate(p, func(c *corev1.Container) corev1.ResourceList { return c.Resources.Requests })
	limits = aggregate(p, func(c *corev1.Container) corev1.ResourceList { return c.Resources.Limits })
	requests.CPU += p.Spec.Overhead.Cpu().MilliValue()
	requests.Memory += p.Spec.Overhead.Memory().Value()
	return requests, limits
}

func aggregate(p *corev1.Pod, get func(*corev1.Container) corev1.ResourceList) model.Resources {
	var sidecars, peak, sum model.Resources
	for i := range p.Spec.InitContainers {
		c := &p.Spec.InitContainers[i]
		r := get(c)
		if isSidecar(c) {
			sidecars.CPU += r.Cpu().MilliValue()
			sidecars.Memory += r.Memory().Value()
			peak.CPU = max(peak.CPU, sidecars.CPU)
			peak.Memory = max(peak.Memory, sidecars.Memory)
			continue
		}
		peak.CPU = max(peak.CPU, r.Cpu().MilliValue()+sidecars.CPU)
		peak.Memory = max(peak.Memory, r.Memory().Value()+sidecars.Memory)
	}
	for i := range p.Spec.Containers {
		r := get(&p.Spec.Containers[i])
		sum.CPU += r.Cpu().MilliValue()
		sum.Memory += r.Memory().Value()
	}
	return model.Resources{CPU: max(sum.CPU+sidecars.CPU, peak.CPU), Memory: max(sum.Memory+sidecars.Memory, peak.Memory)}
}

// ConvertPod traduit un pod ; owner est son workload racine (voir RootOwner).
func ConvertPod(p *corev1.Pod, owner model.OwnerRef) model.Pod {
	status, restarts := DisplayStatus(p)
	req, lim := PodResources(p)
	m := model.Pod{
		UID:           string(p.UID),
		Name:          p.Name,
		Namespace:     p.Namespace,
		NodeName:      p.Spec.NodeName,
		Phase:         string(p.Status.Phase),
		DisplayStatus: status,
		StatusMessage: p.Status.Message,
		Ready:         PodReady(p),
		Restarts:      restarts,
		Containers:    []model.ContainerStatus{},
		Owner:         owner,
		Requests:      req,
		Limits:        lim,
		PodIP:         p.Status.PodIP,
		CreatedAt:     p.CreationTimestamp.Time,
		QOSClass:      string(p.Status.QOSClass),
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Message != "" {
			m.StatusMessage = c.Message
		}
	}
	m.Containers = append(m.Containers, containers(p.Spec.InitContainers, p.Status.InitContainerStatuses, true)...)
	m.Containers = append(m.Containers, containers(p.Spec.Containers, p.Status.ContainerStatuses, false)...)
	return m
}

func containers(specs []corev1.Container, statuses []corev1.ContainerStatus, init bool) []model.ContainerStatus {
	byName := map[string]corev1.ContainerStatus{}
	for _, s := range statuses {
		byName[s.Name] = s
	}
	out := make([]model.ContainerStatus, 0, len(specs))
	for _, c := range specs {
		s := byName[c.Name]
		m := model.ContainerStatus{Name: c.Name, Image: c.Image, Ready: s.Ready, Restarts: s.RestartCount, Init: init, State: "waiting"}
		switch {
		case s.State.Running != nil:
			m.State = "running"
		case s.State.Terminated != nil:
			m.State, m.Reason = "terminated", s.State.Terminated.Reason
		case s.State.Waiting != nil:
			m.Reason = s.State.Waiting.Reason
		}
		out = append(out, m)
	}
	return out
}

func replicas(r *int32) int32 {
	if r == nil {
		return 1
	}
	return *r
}

func workload(kind string, meta metav1.ObjectMeta, desired, ready int32) model.Workload {
	return model.Workload{Kind: kind, Name: meta.Name, Namespace: meta.Namespace, Replicas: desired, ReadyReplicas: ready, Argo: argoInfo(meta)}
}

func DeploymentWorkload(d *appsv1.Deployment) model.Workload {
	return workload("Deployment", d.ObjectMeta, replicas(d.Spec.Replicas), d.Status.ReadyReplicas)
}

func StatefulSetWorkload(s *appsv1.StatefulSet) model.Workload {
	return workload("StatefulSet", s.ObjectMeta, replicas(s.Spec.Replicas), s.Status.ReadyReplicas)
}

func DaemonSetWorkload(d *appsv1.DaemonSet) model.Workload {
	return workload("DaemonSet", d.ObjectMeta, d.Status.DesiredNumberScheduled, d.Status.NumberReady)
}

func ReplicaSetWorkload(r *appsv1.ReplicaSet) model.Workload {
	return workload("ReplicaSet", r.ObjectMeta, replicas(r.Spec.Replicas), r.Status.ReadyReplicas)
}

func JobWorkload(j *batchv1.Job) model.Workload {
	var ready int32
	if j.Status.Ready != nil {
		ready = *j.Status.Ready
	}
	return workload("Job", j.ObjectMeta, replicas(j.Spec.Parallelism), ready)
}

// argoInfo lit l'application ArgoCD dans l'annotation de suivi ou le label
// d'instance d'ArgoCD. Le statut de sync demanderait de lire les Application :
// il reste vide.
func argoInfo(meta metav1.ObjectMeta) *model.ArgoInfo {
	if id := meta.Annotations["argocd.argoproj.io/tracking-id"]; id != "" {
		app, _, _ := strings.Cut(id, ":")
		return &model.ArgoInfo{Application: app}
	}
	if app := meta.Labels["argocd.argoproj.io/instance"]; app != "" {
		return &model.ArgoInfo{Application: app}
	}
	return nil
}

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

// ConvertNamespace garde la couleur de l'annotation atlas.io/color si elle est valide.
func ConvertNamespace(ns *corev1.Namespace) model.Namespace {
	m := model.Namespace{Name: ns.Name}
	if c := ns.Annotations["atlas.io/color"]; hexColor.MatchString(c) {
		m.Color = c
	}
	return m
}
