package kube

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// unstr : objet unstructured de test (status omis si nil).
func unstr(apiVersion, kind, ns, name string, spec, status map[string]any) *unstructured.Unstructured {
	o := map[string]any{"apiVersion": apiVersion, "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": ns}, "spec": spec}
	if status != nil {
		o["status"] = status
	}
	return &unstructured.Unstructured{Object: o}
}

func condOf(typ, status, reason string) map[string]any {
	return map[string]any{"type": typ, "status": status, "reason": reason, "message": reason + " (test)"}
}

const gwAPI = gatewayGroup + "/v1"

// w : poids publié d'un backend, -1 s'il n'en a pas.
func w(b model.Backend) int {
	if b.Weight == nil {
		return -1
	}
	return *b.Weight
}

func TestConvertGateway(t *testing.T) {
	u := unstr(gwAPI, "Gateway", "infra", "public",
		map[string]any{"gatewayClassName": "eg", "listeners": []any{
			map[string]any{"name": "http", "protocol": "HTTP", "port": int64(80), "hostname": "*.example.com"},
			map[string]any{"name": "https", "protocol": "HTTPS", "port": int64(443)},
		}},
		map[string]any{
			"conditions": []any{condOf("Accepted", "True", "Accepted"), condOf("Programmed", "True", "Programmed")},
			"addresses":  []any{map[string]any{"type": "IPAddress", "value": "34.1.2.3"}},
			"listeners": []any{map[string]any{"name": "http", "attachedRoutes": int64(2),
				"conditions": []any{condOf("Programmed", "True", "Programmed")}}},
		})
	g := ConvertGateway(u)
	if g.Namespace != "infra" || g.Name != "public" || g.Class != "eg" || g.Accepted != model.CondTrue || g.Programmed != model.CondTrue ||
		g.Reason != "Programmed" || len(g.Addresses) != 1 || g.Addresses[0] != "34.1.2.3" {
		t.Fatalf("gateway = %+v", g)
	}
	want := []model.Listener{
		{Name: "http", Protocol: "HTTP", Port: 80, Hostname: "*.example.com", AttachedRoutes: 2, Ready: model.CondTrue},
		{Name: "https", Protocol: "HTTPS", Port: 443, Ready: model.CondUnknown},
	}
	if len(g.Listeners) != 2 || g.Listeners[0] != want[0] || g.Listeners[1] != want[1] {
		t.Errorf("listeners = %+v", g.Listeners)
	}
}

func TestConvertGatewayStatus(t *testing.T) {
	bare := ConvertGateway(unstr(gwAPI, "Gateway", "infra", "new", map[string]any{"gatewayClassName": "eg"}, nil))
	if bare.Accepted != model.CondUnknown || bare.Programmed != model.CondUnknown || bare.Listeners == nil || bare.Reason != "" {
		t.Errorf("sans contrôleur = %+v", bare)
	}
	down := ConvertGateway(unstr(gwAPI, "Gateway", "infra", "internal", map[string]any{"gatewayClassName": "eg"},
		map[string]any{"conditions": []any{condOf("Accepted", "True", "Accepted"), condOf("Programmed", "False", "AddressNotAssigned")}}))
	if down.Programmed != model.CondFalse || down.Reason != "AddressNotAssigned" || down.Message != "AddressNotAssigned (test)" {
		t.Errorf("non programmé = %+v", down)
	}
	// Sans condition Programmed, la raison vient d'Accepted.
	refused := ConvertGateway(unstr(gwAPI, "Gateway", "infra", "bad", map[string]any{"gatewayClassName": "eg"},
		map[string]any{"conditions": []any{condOf("Accepted", "False", "InvalidParameters")}}))
	if refused.Accepted != model.CondFalse || refused.Programmed != model.CondUnknown || refused.Reason != "InvalidParameters" {
		t.Errorf("refusé = %+v", refused)
	}
}

func parentStatus(ns, name string, conds ...any) map[string]any {
	return map[string]any{"parentRef": map[string]any{"name": name, "namespace": ns}, "conditions": conds}
}

