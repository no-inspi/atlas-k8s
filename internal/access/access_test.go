package access

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	authzv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// policy : alice (groupe oidc:sre) voit tout ; bob (oidc:dev) ne voit que production.
func fakeClient(calls *atomic.Int32, fail bool) *fake.Clientset {
	c := fake.NewClientset()
	c.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		calls.Add(1)
		sar := a.(k8stesting.CreateAction).GetObject().(*authzv1.SubjectAccessReview)
		if fail {
			return true, nil, context.DeadlineExceeded
		}
		ra := sar.Spec.ResourceAttributes
		allowed := false
		for _, g := range sar.Spec.Groups {
			if g == "oidc:sre" {
				allowed = true
			}
		}
		if sar.Spec.User == "bob" && ra.Namespace == "production" && ra.Resource != "nodes" {
			allowed = true
		}
		hasAuth := false
		for _, g := range sar.Spec.Groups {
			hasAuth = hasAuth || g == "system:authenticated"
		}
		if !hasAuth {
			allowed = false
		}
		sar.Status.Allowed = allowed
		return true, sar, nil
	})
	return c
}

var (
	alice = User{Name: "alice", Groups: []string{"oidc:sre"}}
	bob   = User{Name: "bob", Groups: []string{"oidc:dev"}}
)

func TestReviewerAndCache(t *testing.T) {
	var calls atomic.Int32
	r := NewReviewer(fakeClient(&calls, false))
	now := time.Now()
	r.now = func() time.Time { return now }
	ctx := context.Background()
	podsIn := func(ns string) Attributes { return Attributes{Verb: "list", Resource: "pods", Namespace: ns} }

	if !r.Allowed(ctx, bob, podsIn("production")) || r.Allowed(ctx, bob, podsIn("kube-system")) {
		t.Fatal("décisions de bob incorrectes")
	}
	if !r.Allowed(ctx, alice, podsIn("kube-system")) {
		t.Fatal("alice devrait voir kube-system")
	}
	before := calls.Load()
	r.Allowed(ctx, bob, podsIn("production"))
	if calls.Load() != before {
		t.Error("la décision aurait dû venir du cache")
	}
	now = now.Add(61 * time.Second)
	r.Allowed(ctx, bob, podsIn("production"))
	if calls.Load() != before+1 {
		t.Error("le cache doit expirer après 60 s")
	}
}

func TestReviewerDeniesOnError(t *testing.T) {
	var calls atomic.Int32
	r := NewReviewer(fakeClient(&calls, true))
	if r.Allowed(context.Background(), alice, Attributes{Verb: "list", Resource: "pods", Namespace: "x"}) {
		t.Fatal("une erreur d'API ne doit jamais autoriser")
	}
}

func TestStreamFilter(t *testing.T) {
	var calls atomic.Int32
	r := NewReviewer(fakeClient(&calls, false))
	f := NewStreamFilter(r, bob)
	ctx := context.Background()
	cases := []struct {
		kind stream.Kind
		obj  any
		want bool
	}{
		{stream.KindPod, model.Pod{Namespace: "production"}, true},
		{stream.KindPod, model.Pod{Namespace: "kube-system"}, false},
		{stream.KindService, model.Service{Namespace: "production"}, true},
		{stream.KindService, model.Service{Namespace: "kube-system"}, false},
		{stream.KindRoute, model.Route{Source: model.SourceIngress, Group: "networking.k8s.io", Namespace: "production"}, true},
		{stream.KindRoute, model.Route{Source: model.SourceIngressRoute, Group: "traefik.io", Namespace: "kube-system"}, false},
		{stream.KindVolume, model.Volume{Namespace: "production"}, true},
		{stream.KindVolume, model.Volume{Namespace: "kube-system"}, false},
		{stream.KindWorkload, model.Workload{Kind: "Deployment", Namespace: "production"}, true},
		{stream.KindWorkload, model.Workload{Kind: "Job", Namespace: "kube-system"}, false},
		{stream.KindNamespace, model.Namespace{Name: "production"}, true},
		{stream.KindNamespace, model.Namespace{Name: "kube-system"}, false},
		{stream.KindNode, model.Node{Name: "n1"}, false},
	}
	for _, c := range cases {
		if got := f.Allow(ctx, c.kind, c.obj); got != c.want {
			t.Errorf("%s %+v : %v, attendu %v", c.kind, c.obj, got, c.want)
		}
	}
	if f.Changed(ctx) {
		t.Error("aucun changement de droits attendu")
	}
}

func TestStreamFilterDetectsChangedRights(t *testing.T) {
	c := fake.NewClientset()
	var allow atomic.Bool
	c.PrependReactor("create", "subjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		sar := a.(k8stesting.CreateAction).GetObject().(*authzv1.SubjectAccessReview)
		sar.Status.Allowed = allow.Load()
		return true, sar, nil
	})
	r := NewReviewer(c)
	now := time.Now()
	r.now = func() time.Time { return now }
	f := NewStreamFilter(r, bob)
	ctx := context.Background()
	if f.Allow(ctx, stream.KindPod, model.Pod{Namespace: "staging"}) {
		t.Fatal("refus attendu au départ")
	}
	allow.Store(true)
	if f.Changed(ctx) {
		t.Error("tant que le cache est valide, rien ne change")
	}
	now = now.Add(61 * time.Second)
	if !f.Changed(ctx) {
		t.Error("le nouveau droit doit être détecté")
	}
}

