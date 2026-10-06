package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func waiting(reason string) corev1.ContainerState {
	return corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason}}
}

func terminated(reason string, code int32, signal int32) corev1.ContainerState {
	return corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: reason, ExitCode: code, Signal: signal}}
}

var running = corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}

type podOpt func(*corev1.Pod)

func newPod(opts ...podOpt) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", UID: "uid-p"},
		Spec:       corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "app"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func phase(ph corev1.PodPhase) podOpt { return func(p *corev1.Pod) { p.Status.Phase = ph } }

func main(state corev1.ContainerState, ready bool, restarts int32) podOpt {
	return func(p *corev1.Pod) {
		p.Status.ContainerStatuses = append(p.Status.ContainerStatuses,
			corev1.ContainerStatus{Name: "app", State: state, Ready: ready, RestartCount: restarts})
	}
}

func initc(state corev1.ContainerState, restarts int32) podOpt {
	return func(p *corev1.Pod) {
		p.Spec.InitContainers = append(p.Spec.InitContainers, corev1.Container{Name: "init"})
		p.Status.InitContainerStatuses = append(p.Status.InitContainerStatuses,
			corev1.ContainerStatus{Name: "init", State: state, RestartCount: restarts})
	}
}

func sidecar(started, ready bool) podOpt {
	always := corev1.ContainerRestartPolicyAlways
	return func(p *corev1.Pod) {
		p.Spec.InitContainers = append(p.Spec.InitContainers, corev1.Container{Name: "proxy", RestartPolicy: &always})
		p.Status.InitContainerStatuses = append(p.Status.InitContainerStatuses,
			corev1.ContainerStatus{Name: "proxy", State: running, Started: &started, Ready: ready, RestartCount: 2})
	}
}

func cond(t corev1.PodConditionType, s corev1.ConditionStatus) podOpt {
	return func(p *corev1.Pod) {
		p.Status.Conditions = append(p.Status.Conditions, corev1.PodCondition{Type: t, Status: s})
	}
}

func deleting(p *corev1.Pod) {
	now := metav1.Now()
	p.DeletionTimestamp = &now
}

func TestDisplayStatus(t *testing.T) {
	cases := []struct {
		name     string
		pod      *corev1.Pod
		want     string
		restarts int32
	}{
		{"pending sans node", newPod(phase(corev1.PodPending), func(p *corev1.Pod) { p.Spec.NodeName = "" }), "Pending", 0},
		{"container en création", newPod(phase(corev1.PodPending), main(waiting("ContainerCreating"), false, 0)), "ContainerCreating", 0},
		{"running prêt", newPod(main(running, true, 0)), "Running", 0},
		{"running non prêt", newPod(main(running, false, 0)), "Running", 0},
		{"crashloop", newPod(main(waiting("CrashLoopBackOff"), false, 7)), "CrashLoopBackOff", 7},
		{"erreur", newPod(main(terminated("Error", 1, 0), false, 3)), "Error", 3},
		{"oomkilled", newPod(main(terminated("OOMKilled", 137, 0), false, 1)), "OOMKilled", 1},
		{"exit code sans raison", newPod(main(terminated("", 137, 0), false, 0)), "ExitCode:137", 0},
		{"signal sans raison", newPod(main(terminated("", 0, 9), false, 0)), "Signal:9", 0},
		{"image introuvable", newPod(phase(corev1.PodPending), main(waiting("ImagePullBackOff"), false, 0)), "ImagePullBackOff", 0},
		{"erreur de pull", newPod(phase(corev1.PodPending), main(waiting("ErrImagePull"), false, 0)), "ErrImagePull", 0},
		{"job terminé", newPod(phase(corev1.PodSucceeded), main(terminated("Completed", 0, 0), false, 0)), "Completed", 0},
		{"évincé", newPod(phase(corev1.PodFailed), func(p *corev1.Pod) { p.Status.Reason = "Evicted" }), "Evicted", 0},
		{"init en attente", newPod(phase(corev1.PodPending), initc(running, 0), main(waiting("PodInitializing"), false, 0)), "Init:0/1", 0},
		{"init en crashloop", newPod(phase(corev1.PodPending), initc(waiting("CrashLoopBackOff"), 4), main(waiting("PodInitializing"), false, 0)), "Init:CrashLoopBackOff", 4},
		{"init en erreur", newPod(phase(corev1.PodPending), initc(terminated("Error", 1, 0), 0)), "Init:Error", 0},
		{"init exit code", newPod(phase(corev1.PodPending), initc(terminated("", 2, 0), 0)), "Init:ExitCode:2", 0},
		{"init terminé puis PodInitializing", newPod(phase(corev1.PodPending), initc(terminated("Completed", 0, 0), 0), main(waiting("PodInitializing"), false, 0)), "PodInitializing", 0},
		{"sidecar démarré ignoré", newPod(sidecar(true, true), main(running, true, 1), cond(corev1.PodReady, corev1.ConditionTrue)), "Running", 3},
		{"completed mais un container tourne encore", newPod(
			func(p *corev1.Pod) {
				p.Spec.Containers = append(p.Spec.Containers, corev1.Container{Name: "side"})
				p.Status.ContainerStatuses = []corev1.ContainerStatus{
					{Name: "app", State: terminated("Completed", 0, 0)},
					{Name: "side", State: running, Ready: true},
				}
			}, cond(corev1.PodReady, corev1.ConditionFalse)), "NotReady", 0},
		{"en suppression", newPod(main(running, true, 0), deleting), "Terminating", 0},
		{"terminé puis supprimé reste Completed", newPod(phase(corev1.PodSucceeded), main(terminated("Completed", 0, 0), false, 0), deleting), "Completed", 0},
		{"node perdu", newPod(main(running, true, 0), deleting, func(p *corev1.Pod) { p.Status.Reason = "NodeLost" }), "Unknown", 0},
		{"scheduling gated", newPod(phase(corev1.PodPending), func(p *corev1.Pod) {
			p.Spec.NodeName = ""
			p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "SchedulingGated"}}
		}), "SchedulingGated", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, restarts := DisplayStatus(c.pod)
			if got != c.want || restarts != c.restarts {
				t.Errorf("DisplayStatus = %q (%d redémarrages), attendu %q (%d)", got, restarts, c.want, c.restarts)
			}
		})
	}
}

func TestPodReady(t *testing.T) {
	if !PodReady(newPod(cond(corev1.PodReady, corev1.ConditionTrue))) {
		t.Error("PodReady=True attendu")
	}
	if PodReady(newPod(cond(corev1.PodReady, corev1.ConditionFalse))) || PodReady(newPod()) {
		t.Error("pod non prêt considéré prêt")
	}
}
