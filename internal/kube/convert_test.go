package kube

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

func rl(cpu, mem string) corev1.ResourceList {
	l := corev1.ResourceList{}
	if cpu != "" {
		l[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if mem != "" {
		l[corev1.ResourceMemory] = resource.MustParse(mem)
	}
	return l
}

func node(labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "gke-prod-default-pool-abcd-1234", Labels: labels},
		Spec:       corev1.NodeSpec{Unschedulable: true, Taints: []corev1.Taint{{Key: "nvidia.com/gpu", Value: "present", Effect: corev1.TaintEffectNoSchedule}}},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("3920m"), corev1.ResourceMemory: resource.MustParse("13000Mi"),
				corev1.ResourcePods: resource.MustParse("110"), "nvidia.com/gpu": resource.MustParse("1"),
			},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeNetworkUnavailable, Status: corev1.ConditionFalse},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue, Reason: "KubeletReady"},
			},
			NodeInfo: corev1.NodeSystemInfo{KubeletVersion: "v1.35.0"},
		},
	}
}

func TestConvertNode(t *testing.T) {
	n := ConvertNode(node(map[string]string{
		"cloud.google.com/gke-nodepool": "gpu-pool", "node.kubernetes.io/instance-type": "g2-standard-8",
		"topology.kubernetes.io/zone": "europe-west1-b", "cloud.google.com/gke-spot": "true",
	}), model.Resources{CPU: 500}, NodeOptions{})
	if n.Pool != "gpu-pool" || n.InstanceType != "g2-standard-8" || n.Zone != "europe-west1-b" || !n.Spot || n.GPU != 1 {
		t.Errorf("node = %+v", n)
	}
	if n.Allocatable.CPU != 3920 || n.Allocatable.Memory != 13000<<20 || n.Allocatable.Pods != 110 || n.Requested.CPU != 500 {
		t.Errorf("ressources = %+v / %+v", n.Allocatable, n.Requested)
	}
	if !n.Unschedulable || len(n.Taints) != 1 || n.Taints[0].Key != "nvidia.com/gpu" || n.KubeletVersion != "v1.35.0" {
		t.Errorf("spec = %+v", n)
	}
	if len(n.Conditions) != 2 || n.Conditions[0].Type != "Ready" || n.Conditions[1].Type != "MemoryPressure" {
		t.Errorf("conditions (Ready en tête, seulement les 4 de la spec) = %+v", n.Conditions)
	}
}

func TestNodePoolDetection(t *testing.T) {
	cases := []struct {
		labels map[string]string
		opts   NodeOptions
		want   string
	}{
		{map[string]string{"karpenter.sh/nodepool": "general"}, NodeOptions{}, "general"},
		{map[string]string{"eks.amazonaws.com/nodegroup": "ng-1"}, NodeOptions{}, "ng-1"},
		{map[string]string{"node.kubernetes.io/instance-type": "m5.large"}, NodeOptions{}, "m5.large"},
		{map[string]string{"team": "data", "cloud.google.com/gke-nodepool": "x"}, NodeOptions{PoolLabel: "team"}, "data"},
		{map[string]string{}, NodeOptions{}, "default"},
	}
	for _, c := range cases {
		if got := ConvertNode(node(c.labels), model.Resources{}, c.opts).Pool; got != c.want {
			t.Errorf("labels %v : pool %q, attendu %q", c.labels, got, c.want)
		}
	}
	for _, l := range []map[string]string{
		{"karpenter.sh/capacity-type": "spot"}, {"eks.amazonaws.com/capacityType": "SPOT"}, {"cloud.google.com/gke-preemptible": "true"},
	} {
		if !ConvertNode(node(l), model.Resources{}, NodeOptions{}).Spot {
			t.Errorf("%v devrait être spot", l)
		}
	}
}

func TestPodRequestsFollowSchedulerRule(t *testing.T) {
	always := corev1.ContainerRestartPolicyAlways
	p := &corev1.Pod{Spec: corev1.PodSpec{
		InitContainers: []corev1.Container{
			{Name: "migrate", Resources: corev1.ResourceRequirements{Requests: rl("2", "100Mi")}},
			{Name: "proxy", RestartPolicy: &always, Resources: corev1.ResourceRequirements{Requests: rl("100m", "50Mi")}},
		},
		Containers: []corev1.Container{
			{Name: "a", Resources: corev1.ResourceRequirements{Requests: rl("250m", "256Mi"), Limits: rl("", "512Mi")}},
			{Name: "b", Resources: corev1.ResourceRequirements{Requests: rl("250m", "64Mi"), Limits: rl("1", "128Mi")}},
		},
		Overhead: rl("10m", ""),
	}}
	req, lim := PodResources(p)
	// max(init migrate = 2000m, containers 500m + sidecar 100m) + overhead 10m
	if req.CPU != 2010 {
		t.Errorf("CPU demandé = %d, attendu 2010", req.CPU)
	}
	// max(100Mi, 256+64+50 Mi)
	if req.Memory != 370<<20 {
		t.Errorf("mémoire demandée = %d", req.Memory)
	}
	if lim.Memory != 640<<20 {
		t.Errorf("limite mémoire = %d", lim.Memory)
	}
}

