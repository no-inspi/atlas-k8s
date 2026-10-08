package inspect

import "testing"

func TestNetworkKinds(t *testing.T) {
	for _, c := range []struct{ group, version, kind string }{
		{"", "v1", "Service"}, {"", "v1", "PersistentVolumeClaim"}, {"networking.k8s.io", "v1", "Ingress"},
		{"traefik.io", "v1alpha1", "IngressRoute"}, {"traefik.containo.us", "v1alpha1", "IngressRoute"},
		{"gateway.networking.k8s.io", "v1", "Gateway"}, {"gateway.networking.k8s.io", "v1", "GatewayClass"},
		{"gateway.networking.k8s.io", "v1", "HTTPRoute"}, {"gateway.networking.k8s.io", "v1", "GRPCRoute"},
		{"traefik.io", "v1alpha1", "IngressRouteTCP"}, {"traefik.containo.us", "v1alpha1", "IngressRouteTCP"},
		{"traefik.io", "v1alpha1", "IngressRouteUDP"}, {"traefik.containo.us", "v1alpha1", "IngressRouteUDP"},
		{"traefik.io", "v1alpha1", "TraefikService"}, {"traefik.containo.us", "v1alpha1", "TraefikService"},
		{"", "v1", "PersistentVolume"},
	} {
		if _, err := Lookup(c.group, c.version, c.kind); err != nil {
			t.Errorf("%+v : %v", c, err)
		}
	}
	if k, ok := KindForResource("persistentvolumeclaims"); !ok || k != "PersistentVolumeClaim" {
		t.Errorf("KindForResource = %s %v", k, ok)
	}
	for res, want := range map[string]string{"persistentvolumes": "PersistentVolume", "gateways": "Gateway", "httproutes": "HTTPRoute",
		"grpcroutes": "GRPCRoute", "ingressroutetcps": "IngressRouteTCP", "ingressrouteudps": "IngressRouteUDP", "traefikservices": "TraefikService"} {
		if k, ok := KindForResource(res); !ok || k != want {
			t.Errorf("KindForResource(%s) = %s %v", res, k, ok)
		}
	}
	if _, ok := KindForResource("secrets"); ok {
		t.Error("secrets ne doit pas être inspectable")
	}
}
