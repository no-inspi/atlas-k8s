package kube

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

var irGVR = gvrIngressRoute("traefik.io")

// fakeDynamic : client dynamique factice qui sait lister les CRD et tous les types du registre.
// Les objets d'un type du registre sont rangés sous sa ressource : le client
// factice devinerait sinon le pluriel (« gatewaies » pour Gateway).
func fakeDynamic(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	kinds := map[schema.GroupVersionResource]string{gvrCRD: "CustomResourceDefinitionList"}
	for _, k := range dynKinds {
		kinds[k.gvr] = k.gvr.Resource + "List"
	}
	var rest []runtime.Object
	type placed struct {
		gvr schema.GroupVersionResource
		u   *unstructured.Unstructured
	}
	var known []placed
	for _, o := range objs {
		if u, ok := o.(*unstructured.Unstructured); ok {
			if gvr, ok := registryGVR(u); ok {
				known = append(known, placed{gvr, u})
				continue
			}
		}
		rest = append(rest, o)
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, rest...)
	for _, p := range known {
		if err := dyn.Tracker().Create(p.gvr, p.u, p.u.GetNamespace()); err != nil {
			panic(err)
		}
	}
	return dyn
}

// registryGVR : type du registre d'un objet (groupe, version, kind en minuscules suivi de « s »).
// Le pluriel « kind + s » est une hypothèse : à revoir si le registre accueille
// un type au pluriel irrégulier (kind en -y, -s…), dont les objets passeraient
// sinon par la devinette du client factice.
func registryGVR(u *unstructured.Unstructured) (schema.GroupVersionResource, bool) {
	gvk := u.GroupVersionKind()
	for _, k := range dynKinds {
		if k.gvr.GroupVersion() == gvk.GroupVersion() && k.gvr.Resource == strings.ToLower(gvk.Kind)+"s" {
			return k.gvr, true
		}
	}
	return schema.GroupVersionResource{}, false
}

// servedDyn : client dynamique factice d'un cluster qui sert les types donnés :
// CRD installées, et déclarées à la découverte (repli sans droit sur les CRD).
// Ne jamais lui passer deux fois le même type (la CRD serait créée deux fois).
func servedDyn(client *fake.Clientset, served []dynKind, objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	byGV := map[string][]metav1.APIResource{}
	for _, k := range served {
		gv := k.gvr.GroupVersion().String()
		byGV[gv] = append(byGV[gv], metav1.APIResource{Name: k.gvr.Resource, Namespaced: true})
		objs = append(objs, crdObject(k, true))
	}
	for gv, rs := range byGV {
		client.Resources = append(client.Resources, &metav1.APIResourceList{GroupVersion: gv, APIResources: rs})
	}
	return fakeDynamic(objs...)
}

// crdObject : CRD factice du type ; served=false : installée, version attendue non servie.
func crdObject(k dynKind, served bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": k.crd()},
		"spec": map[string]any{"group": k.gvr.Group, "versions": []any{
			map[string]any{"name": k.gvr.Version, "served": served, "storage": true}}},
	}}
}

// kindsOf : entrées du registre pour ces ressources, dans l'ordre donné.
func kindsOf(gvrs ...schema.GroupVersionResource) []dynKind {
	var out []dynKind
	for _, g := range gvrs {
		for _, k := range dynKinds {
			if k.gvr == g {
				out = append(out, k)
			}
		}
	}
	return out
}

func adminRoute(group string, services ...string) *unstructured.Unstructured {
	var svcs []any
	for _, s := range services {
		svcs = append(svcs, map[string]any{"name": s, "port": int64(80)})
	}
	return ingressRoute(group, "prod", "admin", []any{map[string]any{"match": "Host(`admin.example.com`)", "services": svcs}})
}

const adminID = "IngressRoute/prod/admin"

func TestIngressRoutesFromTraefik(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api", "ghost"))

	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	o, ok := sk.get(stream.KindRoute, adminID)
	if !ok {
		t.Fatal("IngressRoute absente")
	}
	r := o.(model.Route)
	if r.Gate != "traefik" || len(r.Gates) != 1 || r.Group != "traefik.io" || r.Rules[0].Host != "admin.example.com" ||
		r.Rules[0].Backend.State != model.BackendOK || r.Rules[1].Backend.State != model.BackendMissing {
		t.Fatalf("route = %+v", r)
	}

	if err := dyn.Resource(irGVR).Namespace("prod").Delete(context.Background(), "admin", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute supprimée", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return !ok })
}

func TestNoTraefikWithoutCRD(t *testing.T) {
	_, sk := startSourceWith(t, fake.NewClientset(netFixtures()...), Options{Dynamic: fakeDynamic()})
	if _, ok := sk.get(stream.KindRoute, "Ingress/prod/storefront"); !ok {
		t.Error("la source doit fonctionner sans CRD Traefik")
	}
}

func TestTraefikIOWinsOverContainous(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	old := gvrIngressRoute("traefik.containo.us")
	dyn := servedDyn(client, kindsOf(irGVR, old), adminRoute("traefik.containo.us", "api"), adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if o, ok := sk.get(stream.KindRoute, adminID); !ok || o.(model.Route).Group != "traefik.io" {
		t.Fatalf("route = %v %+v", ok, o)
	}
	// Sans traefik.io, l'ancien groupe prend le relais.
	lastSource.stopDyn(irGVR)
	eventually(t, "repli sur traefik.containo.us", func() bool {
		o, ok := sk.get(stream.KindRoute, adminID)
		return ok && o.(model.Route).Group == "traefik.containo.us"
	})
}

func TestStoppedKindLeavesTheStream(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, adminID); !ok {
		t.Fatal("IngressRoute absente")
	}
	lastSource.stopDyn(irGVR)
	eventually(t, "IngressRoute retirée", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return !ok })
	if lastSource.dynIndexer(irGVR) != nil {
		t.Error("type toujours inscrit")
	}
	if !lastSource.startDyn(context.Background(), kindsOf(irGVR)[0]) {
		t.Fatal("redémarrage refusé")
	}
	eventually(t, "IngressRoute revenue", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return ok })
}

func TestServiceDeletedTurnsIngressRouteBackendMissing(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if err := client.CoreV1().Services("prod").Delete(context.Background(), "api", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backend manquant", func() bool {
		o, _ := sk.get(stream.KindRoute, adminID)
		return o.(model.Route).Rules[0].Backend.State == model.BackendMissing
	})
}