func TestConvertHTTPRoute(t *testing.T) {
	u := unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{
		"parentRefs": []any{
			map[string]any{"name": "public", "namespace": "infra"},
			map[string]any{"name": "local"},
			map[string]any{"name": "mesh", "kind": "Service", "group": ""}, // maillage (GAMMA) : pas une porte
		},
		"hostnames": []any{"shop.example.com", "www.example.com"},
		"rules": []any{
			map[string]any{
				"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/api"}}},
				"backendRefs": []any{
					map[string]any{"name": "api", "port": int64(80), "weight": int64(9)},
					map[string]any{"name": "canary", "port": int64(80), "weight": int64(1)},
				}},
			map[string]any{"backendRefs": []any{map[string]any{"name": "web", "port": int64(8080)}}},
		},
	}, map[string]any{"parents": []any{
		parentStatus("infra", "public", condOf("Accepted", "True", "Accepted"), condOf("ResolvedRefs", "True", "ResolvedRefs")),
	}})
	exists := func(ns, name string) bool { return name != "canary" }
	r := ConvertGatewayRoute(u, model.SourceHTTPRoute, exists)
	if r.Source != model.SourceHTTPRoute || r.Group != gatewayGroup || r.Gate != "infra/public" ||
		len(r.Gates) != 2 || r.Gates[1] != "prod/local" || len(r.Rules) != 3 {
		t.Fatalf("route = %+v", r)
	}
	api, canary, web := r.Rules[0], r.Rules[1], r.Rules[2]
	if api.Host != "shop.example.com" || api.Path != "/api" || w(api.Backend) != 900 || api.Backend.Port != "80" ||
		api.Backend.Namespace != "prod" || api.Backend.Kind != "Service" || api.Backend.State != model.BackendOK {
		t.Errorf("api = %+v", api)
	}
	if w(canary.Backend) != 100 || canary.Backend.State != model.BackendMissing {
		t.Errorf("canary = %+v", canary)
	}
	if web.Backend.Weight != nil || web.Path != "" || web.Backend.State != model.BackendOK {
		t.Errorf("une règle à un seul backend n'a pas de poids : %+v", web)
	}
	wantParent := model.RouteParent{Gateway: "infra/public", Accepted: model.CondTrue, ResolvedRefs: model.CondTrue}
	if len(r.Parents) != 1 || r.Parents[0] != wantParent {
		t.Errorf("parents = %+v", r.Parents)
	}
	if got := routeBackends(r); len(got) != 3 || got[0] != "prod/api" {
		t.Errorf("routeBackends = %v", got)
	}
}

func TestConvertHTTPRouteZeroWeight(t *testing.T) {
	// Un backend de poids 0 (canary coupé) reste publié, avec un poids 0 explicite.
	u := unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{
		"parentRefs": []any{map[string]any{"name": "public", "namespace": "infra"}},
		"rules": []any{map[string]any{"backendRefs": []any{
			map[string]any{"name": "api", "port": int64(80), "weight": int64(1)},
			map[string]any{"name": "canary", "port": int64(80), "weight": int64(0)},
		}}},
	}, nil)
	r := ConvertGatewayRoute(u, model.SourceHTTPRoute, nil)
	if len(r.Rules) != 2 || w(r.Rules[0].Backend) != 1000 || r.Rules[1].Backend.Weight == nil || *r.Rules[1].Backend.Weight != 0 {
		t.Errorf("règles = %+v", r.Rules)
	}
}

func TestConvertHTTPRouteRefused(t *testing.T) {
	spec := func(parents ...any) map[string]any {
		return map[string]any{"parentRefs": parents,
			"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api", "port": int64(80)}}}}}
	}
	refused := []any{parentStatus("infra", "public", condOf("Accepted", "False", "NotAllowedByListeners"), condOf("ResolvedRefs", "True", "ResolvedRefs"))}
	r := ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "legacy",
		spec(map[string]any{"name": "public", "namespace": "infra"}), map[string]any{"parents": refused}), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendRefused || r.Parents[0].Reason != "NotAllowedByListeners" || r.Parents[0].Accepted != model.CondFalse {
		t.Errorf("route refusée = %+v", r)
	}
	// Refusée par un seul de ses deux Gateways : pas refusée.
	r = ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "legacy",
		spec(map[string]any{"name": "public", "namespace": "infra"}, map[string]any{"name": "local"}), map[string]any{"parents": refused}), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("refus partiel = %+v", r.Rules[0])
	}
}

