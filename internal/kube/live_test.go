//go:build integration

package kube

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// liveContext : contexte kube des tests d'intégration (ATLAS_CONTEXT, kind-atlas
// par défaut). Ces tests créent des objets et suppriment des CRD : un contexte
// n'est accepté que s'il s'appelle kind-… ET que son API server, lu dans le
// kubeconfig (mêmes règles de chargement que kubectl), est en boucle locale.
// À lire une fois par test, avant tout t.Cleanup qui en dépend.
func liveContext(t *testing.T) string {
	t.Helper()
	c := os.Getenv("ATLAS_CONTEXT")
	if c == "" {
		c = "kind-atlas"
	}
	if !strings.HasPrefix(c, "kind-") {
		t.Fatalf("ATLAS_CONTEXT=%q refusé : les tests d'intégration ne visent qu'un cluster kind (kind-…)", c)
	}
	raw, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		t.Skipf("kubeconfig illisible : %v", err)
	}
	kc, ok := raw.Contexts[c]
	if !ok {
		t.Skipf("pas de contexte %q dans le kubeconfig : make kind-up", c)
	}
	cl, ok := raw.Clusters[kc.Cluster]
	if !ok {
		t.Fatalf("contexte %q refusé : cluster %q absent du kubeconfig", c, kc.Cluster)
	}
	u, err := url.Parse(cl.Server)
	if err != nil {
		t.Fatalf("contexte %q refusé : serveur %q illisible : %v", c, cl.Server, err)
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
	default:
		t.Fatalf("contexte %q refusé : serveur %q hors boucle locale (pas un cluster kind)", c, cl.Server)
	}
	return c
}

// startLive branche une source et un hub sur le cluster du contexte kctx (liveContext).
func startLive(t *testing.T, kctx string) (*kubernetes.Clientset, *stream.Subscription, context.Context) {
	t.Helper()
	rc, err := RestConfig("", kctx)
	if err != nil {
		t.Skipf("pas de cluster : %v", err)
	}
	client := kubernetes.NewForConfigOrDie(rc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	hub := stream.NewHub(stream.Options{})
	go hub.Run(ctx)
	src := NewSource(client, hub, Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Dynamic: dynamic.NewForConfigOrDie(rc)})
	go func() { _ = src.Run(ctx) }()
	for !hub.Ready() {
		select {
		case <-ctx.Done():
			t.Fatal("source jamais prête : cluster injoignable ?")
		case <-time.After(50 * time.Millisecond):
		}
	}
	_, sub := hub.Subscribe(hub.Rev())
	t.Cleanup(sub.Close)
	return client, sub, ctx
}

