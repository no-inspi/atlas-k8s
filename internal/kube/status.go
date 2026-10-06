// Package kube lit le cluster via des informers partagés et le traduit dans le
// modèle réduit envoyé au front.
package kube

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

// DisplayStatus renvoie le statut affiché par `kubectl get pods` et le nombre de
// redémarrages de la même colonne. Portage de printPod
// (k8s.io/kubernetes/pkg/printers/internalversion/printers.go).
func DisplayStatus(pod *corev1.Pod) (string, int32) {
	var restarts, sidecarRestarts int32
	reason := string(pod.Status.Phase)
	if pod.Status.Reason != "" {
		reason = pod.Status.Reason
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Reason == corev1.PodReasonSchedulingGated {
			reason = corev1.PodReasonSchedulingGated
		}
	}

	initSpecs := map[string]*corev1.Container{}
	for i := range pod.Spec.InitContainers {
		initSpecs[pod.Spec.InitContainers[i].Name] = &pod.Spec.InitContainers[i]
	}

	initializing := false
	for i, c := range pod.Status.InitContainerStatuses {
		restarts += c.RestartCount
		sidecar := isSidecar(initSpecs[c.Name])
		if sidecar {
			sidecarRestarts += c.RestartCount
		}
		switch {
		case c.State.Terminated != nil && c.State.Terminated.ExitCode == 0:
			continue
		case sidecar && c.Started != nil && *c.Started:
			continue
		case c.State.Terminated != nil:
			t := c.State.Terminated
			switch {
			case t.Reason != "":
				reason = "Init:" + t.Reason
			case t.Signal != 0:
				reason = fmt.Sprintf("Init:Signal:%d", t.Signal)
			default:
				reason = fmt.Sprintf("Init:ExitCode:%d", t.ExitCode)
			}
			initializing = true
		case c.State.Waiting != nil && c.State.Waiting.Reason != "" && c.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + c.State.Waiting.Reason
			initializing = true
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(pod.Spec.InitContainers))
			initializing = true
		}
		break
	}

	if !initializing || conditionTrue(pod, corev1.PodInitialized) {
		restarts = sidecarRestarts
		hasRunning := false
		for i := len(pod.Status.ContainerStatuses) - 1; i >= 0; i-- {
			c := pod.Status.ContainerStatuses[i]
			restarts += c.RestartCount
			switch {
			case c.State.Waiting != nil && c.State.Waiting.Reason != "":
				reason = c.State.Waiting.Reason
			case c.State.Terminated != nil && c.State.Terminated.Reason != "":
				reason = c.State.Terminated.Reason
			case c.State.Terminated != nil && c.State.Terminated.Signal != 0:
				reason = fmt.Sprintf("Signal:%d", c.State.Terminated.Signal)
			case c.State.Terminated != nil:
				reason = fmt.Sprintf("ExitCode:%d", c.State.Terminated.ExitCode)
			case c.Ready && c.State.Running != nil:
				hasRunning = true
			}
		}
		if reason == "Completed" && hasRunning {
			if conditionTrue(pod, corev1.PodReady) {
				reason = "Running"
			} else {
				reason = "NotReady"
			}
		}
	}

	terminal := pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
	switch {
	case pod.DeletionTimestamp != nil && pod.Status.Reason == "NodeLost":
		reason = "Unknown"
	case pod.DeletionTimestamp != nil && !terminal:
		reason = "Terminating"
	}
	return reason, restarts
}

// PodReady suit la condition Ready du pod.
func PodReady(pod *corev1.Pod) bool { return conditionTrue(pod, corev1.PodReady) }

func conditionTrue(pod *corev1.Pod, t corev1.PodConditionType) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == t {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// isSidecar : init container avec restartPolicy Always (sidecar natif).
func isSidecar(c *corev1.Container) bool {
	return c != nil && c.RestartPolicy != nil && *c.RestartPolicy == corev1.ContainerRestartPolicyAlways
}
