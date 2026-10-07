package kube

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

var irGVR = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "ingressroutes"}

func TestIngressRoutesFromTraefik(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	client.Resources = []*metav1.APIResourceList{{GroupVersion: "traefik.io/v1alpha1",
		APIResources: []metav1.APIResource{{Name: "ingressroutes", Kind: "IngressRoute", Namespaced: true}}}}
	ir := ingressRoute("traefik.io", "prod", "admin", []any{map[string]any{"match": "Host(`admin.example.com`)",
		"services": []any{map[string]any{"name": "api", "port": int64(80)}, map[string]any{"name": "ghost", "port": int64(80)}}}})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{irGVR: "IngressRouteList"}, ir)

	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	o, ok := sk.get(stream.KindRoute, "IngressRoute/prod/admin")
	if !ok {
		t.Fatal("IngressRoute absente")
	}
	r := o.(model.Route)
	if r.Gate != "traefik" || r.Group != "traefik.io" || r.Rules[0].Host != "admin.example.com" ||
		r.Rules[0].Backend.State != model.BackendOK || r.Rules[1].Backend.State != model.BackendMissing {
		t.Fatalf("route = %+v", r)
	}

	if err := dyn.Resource(irGVR).Namespace("prod").Delete(context.Background(), "admin", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute supprimée", func() bool { _, ok := sk.get(stream.KindRoute, "IngressRoute/prod/admin"); return !ok })
}

func TestNoTraefikWithoutCRD(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{irGVR: "IngressRouteList"})
	_, sk := startSourceWith(t, fake.NewClientset(netFixtures()...), Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, "Ingress/prod/storefront"); !ok {
		t.Error("la source doit fonctionner sans CRD Traefik")
	}
}