// kubectl lance kubectl sur le contexte kctx (liveContext) ; stdin : manifeste éventuel (« -f - »).
func kubectl(t *testing.T, kctx, stdin string, args ...string) {
	t.Helper()
	cmd := exec.Command("kubectl", append([]string{"--context", kctx}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kubectl %s : %v\n%s", strings.Join(args, " "), err, out)
	}
}

// restore : commande kubectl de remise en état sur kctx, pour les t.Cleanup :
// journalisée en cas d'erreur, sans t.Fatal.
func restore(t *testing.T, kctx string, args ...string) {
	t.Helper()
	cmd := exec.Command("kubectl", append([]string{"--context", kctx}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("remise en état : kubectl %s : %v\n%s", strings.Join(args, " "), err, out)
	}
}

// manifest : manifeste de CRD mis en cache par make crds (chemin dans la variable env).
func manifest(t *testing.T, env string) string {
	t.Helper()
	p := os.Getenv(env)
	if p == "" {
		t.Skipf("%s absent : lancer par make test-integration", env)
	}
	return p
}

// isRoute : message de type typ (upsert, delete) pour une route donnée, qui satisfait ok si présent.
func isRoute(typ, source, ns, name string, ok func(model.Route) bool) func(stream.Message) bool {
	return func(m stream.Message) bool {
		r, is := m.Obj.(model.Route)
		return m.Type == typ && is && r.Source == source && r.Namespace == ns && r.Name == name && (ok == nil || ok(r))
	}
}

// backends : backends d'une route par nom de Service.
func backends(r model.Route) map[string]model.Backend {
	out := map[string]model.Backend{}
	for _, x := range r.Rules {
		out[x.Backend.Service] = x.Backend
	}
	return out
}

// waitFor attend un message diffusé qui satisfait match et renvoie le délai écoulé.
func waitFor(t *testing.T, sub *stream.Subscription, what string, match func(stream.Message) bool) time.Duration {
	t.Helper()
	start := time.Now()
	for {
		select {
		case batch := <-sub.C:
			for _, m := range batch {
				if match(m) {
					return time.Since(start)
				}
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s : rien reçu en 10 s", what)
		}
	}
}

// TestLiveLatency vérifie le critère d'acceptation « un pod créé, supprimé ou
// qui change de statut apparaît dans la vue en moins de 2 s » sur un vrai
// cluster (make kind-up). Mesure jusqu'au lot diffusé par le hub, regroupement
// de 250 ms compris.
func TestLiveLatency(t *testing.T) {
	client, sub, ctx := startLive(t, liveContext(t))

	const ns = "atlas-it"
	_, _ = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	t.Cleanup(func() { _ = client.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })

	isPod := func(typ, status string) func(stream.Message) bool {
		return func(m stream.Message) bool {
			p, ok := m.Obj.(model.Pod)
			return m.Type == typ && ok && p.Namespace == ns && p.Name == "probe" && (status == "" || p.DisplayStatus == status)
		}
	}

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: ns},
		Spec: corev1.PodSpec{TerminationGracePeriodSeconds: new(int64), Containers: []corev1.Container{{Name: "c", Image: "registry.k8s.io/pause:3.10"}}}}
	start := time.Now()
	if _, err := client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	created := waitFor(t, sub, "création", isPod("upsert", ""))
	t.Logf("création visible en %v", created)
	if created > 2*time.Second {
		t.Errorf("création visible en %v (> 2 s)", created)
	}

	_ = waitFor(t, sub, "Running", isPod("upsert", "Running"))
	t.Logf("Running visible %v après la création (inclut le démarrage du container)", time.Since(start))

	if err := client.CoreV1().Pods(ns).Delete(ctx, "probe", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	gone := waitFor(t, sub, "suppression", isPod("delete", ""))
	t.Logf("suppression visible en %v", gone)
	if gone > 2*time.Second {
		t.Errorf("suppression visible en %v (> 2 s)", gone)
	}
}

// TestLiveServiceEndpoints : supprimer un pod endpoint met à jour son Service en moins de 2 s.
func TestLiveServiceEndpoints(t *testing.T) {
	client, sub, ctx := startLive(t, liveContext(t))
	const ns = "atlas-it-svc"
	_, _ = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	t.Cleanup(func() { _ = client.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })

	labels := map[string]string{"app": "probe"}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: ns},
		Spec: corev1.ServiceSpec{Selector: labels, Ports: []corev1.ServicePort{{Port: 80}}}}
	if _, err := client.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: ns, Labels: labels},
		Spec: corev1.PodSpec{TerminationGracePeriodSeconds: new(int64), Containers: []corev1.Container{{Name: "c", Image: "registry.k8s.io/pause:3.10"}}}}
	if _, err := client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	endpoints := func(n int) func(stream.Message) bool {
		return func(m stream.Message) bool {
			s, ok := m.Obj.(model.Service)
			return m.Type == "upsert" && ok && s.Namespace == ns && s.Name == "probe" && len(s.Endpoints) == n
		}
	}
	_ = waitFor(t, sub, "endpoint ajouté", endpoints(1))
	if err := client.CoreV1().Pods(ns).Delete(ctx, "probe", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	gone := waitFor(t, sub, "endpoint retiré", endpoints(0))
	t.Logf("endpoint retiré visible en %v", gone)
	if gone > 2*time.Second {
		t.Errorf("endpoint retiré visible en %v (> 2 s)", gone)
	}
}

const gatewayFixture = `
apiVersion: v1
kind: Namespace
metadata: { name: atlas-it-gw }
---
apiVersion: v1
kind: Service
metadata: { name: web, namespace: atlas-it-gw }
spec: { selector: { app: web }, ports: [{ port: 80 }] }
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: gw, namespace: atlas-it-gw }
spec:
  gatewayClassName: atlas-it
  listeners: [{ name: http, protocol: HTTP, port: 80 }]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: web, namespace: atlas-it-gw }
spec:
  parentRefs: [{ name: gw }]
  rules:
    - backendRefs:
        - { name: web, port: 80, weight: 90 }
        - { name: web-canary, port: 80, weight: 10 }
`

const gatewayProgrammed = `{"status":{"conditions":[` +
	`{"type":"Accepted","status":"True","reason":"Accepted","message":"","lastTransitionTime":"2026-10-07T00:00:00Z","observedGeneration":1},` +
	`{"type":"Programmed","status":"True","reason":"Programmed","message":"","lastTransitionTime":"2026-10-07T00:00:00Z","observedGeneration":1}]}}`

