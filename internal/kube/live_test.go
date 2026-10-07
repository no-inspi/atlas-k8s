//go:build integration

package kube

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// startLive branche une source et un hub sur le cluster kind (ATLAS_CONTEXT, kind-atlas par défaut).
func startLive(t *testing.T) (*kubernetes.Clientset, *stream.Subscription, context.Context) {
	t.Helper()
	ctxName := os.Getenv("ATLAS_CONTEXT")
	if ctxName == "" {
		ctxName = "kind-atlas"
	}
	rc, err := RestConfig("", ctxName)
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
		time.Sleep(50 * time.Millisecond)
	}
	_, sub := hub.Subscribe(hub.Rev())
	t.Cleanup(sub.Close)
	return client, sub, ctx
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
	client, sub, ctx := startLive(t)

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
	client, sub, ctx := startLive(t)
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
