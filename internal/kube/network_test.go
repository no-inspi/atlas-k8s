package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

func sptr(s string) *string { return &s }

func TestIngressGate(t *testing.T) {
	def := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "nginx", Annotations: map[string]string{"ingressclass.kubernetes.io/is-default-class": "true"}}}
	other := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "traefik"}}
	classes := []*networkingv1.IngressClass{other, def}
	cases := []struct {
		name string
		ing  networkingv1.Ingress
		want string
	}{
		{"spec", networkingv1.Ingress{Spec: networkingv1.IngressSpec{IngressClassName: sptr("traefik")}}, "traefik"},
		{"annotation", networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"kubernetes.io/ingress.class": "haproxy"}}}, "haproxy"},
		{"défaut", networkingv1.Ingress{}, "nginx"},
	}
	for _, c := range cases {
		if got := IngressGate(&c.ing, classes); got != c.want {
			t.Errorf("%s : %s, attendu %s", c.name, got, c.want)
		}
	}
	if got := IngressGate(&networkingv1.Ingress{}, []*networkingv1.IngressClass{other}); got != "default" {
		t.Errorf("sans classe par défaut : %s", got)
	}
}

func TestConvertIngress(t *testing.T) {
	pathType := networkingv1.PathTypePrefix
	backend := func(svc string, port int32) networkingv1.IngressBackend {
		return networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: svc, Port: networkingv1.ServiceBackendPort{Number: port}}}
	}
	def := backend("frontend", 80)
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "prod"},
		Spec: networkingv1.IngressSpec{DefaultBackend: &def, Rules: []networkingv1.IngressRule{{Host: "shop.example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
				{Path: "/api", PathType: &pathType, Backend: backend("api", 8080)},
				{Path: "/old", PathType: &pathType, Backend: backend("ghost", 80)},
			}}}}}},
		Status: networkingv1.IngressStatus{LoadBalancer: networkingv1.IngressLoadBalancerStatus{Ingress: []networkingv1.IngressLoadBalancerIngress{{IP: "34.1.2.3"}}}}}
	exists := func(ns, name string) bool { return ns == "prod" && name != "ghost" }
	r := ConvertIngress(ing, "nginx", exists)
	if r.Source != "Ingress" || r.Group != "networking.k8s.io" || r.Gate != "nginx" || r.Addresses[0] != "34.1.2.3" || len(r.Rules) != 3 {
		t.Fatalf("route = %+v", r)
	}
	if b := r.Rules[0]; b.Host != "" || b.Backend.Service != "frontend" || b.Backend.Port != "80" || b.Backend.State != model.BackendOK {
		t.Errorf("defaultBackend = %+v", b)
	}
	if b := r.Rules[1]; b.Host != "shop.example.com" || b.Path != "/api" || b.Backend.Service != "api" || b.Backend.Namespace != "prod" {
		t.Errorf("règle /api = %+v", b)
	}
	if r.Rules[2].Backend.State != model.BackendMissing {
		t.Errorf("ghost = %+v", r.Rules[2])
	}
	if got := routeBackends(r); len(got) != 3 || got[0] != "prod/frontend" {
		t.Errorf("routeBackends = %v", got)
	}
}

func ingressRoute(group, ns, name string, routes []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": group + "/v1alpha1", "kind": "IngressRoute",
		"metadata": map[string]any{"name": name, "namespace": ns},
		"spec":     map[string]any{"entryPoints": []any{"websecure"}, "routes": routes},
	}}
}

func TestConvertIngressRoute(t *testing.T) {
	u := ingressRoute("traefik.io", "mon", "grafana", []any{
		map[string]any{"match": "Host(`grafana.example.com`) && PathPrefix(`/d`)", "kind": "Rule", "services": []any{
			map[string]any{"name": "grafana", "port": int64(3000)},
			map[string]any{"name": "auth", "namespace": "sso", "port": "http"},
			map[string]any{"name": "weighted", "kind": "TraefikService"},
		}},
	})
	r := ConvertIngressRoute(u, func(ns, name string) bool { return name != "auth" })
	if r.Source != "IngressRoute" || r.Group != "traefik.io" || r.Gate != "traefik" || len(r.Rules) != 3 {
		t.Fatalf("route = %+v", r)
	}
	g := r.Rules[0]
	if g.Host != "grafana.example.com" || g.Path != "/d" || g.Match == "" || g.Backend.Port != "3000" || g.Backend.Namespace != "mon" || g.Backend.State != model.BackendOK {
		t.Errorf("grafana = %+v", g)
	}
	if a := r.Rules[1].Backend; a.Namespace != "sso" || a.Port != "http" || a.State != model.BackendMissing {
		t.Errorf("auth = %+v", a)
	}
	if w := r.Rules[2].Backend; w.Kind != "TraefikService" || w.State != model.BackendIndirect {
		t.Errorf("weighted = %+v", w)
	}
	if got := routeBackends(r); len(got) != 2 {
		t.Errorf("un TraefikService n'est pas un Service : %v", got)
	}
	u.SetAnnotations(map[string]string{"kubernetes.io/ingress.class": "traefik-internal"})
	if g := ConvertIngressRoute(u, nil).Gate; g != "traefik-internal" {
		t.Errorf("porte annotée = %s", g)
	}
}

func TestConvertPVC(t *testing.T) {
	class := "standard-rwo"
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data-0", Namespace: "prod"},
		Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &class, VolumeName: "pvc-123",
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}}},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound, Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")}}}
	v := ConvertPVC(pvc, []string{"u1"})
	if v.StorageClass != "standard-rwo" || v.Requested != 10<<30 || v.Capacity != 20<<30 || v.Phase != "Bound" ||
		v.AccessModes[0] != "ReadWriteOnce" || v.VolumeName != "pvc-123" || v.Pods[0] != "u1" {
		t.Errorf("volume = %+v", v)
	}
	empty := ConvertPVC(&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "x"}}, nil)
	if empty.Phase != "Pending" || empty.Pods == nil || empty.AccessModes == nil {
		t.Errorf("PVC neuf = %+v", empty)
	}
}

func TestPodClaims(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-0"}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{
		{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-web-0"}}},
		{Name: "scratch", VolumeSource: corev1.VolumeSource{Ephemeral: &corev1.EphemeralVolumeSource{}}},
		{Name: "cfg", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{}}},
	}}}
	got := PodClaims(p)
	if len(got) != 2 || got[0] != "data-web-0" || got[1] != "web-0-scratch" {
		t.Errorf("PodClaims = %v", got)
	}
}