// TestLiveGatewayCRDHot : Atlas démarre sans la Gateway API ; une HTTPRoute
// créée après l'installation des CRD est diffusée en moins de 5 s, le statut
// écrit sur son Gateway aussi, et supprimer la CRD retire la route du flux.
//
// Supprimer les CRD Gateway API emporte TOUS les objets Gateway API du cluster
// kind (GatewayClass, Gateways, HTTPRoute, GRPCRoute, ReferenceGrant), pas
// seulement ceux du test : seuls les scénarios (hack/scenarios-gateway/) sont
// recréés à la fin, tout autre objet Gateway API ajouté à la main est perdu.
func TestLiveGatewayCRDHot(t *testing.T) {
	crds := manifest(t, "GATEWAY_API_CRDS")
	kctx := liveContext(t)
	t.Cleanup(func() {
		restore(t, kctx, "delete", "namespace", "atlas-it-gw", "--ignore-not-found", "--wait=true")
		restore(t, kctx, "apply", "--server-side", "-f", crds)
		restore(t, kctx, "wait", "--for", "condition=established", "--timeout=60s",
			"crd/gatewayclasses.gateway.networking.k8s.io", "crd/gateways.gateway.networking.k8s.io",
			"crd/httproutes.gateway.networking.k8s.io", "crd/grpcroutes.gateway.networking.k8s.io")
		restore(t, kctx, "apply", "-f", "../../hack/scenarios-gateway/gateways.yaml")
		if out, err := exec.Command("../../hack/scenarios-gateway/status.sh", kctx).CombinedOutput(); err != nil {
			t.Logf("remise en état : status.sh : %v\n%s", err, out)
		}
	})
	kubectl(t, kctx, "", "delete", "--ignore-not-found", "--wait=true", "-f", crds)
	_, sub, _ := startLive(t, kctx)

	kubectl(t, kctx, "", "apply", "--server-side", "-f", crds)
	kubectl(t, kctx, "", "wait", "--for", "condition=established", "--timeout=60s",
		"crd/gateways.gateway.networking.k8s.io", "crd/httproutes.gateway.networking.k8s.io")
	// Chronomètres partis AVANT kubectl : waitFor ne commence à lire le flux
	// qu'après le retour de kubectl, et le message peut déjà être diffusé ;
	// le délai mesuré majore donc celui vu par un client (aller-retour de
	// kubectl compris), ce qui rend le seuil de 5 s plus strict, pas plus lâche.
	start := time.Now()
	kubectl(t, kctx, gatewayFixture, "apply", "-f", "-")
	_ = waitFor(t, sub, "HTTPRoute après l'installation des CRD", isRoute("upsert", model.SourceHTTPRoute, "atlas-it-gw", "web", func(r model.Route) bool {
		b := backends(r)
		return len(r.Gates) == 1 && r.Gates[0] == "atlas-it-gw/gw" &&
			w(b["web"]) == 900 && w(b["web-canary"]) == 100 && b["web-canary"].State == model.BackendMissing
	}))
	visible := time.Since(start)
	t.Logf("HTTPRoute visible %v après sa création (CRD installées après le démarrage)", visible)
	if visible > 5*time.Second {
		t.Errorf("HTTPRoute visible en %v (> 5 s)", visible)
	}

	// Pas de contrôleur dans kind : le statut est écrit à la main.
	kubectl(t, kctx, "", "-n", "atlas-it-gw", "patch", "gateway", "gw", "--subresource=status", "--type=merge", "-p", gatewayProgrammed)
	_ = waitFor(t, sub, "Gateway programmé", func(m stream.Message) bool {
		g, ok := m.Obj.(model.Gateway)
		return m.Type == "upsert" && ok && g.Namespace == "atlas-it-gw" && g.Name == "gw" && g.Programmed == model.CondTrue
	})

	start = time.Now()
	kubectl(t, kctx, "", "delete", "crd", "httproutes.gateway.networking.k8s.io", "--wait=true")
	_ = waitFor(t, sub, "route retirée avec sa CRD", isRoute("delete", model.SourceHTTPRoute, "atlas-it-gw", "web", nil))
	gone := time.Since(start)
	t.Logf("route retirée %v après la suppression de la CRD", gone)
	if gone > 5*time.Second {
		t.Errorf("route retirée en %v (> 5 s)", gone)
	}
}

