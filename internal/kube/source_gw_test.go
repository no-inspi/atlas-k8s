package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// gwObjects : un Gateway programmé, une HTTPRoute 9:1 vers api et canary
// (absent), un TraefikService 3:1 vers les mêmes, l'IngressRoute checkout qui
// le vise et une IngressRouteTCP.
func gwObjects() []runtime.Object {
	return []runtime.Object{
		unstr(gwAPI, "Gateway", "infra", "public", map[string]any{"gatewayClassName": "eg",
			"listeners": []any{map[string]any{"name": "http", "protocol": "HTTP", "port": int64(80)}}},
			map[string]any{"conditions": []any{condOf("Programmed", "True", "Programmed")}}),
		unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{
			"parentRefs": []any{map[string]any{"name": "public", "namespace": "infra"}},
			"rules": []any{map[string]any{"backendRefs": []any{
				map[string]any{"name": "api", "port": int64(80), "weight": int64(9)},
				map[string]any{"name": "canary", "port": int64(80), "weight": int64(1)},
			}}}}, nil),
		traefikService("prod", "split", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "api", "port": int64(80), "weight": int64(3)},
			map[string]any{"name": "canary", "port": int64(80), "weight": int64(1)},
		}}}),
		checkoutRoute(toTS("split")),
		unstr("traefik.io/v1alpha1", "IngressRouteTCP", "prod", "pg", map[string]any{"routes": []any{
			map[string]any{"match": "HostSNI(`*`)", "services": []any{map[string]any{"name": "api", "port": int64(5432)}}}}}, nil),
	}
}

var gwGVRs = []schema.GroupVersionResource{gvrTraefikService("traefik.io"), gvrGateway, gvrHTTPRoute, irGVR, gvrIngressRouteTCP("traefik.io")}

func routeOf(t *testing.T, sk *sink, id string) model.Route {
	t.Helper()
	o, ok := sk.get(stream.KindRoute, id)
	if !ok {
		t.Fatalf("route %s absente", id)
	}
	return o.(model.Route)
}

func startGateways(t *testing.T) (*fake.Clientset, *sink, context.Context) {
	t.Helper()
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(gwGVRs...), gwObjects()...)
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	eventually(t, "objets dynamiques publiés", func() bool {
		_, g := sk.get(stream.KindGateway, "infra/public")
		_, r := sk.get(stream.KindRoute, "IngressRoute/prod/checkout")
		_, tcp := sk.get(stream.KindRoute, "IngressRouteTCP/prod/pg")
		return g && r && tcp
	})
	return client, sk, context.Background()
}