func TestConvertPod(t *testing.T) {
	p := newPod(main(waiting("CrashLoopBackOff"), false, 4), cond(corev1.PodReady, corev1.ConditionFalse))
	p.Spec.Containers[0].Image = "repo/app:1.2"
	p.Status.PodIP = "10.0.0.7"
	p.Status.QOSClass = corev1.PodQOSBurstable
	p.CreationTimestamp = metav1.Unix(1_700_000_000, 0)
	m := ConvertPod(p, model.OwnerRef{Kind: "Deployment", Name: "app"})
	if m.UID != "uid-p" || m.NodeName != "n1" || m.DisplayStatus != "CrashLoopBackOff" || m.Restarts != 4 || m.Ready {
		t.Errorf("pod = %+v", m)
	}
	if len(m.Containers) != 1 || m.Containers[0].Image != "repo/app:1.2" || m.Containers[0].State != "waiting" || m.Containers[0].Reason != "CrashLoopBackOff" {
		t.Errorf("containers = %+v", m.Containers)
	}
	if m.QOSClass != "Burstable" || m.PodIP != "10.0.0.7" || m.Owner.Kind != "Deployment" || m.CreatedAt.Unix() != 1_700_000_000 {
		t.Errorf("champs = %+v", m)
	}
}

func TestPendingPodCarriesSchedulerMessage(t *testing.T) {
	p := newPod(phase(corev1.PodPending), func(p *corev1.Pod) {
		p.Spec.NodeName = ""
		p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse,
			Reason: "Unschedulable", Message: "0/4 nodes are available: 4 Insufficient cpu."}}
	})
	if m := ConvertPod(p, model.OwnerRef{}); m.StatusMessage != "0/4 nodes are available: 4 Insufficient cpu." {
		t.Errorf("message = %q", m.StatusMessage)
	}
}

func i32(v int32) *int32 { return &v }

func TestConvertWorkloads(t *testing.T) {
	meta := metav1.ObjectMeta{Name: "api", Namespace: "prod",
		Annotations: map[string]string{"argocd.argoproj.io/tracking-id": "prod-apps:apps/Deployment:prod/api"}}
	d := DeploymentWorkload(&appsv1.Deployment{ObjectMeta: meta, Spec: appsv1.DeploymentSpec{Replicas: i32(3)}, Status: appsv1.DeploymentStatus{ReadyReplicas: 2}})
	if d.Kind != "Deployment" || d.Replicas != 3 || d.ReadyReplicas != 2 || d.Argo == nil || d.Argo.Application != "prod-apps" || d.Argo.SyncStatus != "" {
		t.Errorf("deployment = %+v %+v", d, d.Argo)
	}
	plain := metav1.ObjectMeta{Name: "x", Namespace: "prod", Labels: map[string]string{"app.kubernetes.io/instance": "x"}}
	if w := StatefulSetWorkload(&appsv1.StatefulSet{ObjectMeta: plain}); w.Replicas != 1 || w.Argo != nil {
		t.Errorf("statefulset sans replicas = %+v (défaut 1, pas d'ArgoCD)", w)
	}
	byLabel := metav1.ObjectMeta{Name: "y", Labels: map[string]string{"argocd.argoproj.io/instance": "infra"}}
	if w := DaemonSetWorkload(&appsv1.DaemonSet{ObjectMeta: byLabel, Status: appsv1.DaemonSetStatus{DesiredNumberScheduled: 4, NumberReady: 3}}); w.Replicas != 4 || w.ReadyReplicas != 3 || w.Argo.Application != "infra" {
		t.Errorf("daemonset = %+v", w)
	}
	if w := JobWorkload(&batchv1.Job{ObjectMeta: plain, Spec: batchv1.JobSpec{Parallelism: i32(2)}, Status: batchv1.JobStatus{Ready: i32(1)}}); w.Kind != "Job" || w.Replicas != 2 || w.ReadyReplicas != 1 {
		t.Errorf("job = %+v", w)
	}
	if w := ReplicaSetWorkload(&appsv1.ReplicaSet{ObjectMeta: plain, Spec: appsv1.ReplicaSetSpec{Replicas: i32(2)}, Status: appsv1.ReplicaSetStatus{ReadyReplicas: 2}}); w.Kind != "ReplicaSet" || w.ReadyReplicas != 2 {
		t.Errorf("replicaset = %+v", w)
	}
}

func TestConvertNamespace(t *testing.T) {
	ns := func(color string) *corev1.Namespace {
		return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "prod", Annotations: map[string]string{"atlas.io/color": color}}}
	}
	if got := ConvertNamespace(ns("#3D6FB6")); got.Color != "#3D6FB6" {
		t.Errorf("couleur = %q", got.Color)
	}
	for _, bad := range []string{"red", "#12345", "#GGGGGG", "javascript:x"} {
		if got := ConvertNamespace(ns(bad)); got.Color != "" {
			t.Errorf("couleur invalide %q acceptée", bad)
		}
	}
}
