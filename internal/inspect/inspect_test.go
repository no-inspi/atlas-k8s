package inspect

import "testing"

func TestNetworkKinds(t *testing.T) {
	for _, c := range []struct{ group, version, kind string }{
		{"", "v1", "Service"}, {"", "v1", "PersistentVolumeClaim"}, {"networking.k8s.io", "v1", "Ingress"},
		{"traefik.io", "v1alpha1", "IngressRoute"}, {"traefik.containo.us", "v1alpha1", "IngressRoute"},
	} {
		if _, err := Lookup(c.group, c.version, c.kind); err != nil {
			t.Errorf("%+v : %v", c, err)
		}
	}
	if k, ok := KindForResource("persistentvolumeclaims"); !ok || k != "PersistentVolumeClaim" {
		t.Errorf("KindForResource = %s %v", k, ok)
	}
	if _, ok := KindForResource("secrets"); ok {
		t.Error("secrets ne doit pas être inspectable")
	}
}