func TestGatewayAPIAndTraefikInSource(t *testing.T) {
	client, sk, ctx := startGateways(t)
	dyn := lastSource.opts.Dynamic

	g, _ := sk.get(stream.KindGateway, "infra/public")
	if g.(model.Gateway).Programmed != model.CondTrue || g.(model.Gateway).Class != "eg" {
		t.Errorf("gateway = %+v", g)
	}
	shop := routeOf(t, sk, "HTTPRoute/prod/shop")
	if shop.Gate != "infra/public" || w(shop.Rules[0].Backend) != 900 || shop.Rules[0].Backend.State != model.BackendOK ||
		w(shop.Rules[1].Backend) != 100 || shop.Rules[1].Backend.State != model.BackendMissing {
		t.Errorf("HTTPRoute = %+v", shop)
	}
	co := routeOf(t, sk, "IngressRoute/prod/checkout")
	if len(co.Rules) != 2 || co.Rules[0].Backend.Service != "api" || w(co.Rules[0].Backend) != 750 || co.Rules[0].Backend.Via != "prod/split" ||
		w(co.Rules[1].Backend) != 250 || co.Rules[1].Backend.State != model.BackendMissing {
		t.Errorf("IngressRoute via TraefikService = %+v", co.Rules)
	}
	if pg := routeOf(t, sk, "IngressRouteTCP/prod/pg"); pg.Gate != "traefik" || pg.Rules[0].Host != "" || pg.Rules[0].Backend.Port != "5432" {
		t.Errorf("IngressRouteTCP = %+v", pg)
	}

	// Le Service canary apparaît : la HTTPRoute et l'IngressRoute (par le TraefikService) le trouvent.
	if _, err := client.CoreV1().Services("prod").Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "canary", Namespace: "prod"},
		Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "canary"}}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "canary résolu", func() bool {
		return routeOf(t, sk, "HTTPRoute/prod/shop").Rules[1].Backend.State == model.BackendOK &&
			routeOf(t, sk, "IngressRoute/prod/checkout").Rules[1].Backend.State == model.BackendOK
	})

	// Le TraefikService passe à 1:1 : l'IngressRoute suit.
	tsGVR := gvrTraefikService("traefik.io")
	ts, err := dyn.Resource(tsGVR).Namespace("prod").Get(ctx, "split", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(ts.Object, []any{map[string]any{"name": "api", "port": int64(80)},
		map[string]any{"name": "canary", "port": int64(80)}}, "spec", "weighted", "services"); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(tsGVR).Namespace("prod").Update(ctx, ts, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "poids 500/500", func() bool {
		r := routeOf(t, sk, "IngressRoute/prod/checkout")
		return w(r.Rules[0].Backend) == 500 && w(r.Rules[1].Backend) == 500
	})

	// Le Gateway disparaît : retiré du flux ; la route garde sa porte.
	if err := dyn.Resource(gvrGateway).Namespace("infra").Delete(ctx, "public", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Gateway retiré", func() bool { _, ok := sk.get(stream.KindGateway, "infra/public"); return !ok })
	if r := routeOf(t, sk, "HTTPRoute/prod/shop"); r.Gate != "infra/public" {
		t.Errorf("porte de la route = %q", r.Gate)
	}
}

func TestTraefikServiceKindStoppedTurnsRouteMissing(t *testing.T) {
	_, sk, _ := startGateways(t)
	lastSource.stopDyn(gvrTraefikService("traefik.io"))
	eventually(t, "TraefikService manquant", func() bool {
		r := routeOf(t, sk, "IngressRoute/prod/checkout")
		return len(r.Rules) == 1 && r.Rules[0].Backend.Kind == "TraefikService" && r.Rules[0].Backend.State == model.BackendMissing
	})
}

// Un TraefikService imbriqué change : la route qui l'atteint par un autre suit.
// Un cycle de TraefikService ne bloque pas la remontée.
func TestNestedTraefikServiceChangeReachesRoute(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	tsGVR := gvrTraefikService("traefik.io")
	objs := []runtime.Object{
		traefikService("prod", "outer", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "split", "kind": "TraefikService"}}}}),
		traefikService("prod", "split", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "api", "port": int64(80)}}}}),
		traefikService("prod", "loop-a", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "loop-b", "kind": "TraefikService"}}}}),
		traefikService("prod", "loop-b", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "loop-a", "kind": "TraefikService"}}}}),
		checkoutRoute(toTS("outer"), toTS("loop-a")),
	}
	dyn := servedDyn(client, kindsOf(tsGVR, irGVR), objs...)
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	ctx := context.Background()
	eventually(t, "route publiée", func() bool { _, ok := sk.get(stream.KindRoute, "IngressRoute/prod/checkout"); return ok })
	r := routeOf(t, sk, "IngressRoute/prod/checkout")
	if len(r.Rules) != 2 || r.Rules[0].Backend.Service != "api" || r.Rules[0].Backend.Via != "prod/outer" ||
		r.Rules[1].Backend.Service != "loop-a" || r.Rules[1].Backend.State != model.BackendMissing {
		t.Fatalf("règles = %+v", r.Rules)
	}

	split, err := dyn.Resource(tsGVR).Namespace("prod").Get(ctx, "split", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(split.Object, []any{map[string]any{"name": "api", "port": int64(80)},
		map[string]any{"name": "canary", "port": int64(80)}}, "spec", "weighted", "services"); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(tsGVR).Namespace("prod").Update(ctx, split, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "canary atteint par outer", func() bool {
		r := routeOf(t, sk, "IngressRoute/prod/checkout")
		return len(r.Rules) == 3 && r.Rules[1].Backend.Service == "canary" && r.Rules[1].Backend.State == model.BackendMissing
	})

	// Le cycle : une mise à jour de loop-b remonte jusqu'à la route sans boucler.
	b, err := dyn.Resource(tsGVR).Namespace("prod").Get(ctx, "loop-b", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(b.Object, []any{map[string]any{"name": "api", "port": int64(80)}}, "spec", "weighted", "services"); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(tsGVR).Namespace("prod").Update(ctx, b, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "cycle rompu", func() bool {
		r := routeOf(t, sk, "IngressRoute/prod/checkout")
		last := r.Rules[len(r.Rules)-1].Backend
		return last.Service == "api" && last.Via == "prod/loop-a" && last.State == model.BackendOK
	})
}

// Type démarré tard : la CRD TraefikService est installée après les routes et
// les TraefikService ; la route passe de missing à ok.
func TestTraefikServiceKindStartedLate(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	tsGVR := gvrTraefikService("traefik.io")
	dyn := servedDyn(client, kindsOf(irGVR),
		traefikService("prod", "outer", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "split", "kind": "TraefikService"}}}}),
		traefikService("prod", "split", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "api", "port": int64(80)}}}}),
		checkoutRoute(toTS("outer")))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	eventually(t, "route publiée", func() bool { _, ok := sk.get(stream.KindRoute, "IngressRoute/prod/checkout"); return ok })
	if r := routeOf(t, sk, "IngressRoute/prod/checkout"); len(r.Rules) != 1 || r.Rules[0].Backend.State != model.BackendMissing {
		t.Fatalf("avant la CRD : %+v", r.Rules)
	}
	if _, err := dyn.Resource(gvrCRD).Create(context.Background(), crdObject(kindsOf(tsGVR)[0], true), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "route résolue après la CRD", func() bool {
		r := routeOf(t, sk, "IngressRoute/prod/checkout")
		return len(r.Rules) == 1 && r.Rules[0].Backend.Service == "api" && r.Rules[0].Backend.Via == "prod/outer" &&
			r.Rules[0].Backend.State == model.BackendOK
	})
}