func TestBatchAccessReview(t *testing.T) {
	c := fake.NewClientset()
	c.PrependReactor("create", "selfsubjectaccessreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		s := a.(k8stesting.CreateAction).GetObject().(*authzv1.SelfSubjectAccessReview)
		ra := s.Spec.ResourceAttributes
		s.Status.Allowed = ra.Verb == "get" || (ra.Verb == "delete" && ra.Namespace == "production")
		if !s.Status.Allowed {
			s.Status.Reason = "RBAC: refusé"
		}
		return true, s, nil
	})
	res, err := Review(context.Background(), c, []Check{
		{Verb: "get", Resource: "pods", Subresource: "log", Namespace: "staging"},
		{Verb: "delete", Resource: "pods", Namespace: "production", Name: "x"},
		{Verb: "delete", Resource: "pods", Namespace: "staging"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].Allowed || !res[1].Allowed || res[2].Allowed || !strings.Contains(res[2].Reason, "refusé") {
		t.Errorf("résultats = %+v", res)
	}
	if _, err := Review(context.Background(), c, make([]Check, MaxChecks+1)); err == nil {
		t.Error("un lot trop gros doit être refusé")
	}
}

func TestClientsImpersonate(t *testing.T) {
	c := NewClients(&rest.Config{Host: "https://k8s.example"})
	a, err := c.Kube(alice)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.Kube(alice)
	if a != b {
		t.Error("le client d'un même utilisateur doit être réutilisé")
	}
	cfg := c.configFor(alice)
	if cfg.Impersonate.UserName != "alice" || len(cfg.Impersonate.Groups) != 1 || cfg.Impersonate.Groups[0] != "oidc:sre" {
		t.Errorf("impersonation = %+v", cfg.Impersonate)
	}
}

func TestNetworkAttributes(t *testing.T) {
	cases := []struct {
		obj  any
		want Attributes
	}{
		{model.Service{Namespace: "a"}, Attributes{Verb: "list", Resource: "services", Namespace: "a"}},
		{model.Route{Source: model.SourceIngress, Group: "networking.k8s.io", Namespace: "a"}, Attributes{Verb: "list", Group: "networking.k8s.io", Resource: "ingresses", Namespace: "a"}},
		{model.Route{Source: model.SourceIngressRoute, Group: "traefik.containo.us", Namespace: "a"}, Attributes{Verb: "list", Group: "traefik.containo.us", Resource: "ingressroutes", Namespace: "a"}},
		{model.Volume{Namespace: "a"}, Attributes{Verb: "list", Resource: "persistentvolumeclaims", Namespace: "a"}},
		{model.Route{Source: model.SourceIngressRouteTCP, Group: "traefik.io", Namespace: "a"}, Attributes{Verb: "list", Group: "traefik.io", Resource: "ingressroutetcps", Namespace: "a"}},
		{model.Route{Source: model.SourceIngressRouteUDP, Group: "traefik.containo.us", Namespace: "a"}, Attributes{Verb: "list", Group: "traefik.containo.us", Resource: "ingressrouteudps", Namespace: "a"}},
		{model.Route{Source: model.SourceHTTPRoute, Group: "gateway.networking.k8s.io", Namespace: "a"}, Attributes{Verb: "list", Group: "gateway.networking.k8s.io", Resource: "httproutes", Namespace: "a"}},
		{model.Route{Source: model.SourceGRPCRoute, Group: "gateway.networking.k8s.io", Namespace: "a"}, Attributes{Verb: "list", Group: "gateway.networking.k8s.io", Resource: "grpcroutes", Namespace: "a"}},
		{model.Gateway{Namespace: "a"}, Attributes{Verb: "list", Group: "gateway.networking.k8s.io", Resource: "gateways", Namespace: "a"}},
		{model.PersistentVolume{Name: "pv"}, Attributes{Verb: "list", Resource: "persistentvolumes"}},
	}
	for _, c := range cases {
		if got, ok := attributes("", c.obj); !ok || got != c.want {
			t.Errorf("%+v : %+v, attendu %+v", c.obj, got, c.want)
		}
	}
	if _, ok := attributes("", model.Route{Source: "Mystère", Namespace: "a"}); ok {
		t.Error("route de source inconnue acceptée")
	}
	if _, ok := attributes("", model.Route{Source: model.SourceHTTPRoute, Namespace: "a"}); ok {
		t.Error("route sans groupe acceptée")
	}
}

func TestStreamFilterGatewaysAndVolumes(t *testing.T) {
	var calls atomic.Int32
	f := NewStreamFilter(NewReviewer(fakeClient(&calls, false)), bob)
	ctx := context.Background()
	if f.Allow(ctx, stream.KindPersistentVolume, model.PersistentVolume{Name: "pv-1"}) {
		t.Error("bob, sans droit sur le cluster, ne doit voir aucun PV")
	}
	if !NewStreamFilter(NewReviewer(fakeClient(&calls, false)), alice).Allow(ctx, stream.KindPersistentVolume, model.PersistentVolume{Name: "pv-1"}) {
		t.Error("alice, qui voit tout, doit voir les PV")
	}
	if f.Allow(ctx, stream.KindGateway, model.Gateway{Namespace: "kube-system", Name: "gw"}) {
		t.Error("bob ne doit voir aucun Gateway de kube-system")
	}
	if !f.Allow(ctx, stream.KindGateway, model.Gateway{Namespace: "production", Name: "gw"}) ||
		!f.Allow(ctx, stream.KindRoute, model.Route{Source: model.SourceHTTPRoute, Group: "gateway.networking.k8s.io", Namespace: "production"}) {
		t.Error("bob doit voir les Gateways et les HTTPRoute de production")
	}
}
