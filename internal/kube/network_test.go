package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

func bptr(b bool) *bool { return &b }

func slice(svc string, eps ...discoveryv1.Endpoint) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: svc + "-x", Namespace: "prod", Labels: map[string]string{discoveryv1.LabelServiceName: svc}},
		Endpoints:  eps,
	}
}

func podEp(uid string, ready *bool) discoveryv1.Endpoint {
	return discoveryv1.Endpoint{Addresses: []string{"10.0.0." + uid}, Conditions: discoveryv1.EndpointConditions{Ready: ready},
		TargetRef: &corev1.ObjectReference{Kind: "Pod", UID: types.UID(uid)}}
}

func TestConvertService(t *testing.T) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, ClusterIP: "10.96.0.10", Selector: map[string]string{"app": "api"},
			Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP, NodePort: 31000}}},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "34.1.2.3"}}}}}
	// Double pile : le pod 1 apparaît dans deux slices ; ready absent vaut ready.
	m, ok := ConvertService(svc, []*discoveryv1.EndpointSlice{
		slice("api", podEp("1", bptr(true)), podEp("2", bptr(false))),
		slice("api", podEp("1", nil)),
	})
	if !ok {
		t.Fatal("Service avec selector non publié")
	}
	want := []model.Endpoint{{PodUID: "1", Ready: true}, {PodUID: "2", Ready: false}}
	if len(m.Endpoints) != 2 || m.Endpoints[0] != want[0] || m.Endpoints[1] != want[1] {
		t.Errorf("endpoints = %+v", m.Endpoints)
	}
	if m.Health != model.HealthDegraded || m.Type != "LoadBalancer" || m.LoadBalancer[0] != "34.1.2.3" {
		t.Errorf("service = %+v", m)
	}
	if p := m.Ports[0]; p.Port != 80 || p.TargetPort != "http" || p.NodePort != 31000 || p.Protocol != "TCP" {
		t.Errorf("port = %+v", p)
	}
}

func TestConvertServiceWithoutPods(t *testing.T) {
	// Service sans selector mais avec une slice (default/kubernetes) : adresse sans pod, ready.
	k8s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"}, Spec: corev1.ServiceSpec{ClusterIP: "10.96.0.1"}}
	m, ok := ConvertService(k8s, []*discoveryv1.EndpointSlice{slice("kubernetes", discoveryv1.Endpoint{Addresses: []string{"172.18.0.2"}})})
	if !ok || m.Health != model.HealthOK || len(m.Endpoints) != 0 || m.Type != "ClusterIP" {
		t.Errorf("kubernetes = %v %+v", ok, m)
	}
	// Ni selector ni slice : pas publié.
	if _, ok := ConvertService(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "manual"}}, nil); ok {
		t.Error("Service sans selector ni slice publié")
	}
	// Selector sans endpoint : down. ExternalName : external. Headless : sans IP.
	down, _ := ConvertService(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "ghost"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "x"}, ClusterIP: "None"}}, nil)
	if down.Health != model.HealthDown || !down.Headless || down.ClusterIP != "" {
		t.Errorf("ghost = %+v", down)
	}
	ext, ok := ConvertService(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "stripe"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "api.stripe.com"}}, nil)
	if !ok || ext.Health != model.HealthExternal || ext.ExternalName != "api.stripe.com" {
		t.Errorf("stripe = %v %+v", ok, ext)
	}
}
