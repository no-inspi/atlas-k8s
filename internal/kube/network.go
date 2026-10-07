package kube

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// ConvertService réduit un Service et ses EndpointSlices. La santé compte
// toutes les adresses (un Service sans selector comme default/kubernetes reste
// sain) ; Endpoints ne garde que les pods, dédoublonnés (double pile). Un
// Service sans selector ni slice n'est pas publié (publish=false), sauf
// ExternalName.
func ConvertService(s *corev1.Service, slices []*discoveryv1.EndpointSlice) (m model.Service, publish bool) {
	m = model.Service{Namespace: s.Namespace, Name: s.Name, Type: string(s.Spec.Type), ClusterIP: s.Spec.ClusterIP,
		Headless: s.Spec.ClusterIP == corev1.ClusterIPNone, ExternalName: s.Spec.ExternalName,
		Ports: []model.ServicePort{}, Endpoints: []model.Endpoint{}}
	if m.Type == "" {
		m.Type = string(corev1.ServiceTypeClusterIP)
	}
	if m.Headless {
		m.ClusterIP = ""
	}
	for _, p := range s.Spec.Ports {
		m.Ports = append(m.Ports, model.ServicePort{Name: p.Name, Port: p.Port, TargetPort: p.TargetPort.String(),
			Protocol: string(p.Protocol), NodePort: p.NodePort})
	}
	for _, in := range s.Status.LoadBalancer.Ingress {
		if in.IP != "" {
			m.LoadBalancer = append(m.LoadBalancer, in.IP)
		} else if in.Hostname != "" {
			m.LoadBalancer = append(m.LoadBalancer, in.Hostname)
		}
	}

	// Une adresse est identifiée par son pod, sinon par sa première IP.
	readyByID := map[string]bool{}
	pods := map[string]int{}
	for _, sl := range slices {
		for _, e := range sl.Endpoints {
			ready := e.Conditions.Ready == nil || *e.Conditions.Ready
			id := ""
			if e.TargetRef != nil && e.TargetRef.Kind == "Pod" && e.TargetRef.UID != "" {
				id = "pod:" + string(e.TargetRef.UID)
				if i, ok := pods[id]; ok {
					m.Endpoints[i].Ready = m.Endpoints[i].Ready || ready
				} else {
					pods[id] = len(m.Endpoints)
					m.Endpoints = append(m.Endpoints, model.Endpoint{PodUID: string(e.TargetRef.UID), Ready: ready})
				}
			} else if len(e.Addresses) > 0 {
				id = "ip:" + e.Addresses[0]
			} else {
				continue
			}
			readyByID[id] = readyByID[id] || ready
		}
	}
	sort.Slice(m.Endpoints, func(i, j int) bool { return m.Endpoints[i].PodUID < m.Endpoints[j].PodUID })
	ready := 0
	for _, r := range readyByID {
		if r {
			ready++
		}
	}
	m.Health = model.ServiceHealth(m.Type, len(readyByID), ready)
	publish = m.Type == string(corev1.ServiceTypeExternalName) || len(s.Spec.Selector) > 0 || len(slices) > 0
	return m, publish
}
