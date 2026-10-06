package stream

import (
	"testing"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

func newTestHub(history int) *Hub {
	return NewHub(Options{History: history, BaseRev: 0})
}

func pod(uid, status string) model.Pod {
	return model.Pod{UID: uid, Name: "pod-" + uid, Namespace: "ns", DisplayStatus: status}
}

func recv(t *testing.T, s *Subscription) []Message {
	t.Helper()
	select {
	case b, ok := <-s.C:
		if !ok {
			t.Fatal("abonnement fermé")
		}
		return b
	default:
		t.Fatal("aucun lot reçu")
		return nil
	}
}

func TestSnapshotContainsSortedObjects(t *testing.T) {
	h := newTestHub(16)
	init, sub := h.Subscribe(0)
	defer sub.Close()
	if len(init) != 1 || init[0].Type != "snapshot" || len(init[0].Pods) != 0 {
		t.Fatalf("snapshot initial inattendu : %+v", init)
	}

	h.Upsert(KindPod, "b", pod("b", "Running"))
	h.Upsert(KindPod, "a", pod("a", "Running"))
	h.Upsert(KindNode, "n1", model.Node{Name: "n1"})
	h.Upsert(KindWorkload, "Deployment/ns/w", model.Workload{Kind: "Deployment", Name: "w"})
	h.Upsert(KindNamespace, "prod", model.Namespace{Name: "prod", Color: "#123456"})
	h.Flush()

	init, sub2 := h.Subscribe(0)
	defer sub2.Close()
	snap := init[0]
	if snap.Type != "snapshot" || snap.Rev != h.Rev() {
		t.Fatalf("snapshot = %+v, rev courant %d", snap, h.Rev())
	}
	if len(snap.Pods) != 2 || snap.Pods[0].UID != "a" || snap.Pods[1].UID != "b" {
		t.Errorf("pods non triés : %+v", snap.Pods)
	}
	if len(snap.Namespaces) != 1 || snap.Namespaces[0].Color != "#123456" {
		t.Errorf("namespaces manquants : %+v", snap.Namespaces)
	}
	if len(snap.Nodes) != 1 || len(snap.Workloads) != 1 {
		t.Errorf("nodes/workloads manquants : %+v", snap)
	}
}

func TestDeltasAreCoalescedPerObject(t *testing.T) {
	h := newTestHub(16)
	_, sub := h.Subscribe(0)
	defer sub.Close()

	h.Upsert(KindPod, "a", pod("a", "Pending"))
	h.Upsert(KindPod, "a", pod("a", "Running"))
	h.Upsert(KindPod, "b", pod("b", "Running"))
	h.Delete(KindPod, "b", pod("b", "Running"))
	h.Flush()

	batch := recv(t, sub)
	if len(batch) != 2 {
		t.Fatalf("attendu 2 messages, reçu %d : %+v", len(batch), batch)
	}
	if batch[0].Type != "upsert" || batch[0].Obj.(model.Pod).DisplayStatus != "Running" {
		t.Errorf("premier message = %+v", batch[0])
	}
	if batch[1].Type != "delete" || batch[1].Kind != KindPod {
		t.Errorf("second message = %+v", batch[1])
	}
	if batch[0].Rev != 1 || batch[1].Rev != 2 {
		t.Errorf("revs = %d, %d", batch[0].Rev, batch[1].Rev)
	}
}

func TestEmptyFlushSendsNothing(t *testing.T) {
	h := newTestHub(16)
	_, sub := h.Subscribe(0)
	defer sub.Close()
	h.Flush()
	select {
	case b := <-sub.C:
		t.Fatalf("lot inattendu : %+v", b)
	default:
	}
}

func TestResubscribeReplaysMissingDeltas(t *testing.T) {
	h := newTestHub(16)
	h.Upsert(KindPod, "a", pod("a", "Running"))
	h.Flush()
	last := h.Rev()
	h.Upsert(KindPod, "b", pod("b", "Running"))
	h.Flush()

	init, sub := h.Subscribe(last)
	defer sub.Close()
	if len(init) != 1 || init[0].Type != "upsert" || init[0].Rev != last+1 {
		t.Fatalf("rejeu attendu du seul delta %d, reçu %+v", last+1, init)
	}

	init, sub2 := h.Subscribe(h.Rev())
	defer sub2.Close()
	if len(init) != 0 {
		t.Fatalf("client à jour : rien à rejouer, reçu %+v", init)
	}
}

func TestResubscribeOutsideHistoryGetsSnapshot(t *testing.T) {
	h := newTestHub(2)
	for _, uid := range []string{"a", "b", "c", "d"} {
		h.Upsert(KindPod, uid, pod(uid, "Running"))
		h.Flush()
	}
	init, sub := h.Subscribe(1)
	defer sub.Close()
	if len(init) != 1 || init[0].Type != "snapshot" || len(init[0].Pods) != 4 {
		t.Fatalf("snapshot attendu, reçu %+v", init)
	}

	// Client en avance (serveur redémarré) : snapshot aussi.
	init, sub2 := h.Subscribe(h.Rev() + 50)
	defer sub2.Close()
	if len(init) != 1 || init[0].Type != "snapshot" {
		t.Fatalf("snapshot attendu pour un rev futur, reçu %+v", init)
	}
}

func TestSlowSubscriberIsDroppedWithoutBlocking(t *testing.T) {
	h := NewHub(Options{History: 16, SubscriberBuffer: 4})
	_, sub := h.Subscribe(0)
	for i := 0; i < 10; i++ {
		h.Upsert(KindPod, "a", pod("a", "Running"))
		h.Flush()
	}
	n := 0
	for range sub.C {
		n++
	}
	if n != 4 {
		t.Errorf("attendu 4 lots avant fermeture, reçu %d", n)
	}
	sub.Close() // idempotent
}

func TestMetricsAreBroadcastAndReplayed(t *testing.T) {
	h := newTestHub(16)
	_, sub := h.Subscribe(0)
	defer sub.Close()
	m := model.Metrics{Pods: map[string]model.Usage{"a": {CPU: 120}}, Nodes: map[string]model.Usage{}}
	h.SetMetrics(m)
	batch := recv(t, sub)
	if len(batch) != 1 || batch[0].Type != "metrics" || batch[0].Metrics.Pods["a"].CPU != 120 {
		t.Fatalf("metrics = %+v", batch)
	}

	init, sub2 := h.Subscribe(0)
	defer sub2.Close()
	if len(init) != 2 || init[1].Type != "metrics" {
		t.Fatalf("snapshot puis metrics attendus, reçu %+v", init)
	}
}

func TestStats(t *testing.T) {
	h := newTestHub(16)
	_, sub := h.Subscribe(0)
	h.Upsert(KindPod, "a", pod("a", "Running"))
	h.Flush()
	h.CountSent(3)
	st := h.Stats()
	if st.Clients != 1 || st.Rev != 1 || st.MessagesSent != 3 {
		t.Errorf("stats = %+v", st)
	}
	sub.Close()
	if h.Stats().Clients != 0 {
		t.Error("client fermé encore compté")
	}
}

func TestReady(t *testing.T) {
	h := newTestHub(16)
	if h.Ready() {
		t.Fatal("hub prêt avant MarkReady")
	}
	h.MarkReady()
	if !h.Ready() {
		t.Fatal("hub non prêt après MarkReady")
	}
}