func TestConvertHTTPRouteUnresolvedRefs(t *testing.T) {
	status := map[string]any{"parents": []any{parentStatus("infra", "public",
		condOf("Accepted", "True", "Accepted"), condOf("ResolvedRefs", "False", "RefNotPermitted"))}}
	parents := []any{map[string]any{"name": "public", "namespace": "infra"}}
	// Un backend d'un autre namespace (ReferenceGrant absente) : lui seul est manquant.
	r := ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{"parentRefs": parents,
		"rules": []any{map[string]any{"backendRefs": []any{
			map[string]any{"name": "api", "port": int64(80)},
			map[string]any{"name": "auth", "namespace": "sso", "port": int64(80)},
		}}}}, status), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendOK || r.Rules[1].Backend.State != model.BackendMissing || r.Parents[0].Reason != "RefNotPermitted" {
		t.Errorf("route = %+v", r)
	}
	// Aucun coupable identifiable : tous les backends sont manquants.
	r = ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{"parentRefs": parents,
		"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api", "port": int64(80)}}}}}, status), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendMissing {
		t.Errorf("route = %+v", r)
	}
}

func TestConvertHTTPRouteWithoutGateway(t *testing.T) {
	u := unstr(gwAPI, "HTTPRoute", "prod", "mc", map[string]any{
		"rules": []any{map[string]any{"backendRefs": []any{
			map[string]any{"name": "api", "kind": "ServiceImport", "group": "multicluster.x-k8s.io", "port": int64(80)},
		}}},
	}, nil)
	r := ConvertGatewayRoute(u, model.SourceHTTPRoute, func(string, string) bool { return true })
	if r.Gate != model.NoGateway || len(r.Gates) != 1 || r.Gates[0] != model.NoGateway || len(r.Parents) != 0 {
		t.Errorf("portes = %q %v", r.Gate, r.Gates)
	}
	if b := r.Rules[0].Backend; b.Kind != "ServiceImport.multicluster.x-k8s.io" || b.State != model.BackendMissing {
		t.Errorf("backend non-Service = %+v", b)
	}
	if got := routeBackends(r); len(got) != 0 {
		t.Errorf("un ServiceImport n'est pas un Service : %v", got)
	}
}

func TestConvertGRPCRoute(t *testing.T) {
	u := unstr(gwAPI, "GRPCRoute", "prod", "orders", map[string]any{
		"parentRefs": []any{map[string]any{"name": "public", "namespace": "infra"}},
		"hostnames":  []any{"grpc.example.com"},
		"rules": []any{
			map[string]any{"matches": []any{map[string]any{"method": map[string]any{"service": "orders.v1.Orders", "method": "Create"}}},
				"backendRefs": []any{map[string]any{"name": "orders", "port": int64(9090)}}},
			map[string]any{"matches": []any{map[string]any{"method": map[string]any{"service": "orders.v1.Admin"}}},
				"backendRefs": []any{map[string]any{"name": "admin", "port": int64(9090)}}},
		},
	}, nil)
	r := ConvertGatewayRoute(u, model.SourceGRPCRoute, nil)
	if r.Source != model.SourceGRPCRoute || len(r.Rules) != 2 || r.Rules[0].Host != "grpc.example.com" ||
		r.Rules[0].Match != "orders.v1.Orders/Create" || r.Rules[0].Path != "" || r.Rules[1].Match != "orders.v1.Admin" {
		t.Errorf("route = %+v", r)
	}
}

func TestConvertHTTPRouteSameGatewayTwice(t *testing.T) {
	gw := []any{map[string]any{"name": "public", "namespace": "infra"}}
	spec := map[string]any{"parentRefs": gw,
		"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api", "port": int64(0)}}}}}
	acc := parentStatus("infra", "public", condOf("Accepted", "True", "Accepted"))
	ref := parentStatus("infra", "public", condOf("Accepted", "False", "NotAllowedByListeners"))
	for _, order := range [][]any{{acc, ref}, {ref, acc}} {
		r := ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "shop", spec, map[string]any{"parents": order}), model.SourceHTTPRoute, nil)
		if r.Rules[0].Backend.State != model.BackendOK || len(r.Parents) != 2 || r.Rules[0].Backend.Port != "" {
			t.Errorf("deux entrées, une acceptée : %+v", r)
		}
	}
	r := ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "shop", spec, map[string]any{"parents": []any{ref, ref}}), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendRefused {
		t.Errorf("toutes refusées = %+v", r)
	}
}
