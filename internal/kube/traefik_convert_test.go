package kube

import (
	"fmt"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

func traefikService(ns, name string, spec map[string]any) *unstructured.Unstructured {
	return unstr("traefik.io/v1alpha1", "TraefikService", ns, name, spec, nil)
}

func lookupOf(ts ...*unstructured.Unstructured) TraefikLookup {
	return func(ns, name string) (*unstructured.Unstructured, bool) {
		for _, u := range ts {
			if u.GetNamespace() == ns && u.GetName() == name {
				return u, true
			}
		}
		return nil, false
	}
}

func toTS(name string) map[string]any { return map[string]any{"name": name, "kind": "TraefikService"} }

func checkoutRoute(services ...any) *unstructured.Unstructured {
	return ingressRoute("traefik.io", "prod", "checkout", []any{map[string]any{"match": "Host(`c.example.com`)", "services": services}})
}

func TestResolveTraefikService(t *testing.T) {
	split := traefikService("prod", "split", map[string]any{"weighted": map[string]any{"services": []any{
		map[string]any{"name": "api", "port": int64(80), "weight": int64(3)},
		map[string]any{"name": "shadow", "kind": "TraefikService", "weight": int64(1)},
	}}})
	shadow := traefikService("prod", "shadow", map[string]any{"mirroring": map[string]any{"name": "orders", "port": int64(8080),
		"mirrors": []any{map[string]any{"name": "audit", "namespace": "sec", "port": int64(9000), "percent": int64(10)}}}})
	exists := func(ns, name string) bool { return name != "orders" }
	r := ConvertTraefikRoute(checkoutRoute(toTS("split")), model.SourceIngressRoute, exists, lookupOf(split, shadow))
	want := []model.Backend{
		{Namespace: "prod", Service: "api", Port: "80", Kind: "Service", State: model.BackendOK, Weight: model.Weight(750), Via: "prod/split"},
		{Namespace: "prod", Service: "orders", Port: "8080", Kind: "Service", State: model.BackendMissing, Weight: model.Weight(250), Via: "prod/split"},
		{Namespace: "sec", Service: "audit", Port: "9000", Kind: "Service", State: model.BackendOK, Mirror: true, Percent: 10, Via: "prod/split"},
	}
	if len(r.Rules) != len(want) {
		t.Fatalf("règles = %+v", r.Rules)
	}
	for i, w := range want {
		if !reflect.DeepEqual(r.Rules[i].Backend, w) || r.Rules[i].Host != "c.example.com" {
			t.Errorf("règle %d = %+v, attendu %+v", i, r.Rules[i], w)
		}
	}
	if got := routeBackends(r); len(got) != 3 {
		t.Errorf("Services atteints = %v", got)
	}
}

func TestConvertTraefikRouteWeightedServices(t *testing.T) {
	u := checkoutRoute(map[string]any{"name": "a", "port": int64(80)}, map[string]any{"name": "b", "port": int64(80), "weight": int64(3)})
	r := ConvertTraefikRoute(u, model.SourceIngressRoute, nil, nil)
	if w(r.Rules[0].Backend) != 250 || w(r.Rules[1].Backend) != 750 || r.Rules[0].Backend.Via != "" {
		t.Errorf("règles = %+v", r.Rules)
	}
	single := ConvertTraefikRoute(checkoutRoute(map[string]any{"name": "a"}), model.SourceIngressRoute, nil, nil)
	if single.Rules[0].Backend.Weight != nil {
		t.Errorf("un seul backend : poids %d", *single.Rules[0].Backend.Weight)
	}
	// TraefikService weighted dont un service a un poids 0 : publié avec un poids 0 explicite.
	zero := traefikService("prod", "zero", map[string]any{"weighted": map[string]any{"services": []any{
		map[string]any{"name": "a", "weight": int64(1)}, map[string]any{"name": "b", "weight": int64(0)},
	}}})
	r = ConvertTraefikRoute(checkoutRoute(toTS("zero")), model.SourceIngressRoute, nil, lookupOf(zero))
	if len(r.Rules) != 2 || w(r.Rules[0].Backend) != 1000 || r.Rules[1].Backend.Weight == nil || *r.Rules[1].Backend.Weight != 0 || r.Rules[1].Backend.Via != "prod/zero" {
		t.Errorf("poids 0 = %+v", r.Rules)
	}
}

// chain : TraefikService ts0 → ts1 → … → ts(n-1) → Service api.
func chain(n int) []*unstructured.Unstructured {
	var out []*unstructured.Unstructured
	for i := range n {
		next := toTS(fmt.Sprintf("ts%d", i+1))
		if i == n-1 {
			next = map[string]any{"name": "api", "port": int64(80)}
		}
		out = append(out, traefikService("prod", fmt.Sprintf("ts%d", i), map[string]any{"weighted": map[string]any{"services": []any{next}}}))
	}
	return out
}

func TestResolveTraefikServiceFailures(t *testing.T) {
	missing := model.Backend{Namespace: "prod", Service: "ts0", Kind: "TraefikService", State: model.BackendMissing}
	cases := []struct {
		name   string
		lookup TraefikLookup
		want   model.Backend
	}{
		{"profondeur 8", lookupOf(chain(8)...), model.Backend{Namespace: "prod", Service: "api", Port: "80", Kind: "Service", State: model.BackendOK, Via: "prod/ts0"}},
		{"profondeur 9", lookupOf(chain(9)...), missing},
		{"introuvable", lookupOf(), missing},
		{"sans résolveur", nil, missing},
		{"cycle", lookupOf(
			traefikService("prod", "ts0", map[string]any{"weighted": map[string]any{"services": []any{toTS("ts1")}}}),
			traefikService("prod", "ts1", map[string]any{"weighted": map[string]any{"services": []any{toTS("ts0")}}}),
		), missing},
		{"ni weighted ni mirroring", lookupOf(traefikService("prod", "ts0", map[string]any{})), missing},
	}
	for _, c := range cases {
		r := ConvertTraefikRoute(checkoutRoute(toTS("ts0")), model.SourceIngressRoute, nil, c.lookup)
		if len(r.Rules) != 1 || !reflect.DeepEqual(r.Rules[0].Backend, c.want) {
			t.Errorf("%s : %+v", c.name, r.Rules)
		}
	}
}

func TestConvertTraefikRouteTCPAndUDP(t *testing.T) {
	tcp := func(match string) *unstructured.Unstructured {
		return unstr("traefik.io/v1alpha1", "IngressRouteTCP", "prod", "pg", map[string]any{"entryPoints": []any{"postgres"},
			"routes": []any{map[string]any{"match": match, "services": []any{map[string]any{"name": "pg", "port": int64(5432)}}}}}, nil)
	}
	r := ConvertTraefikRoute(tcp("HostSNI(`db.example.com`)"), model.SourceIngressRouteTCP, nil, nil)
	if r.Source != model.SourceIngressRouteTCP || r.Group != "traefik.io" || r.Gate != "traefik" || r.Gates[0] != "traefik" ||
		r.Rules[0].Host != "db.example.com" || r.Rules[0].Path != "" || r.Rules[0].Match != "HostSNI(`db.example.com`)" || r.Rules[0].Backend.Port != "5432" {
		t.Errorf("TCP = %+v", r)
	}
	if h := ConvertTraefikRoute(tcp("HostSNI(`*`)"), model.SourceIngressRouteTCP, nil, nil).Rules[0].Host; h != "" {
		t.Errorf("HostSNI(*) : hôte %q", h)
	}
	udp := unstr("traefik.io/v1alpha1", "IngressRouteUDP", "kube-system", "dns", map[string]any{"entryPoints": []any{"dns"},
		"routes": []any{map[string]any{"services": []any{map[string]any{"name": "kube-dns", "port": int64(53)}}}}}, nil)
	r = ConvertTraefikRoute(udp, model.SourceIngressRouteUDP, nil, nil)
	if r.Source != model.SourceIngressRouteUDP || len(r.Rules) != 1 || r.Rules[0].Match != "" || r.Rules[0].Host != "" ||
		r.Rules[0].Backend.Service != "kube-dns" || r.Rules[0].Backend.Namespace != "kube-system" {
		t.Errorf("UDP = %+v", r)
	}
}

func TestTraefikRefs(t *testing.T) {
	svcs, ts := traefikRefs(checkoutRoute(map[string]any{"name": "api"}, toTS("split"), map[string]any{"name": "api"},
		map[string]any{"name": "auth", "namespace": "sso"}))
	if len(svcs) != 2 || svcs[0] != "prod/api" || svcs[1] != "sso/auth" || len(ts) != 1 || ts[0] != "prod/split" {
		t.Errorf("route : services %v, TraefikService %v", svcs, ts)
	}
	svcs, ts = traefikRefs(traefikService("prod", "shadow", map[string]any{"mirroring": map[string]any{"name": "orders",
		"mirrors": []any{map[string]any{"name": "audit"}, map[string]any{"name": "deep", "kind": "TraefikService"}}}}))
	if len(svcs) != 2 || svcs[0] != "prod/orders" || svcs[1] != "prod/audit" || len(ts) != 1 || ts[0] != "prod/deep" {
		t.Errorf("mirroring : services %v, TraefikService %v", svcs, ts)
	}
}