// GRPCRoute, et routes Traefik de l'ancien groupe traefik.containo.us
// (IngressRouteUDP, IngressRoute vers un TraefikService du même groupe).
func TestGRPCRouteAndContainoTraefikInSource(t *testing.T) {
	const old = "traefik.containo.us"
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(gvrGRPCRoute, gvrTraefikService(old), gvrIngressRoute(old), gvrIngressRouteUDP(old)),
		unstr(gwAPI, "GRPCRoute", "prod", "orders", map[string]any{
			"parentRefs": []any{map[string]any{"name": "public", "namespace": "infra"}},
			"rules":      []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api", "port": int64(9000)}}}}}, nil),
		unstr(old+"/v1alpha1", "TraefikService", "prod", "split", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "api", "port": int64(80), "weight": int64(1)},
			map[string]any{"name": "ghost", "port": int64(80), "weight": int64(1)},
		}}}, nil),
		ingressRoute(old, "prod", "legacy", []any{map[string]any{"match": "Host(`l.example.com`)", "services": []any{toTS("split")}}}),
		unstr(old+"/v1alpha1", "IngressRouteUDP", "prod", "dns", map[string]any{"routes": []any{
			map[string]any{"services": []any{map[string]any{"name": "api", "port": int64(53)}}}}}, nil),
	)
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	eventually(t, "routes publiées", func() bool {
		_, g := sk.get(stream.KindRoute, "GRPCRoute/prod/orders")
		_, l := sk.get(stream.KindRoute, "IngressRoute/prod/legacy")
		_, u := sk.get(stream.KindRoute, "IngressRouteUDP/prod/dns")
		return g && l && u
	})
	if g := routeOf(t, sk, "GRPCRoute/prod/orders"); g.Gate != "infra/public" || g.Rules[0].Backend.State != model.BackendOK || g.Rules[0].Backend.Port != "9000" {
		t.Errorf("GRPCRoute = %+v", g)
	}
	if l := routeOf(t, sk, "IngressRoute/prod/legacy"); len(l.Rules) != 2 || l.Rules[0].Backend.Via != "prod/split" ||
		w(l.Rules[0].Backend) != 500 || l.Rules[1].Backend.State != model.BackendMissing {
		t.Errorf("IngressRoute containo.us = %+v", l.Rules)
	}
	if u := routeOf(t, sk, "IngressRouteUDP/prod/dns"); u.Gate != "traefik" || u.Group != old || u.Rules[0].Backend.Port != "53" ||
		u.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("IngressRouteUDP = %+v", u)
	}
}