const traefikFixture = `
apiVersion: v1
kind: Namespace
metadata: { name: atlas-it-traefik }
---
apiVersion: v1
kind: Service
metadata: { name: a, namespace: atlas-it-traefik }
spec: { selector: { app: a }, ports: [{ port: 80 }] }
---
apiVersion: v1
kind: Service
metadata: { name: b, namespace: atlas-it-traefik }
spec: { selector: { app: b }, ports: [{ port: 80 }] }
---
apiVersion: traefik.io/v1alpha1
kind: TraefikService
metadata: { name: split, namespace: atlas-it-traefik }
spec:
  weighted:
    services:
      - { name: a, port: 80, weight: 3 }
      - { name: b, port: 80, weight: 1 }
---
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata: { name: split, namespace: atlas-it-traefik }
spec:
  routes:
    - match: Host(` + "`split.localtest.me`" + `)
      kind: Rule
      services: [{ name: split, kind: TraefikService }]
`

// orphanPV : PV libéré propre au test ; atlas-it-released appartient aux
// scénarios (hack/scenarios/30-storage.yaml) et n'est pas touché.
const orphanPV = `
apiVersion: v1
kind: PersistentVolume
metadata: { name: atlas-it-orphan }
spec:
  capacity: { storage: 1Gi }
  accessModes: [ReadWriteOnce]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: manual
  claimRef: { namespace: atlas-it-traefik, name: gone, uid: 00000000-0000-0000-0000-000000000002 }
  hostPath: { path: /tmp/atlas-it-orphan }
`

// TestLiveTraefikServiceAndPV : la CRD des TraefikService installée après le
// démarrage est prise en compte, une IngressRoute qui en vise un se résout en
// Services pondérés en moins de 5 s, et un PV sans PVC est publié, Released. Les
// scénarios Traefik (make scenarios) sont remis en place à la fin.
func TestLiveTraefikServiceAndPV(t *testing.T) {
	crds := manifest(t, "TRAEFIK_CRDS")
	kctx := liveContext(t)
	t.Cleanup(func() {
		restore(t, kctx, "delete", "namespace", "atlas-it-traefik", "--ignore-not-found", "--wait=true")
		restore(t, kctx, "delete", "pv", "atlas-it-orphan", "--ignore-not-found")
		restore(t, kctx, "apply", "--server-side", "-f", crds)
		restore(t, kctx, "wait", "--for", "condition=established", "--timeout=60s", "crd/traefikservices.traefik.io")
		restore(t, kctx, "apply", "-f", "../../hack/scenarios-traefik/")
	})
	kubectl(t, kctx, "", "delete", "crd", "traefikservices.traefik.io", "--ignore-not-found", "--wait=true")
	_, sub, _ := startLive(t, kctx)

	kubectl(t, kctx, "", "apply", "--server-side", "-f", crds)
	kubectl(t, kctx, "", "wait", "--for", "condition=established", "--timeout=60s", "crd/traefikservices.traefik.io")
	// Chronomètre parti avant kubectl : voir TestLiveGatewayCRDHot.
	start := time.Now()
	kubectl(t, kctx, traefikFixture, "apply", "-f", "-")
	_ = waitFor(t, sub, "IngressRoute via un TraefikService", isRoute("upsert", model.SourceIngressRoute, "atlas-it-traefik", "split", func(r model.Route) bool {
		b := backends(r)
		return w(b["a"]) == 750 && w(b["b"]) == 250 && b["a"].Via == "atlas-it-traefik/split" && b["a"].State == model.BackendOK
	}))
	resolved := time.Since(start)
	t.Logf("route résolue %v après sa création", resolved)
	if resolved > 5*time.Second {
		t.Errorf("route résolue en %v (> 5 s)", resolved)
	}

	// Le contrôleur de PV passe le volume en Released (son claimRef vise un
	// PVC absent) : le flux doit porter cette phase.
	start = time.Now()
	kubectl(t, kctx, orphanPV, "apply", "-f", "-")
	_ = waitFor(t, sub, "PV sans PVC, Released", func(m stream.Message) bool {
		p, ok := m.Obj.(model.PersistentVolume)
		return m.Type == "upsert" && ok && p.Name == "atlas-it-orphan" && p.ClaimRef == "atlas-it-traefik/gone" && p.Phase == "Released"
	})
	released := time.Since(start)
	t.Logf("PV Released visible %v après sa création", released)
	if released > 5*time.Second {
		t.Errorf("PV Released visible en %v (> 5 s)", released)
	}
}
