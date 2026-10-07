package kube

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

func TestKindStartsWhenItsCRDIsInstalled(t *testing.T) {
	dyn := fakeDynamic(adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, fake.NewClientset(netFixtures()...), Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, adminID); ok {
		t.Fatal("IngressRoute publiée sans CRD")
	}
	crd := crdObject(kindsOf(irGVR)[0], true)
	if _, err := dyn.Resource(gvrCRD).Create(context.Background(), crd, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute publiée après l'installation de la CRD", func() bool {
		_, ok := sk.get(stream.KindRoute, adminID)
		return ok
	})
}

func TestKindStopsWhenItsCRDIsRemoved(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	k := kindsOf(irGVR)[0]
	dyn := servedDyn(client, []dynKind{k}, adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, adminID); !ok {
		t.Fatal("IngressRoute absente")
	}
	if err := dyn.Resource(gvrCRD).Delete(context.Background(), k.crd(), metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute retirée avec sa CRD", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return !ok })
}

func TestKindStopsWhenItsVersionIsNoLongerServed(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	k := kindsOf(irGVR)[0]
	dyn := servedDyn(client, []dynKind{k}, adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if _, err := dyn.Resource(gvrCRD).Update(context.Background(), crdObject(k, false), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute retirée", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return !ok })
	// La version servie de nouveau : le type redémarre.
	if _, err := dyn.Resource(gvrCRD).Update(context.Background(), crdObject(k, true), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute revenue", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return ok })
}

func TestForbiddenCRDsFallBackToDiscovery(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api"))
	dyn.PrependReactor("list", "customresourcedefinitions", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, "", errors.New("refusé"))
	})
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, adminID); !ok {
		t.Fatal("la découverte au démarrage doit servir de repli")
	}
}

func TestSlimCRDKeepsOnlyServedVersions(t *testing.T) {
	crd := crdObject(kindsOf(irGVR)[0], true)
	crd.Object["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)["schema"] = map[string]any{"openAPIV3Schema": map[string]any{"type": "object"}}
	o, _ := slimCRD(crd)
	u := o.(*unstructured.Unstructured)
	if u.GetName() != "ingressroutes.traefik.io" || !crdServes(u, "v1alpha1") || crdServes(u, "v1") {
		t.Fatalf("crd = %+v", u.Object)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "group"); found {
		t.Error("le schéma et le reste de la spec doivent être retirés")
	}
}

// failingLists fait échouer (500) les list d'un groupe tant que down vaut vrai,
// et les compte.
func failingLists(dyn *dynamicfake.FakeDynamicClient, group string, down *atomic.Bool) *atomic.Int32 {
	var n atomic.Int32
	dyn.PrependReactor("list", "ingressroutes", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource().Group != group {
			return false, nil, nil
		}
		n.Add(1)
		if down.Load() {
			return true, nil, apierrors.NewInternalError(errors.New("API server indisponible"))
		}
		return false, nil, nil
	})
	return &n
}

func TestTransientFailureIsRetried(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api"))
	var down atomic.Bool
	down.Store(true)
	lists := failingLists(dyn, "traefik.io", &down)
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn,
		dynSync: 100 * time.Millisecond, dynRetry: 20 * time.Millisecond, dynWait: 200 * time.Millisecond})
	if _, ok := sk.get(stream.KindRoute, adminID); ok {
		t.Fatal("IngressRoute publiée malgré l'erreur")
	}
	// Chaque essai fait au moins la sonde : plusieurs list, plusieurs essais.
	eventually(t, "nouveaux essais", func() bool { return lists.Load() >= 4 })
	down.Store(false)
	eventually(t, "IngressRoute publiée après le retour de l'API server", func() bool {
		_, ok := sk.get(stream.KindRoute, adminID)
		return ok
	})
}

func TestRetryStopsWhenCRDIsRemoved(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	k := kindsOf(irGVR)[0]
	dyn := servedDyn(client, []dynKind{k}, adminRoute("traefik.io", "api"))
	var down atomic.Bool
	down.Store(true)
	lists := failingLists(dyn, "traefik.io", &down)
	startSourceWith(t, client, Options{Dynamic: dyn,
		dynSync: 50 * time.Millisecond, dynRetry: 10 * time.Millisecond, dynWait: 100 * time.Millisecond})
	eventually(t, "nouveaux essais", func() bool { return lists.Load() >= 4 })
	if err := dyn.Resource(gvrCRD).Delete(context.Background(), k.crd(), metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // l'essai en cours se termine
	n := lists.Load()
	time.Sleep(300 * time.Millisecond)
	if got := lists.Load(); got != n {
		t.Fatalf("essais poursuivis après la suppression de la CRD : %d list de plus", got-n)
	}
}

func TestServedKindsStartInParallel(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	old := gvrIngressRoute("traefik.containo.us")
	dyn := servedDyn(client, kindsOf(irGVR, old), adminRoute("traefik.containo.us", "api"))
	var down atomic.Bool
	down.Store(true)
	failingLists(dyn, "traefik.io", &down) // démarré le premier, ne se synchronise jamais
	start := time.Now()
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn, dynSync: 10 * time.Second, dynWait: 300 * time.Millisecond})
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("prêt après %v : l'attente globale doit borner le démarrage", d)
	}
	sk.mu.Lock()
	o, ok := sk.atReady[stream.KindRoute][adminID]
	sk.mu.Unlock()
	if !ok || o.(model.Route).Group != "traefik.containo.us" {
		t.Fatalf("le type synchronisé à temps doit être dans le premier snapshot : %v %+v", ok, o)
	}
}

func TestRunStopsWithoutReadyWhenCancelledDuringDynamicStart(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api"))
	var down atomic.Bool
	down.Store(true)
	lists := failingLists(dyn, "traefik.io", &down)
	sk := newSink()
	src := NewSource(client, sk, Options{Dynamic: dyn, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		ReconcileInterval: 10 * time.Millisecond, dynSync: 10 * time.Second, dynWait: 10 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- src.Run(ctx) }()
	eventually(t, "démarrage des types dynamiques", func() bool { return lists.Load() >= 1 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run ne s'arrête pas")
	}
	sk.mu.Lock()
	defer sk.mu.Unlock()
	if sk.ready || len(sk.objs) != 0 {
		t.Fatalf("rien ne doit être publié après l'annulation : prêt=%v objets=%v", sk.ready, sk.objs)
	}
}

// gateFirstList : le premier list des IngressRoute traefik.io attend release,
// puis échoue avec err ; les suivants passent. Le client factice reste
// verrouillé pendant l'attente : ne pas l'appeler avant release.
// Hypothèse : sans CRD au démarrage, aucun list de ce type n'a lieu avant
// l'ADD de la CRD ; le premier list est donc la sonde (probe) de l'essai que
// lance cet ADD, et non le list de l'informer. Si le démarrage listait ce type
// sans CRD (découverte, autre sonde), la porte se refermerait sur le mauvais
// appel et le test ne reproduirait plus la course.
func gateFirstList(dyn *dynamicfake.FakeDynamicClient, err error) (entered <-chan struct{}, release chan<- struct{}) {
	in, out := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	dyn.PrependReactor("list", "ingressroutes", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetResource().Group != "traefik.io" || !first.CompareAndSwap(false, true) {
			return false, nil, nil
		}
		close(in)
		<-out
		return true, nil, err
	})
	return in, out
}

// Une CRD toute neuve : l'ADD lance un essai dont la sonde reçoit un 404 (CRD
// pas encore servie), et l'UPDATE (Established) arrive pendant cet essai. Il
// doit valoir un nouvel essai, sans attendre dynRetry ni une autre modification.
func TestCRDEventDuringStartRetriesAfterNotFound(t *testing.T) {
	k := kindsOf(irGVR)[0]
	dyn := fakeDynamic(adminRoute("traefik.io", "api"))
	entered, release := gateFirstList(dyn, apierrors.NewNotFound(irGVR.GroupResource(), ""))
	_, sk := startSourceWith(t, fake.NewClientset(netFixtures()...), Options{Dynamic: dyn, dynRetry: time.Hour})
	crd := crdObject(k, true)
	if _, err := dyn.Resource(gvrCRD).Create(context.Background(), crd, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered: // ADD : l'essai est en cours, sa sonde attend
	case <-time.After(3 * time.Second):
		t.Fatal("aucun essai après l'installation de la CRD")
	}
	lastSource.syncCRD(context.Background(), crd) // UPDATE (Established) pendant l'essai
	close(release)                                // la sonde finit en 404
	eventually(t, "IngressRoute publiée après le 404 passager", func() bool {
		_, ok := sk.get(stream.KindRoute, adminID)
		return ok
	})
}

// Un événement de CRD reçu pendant l'attente d'un nouvel essai l'avance.
func TestCRDEventCutsRetryWait(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	k := kindsOf(irGVR)[0]
	dyn := servedDyn(client, []dynKind{k}, adminRoute("traefik.io", "api"))
	var down atomic.Bool
	down.Store(true)
	failingLists(dyn, "traefik.io", &down)
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn,
		dynSync: 500 * time.Millisecond, dynRetry: time.Hour, dynAgain: 50 * time.Millisecond, dynWait: 100 * time.Millisecond})
	for _, first := range lastSource.wantedFirsts() {
		select {
		case <-first: // premier essai en échec : la source attend dynRetry
		case <-time.After(3 * time.Second):
			t.Fatal("premier essai sans fin")
		}
	}
	down.Store(false)
	lastSource.syncCRD(context.Background(), crdObject(k, true))
	eventually(t, "IngressRoute publiée sans attendre dynRetry", func() bool {
		_, ok := sk.get(stream.KindRoute, adminID)
		return ok
	})
}

// Une CRD modifiée en continu pendant que l'API server échoue : chaque
// événement relance un essai, mais pas plus d'un par dynAgain.
func TestCRDEventsAfterTransientFailureAreThrottled(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	k := kindsOf(irGVR)[0]
	dyn := servedDyn(client, []dynKind{k}, adminRoute("traefik.io", "api"))
	var down atomic.Bool
	down.Store(true)
	lists := failingLists(dyn, "traefik.io", &down)
	const again = 200 * time.Millisecond
	startSourceWith(t, client, Options{Dynamic: dyn,
		dynSync: 10 * time.Millisecond, dynRetry: time.Hour, dynAgain: again, dynWait: 50 * time.Millisecond})
	for _, first := range lastSource.wantedFirsts() {
		<-first
	}
	n := lists.Load()
	const flood = time.Second
	for end := time.Now().Add(flood); time.Now().Before(end); time.Sleep(time.Millisecond) {
		lastSource.syncCRD(context.Background(), crdObject(k, true))
	}
	// Un essai = la sonde et au plus quelques list de l'informer.
	tries := int32(flood/again) + 1
	got := lists.Load() - n
	if got < 2 {
		t.Fatalf("les événements doivent relancer des essais : %d list", got)
	}
	if got > 3*tries {
		t.Fatalf("%d list en %v : au plus un essai par %v attendu", got, flood, again)
	}
}
