// Package stream diffuse l'état du cluster aux navigateurs : un snapshot à la
// connexion, puis des deltas regroupés et numérotés (rev) qu'un client peut
// rejouer à la reconnexion.
package stream

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/no-inspi/cluster-atlas/internal/model"
)

type Kind string

const (
	KindNode      Kind = "node"
	KindPod       Kind = "pod"
	KindWorkload  Kind = "workload"
	KindNamespace Kind = "namespace"
)

// Message est le format JSON de /api/stream.
type Message struct {
	Type       string            `json:"type"` // snapshot | upsert | delete | metrics
	Rev        uint64            `json:"rev,omitempty"`
	Kind       Kind              `json:"kind,omitempty"`
	Obj        any               `json:"obj,omitempty"`
	Nodes      []model.Node      `json:"nodes,omitempty"`
	Pods       []model.Pod       `json:"pods,omitempty"`
	Workloads  []model.Workload  `json:"workloads,omitempty"`
	Namespaces []model.Namespace `json:"namespaces,omitempty"`
	Metrics    *model.Metrics    `json:"metrics,omitempty"`
}

type Options struct {
	FlushInterval    time.Duration // 250 ms par défaut
	History          int           // nombre de deltas conservés pour le rejeu
	SubscriberBuffer int           // lots en attente avant de couper un client lent
	// BaseRev est le premier rev. Un rev de départ dérivé de l'heure (TimeBaseRev)
	// garantit qu'un client connecté à une instance précédente reçoit un snapshot.
	BaseRev uint64
}

// TimeBaseRev renvoie un rev de départ croissant d'un démarrage à l'autre et
// représentable exactement par un nombre JavaScript.
func TimeBaseRev() uint64 { return uint64(time.Now().UnixMilli()) * 1000 }

type objKey struct {
	kind Kind
	key  string
}

type Hub struct {
	opts Options

	mu          sync.Mutex
	rev         uint64
	state       map[objKey]any
	pending     []Message
	pendingIdx  map[objKey]int
	history     []Message // anneau ordonné, au plus opts.History éléments
	lastMetrics *Message
	subs        map[*Subscription]struct{}
	ready       bool
	sent        atomic.Uint64
}

// Stats alimente les métriques Prometheus.
type Stats struct {
	Clients      int
	Rev          uint64
	MessagesSent uint64
}

func (h *Hub) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	return Stats{Clients: len(h.subs), Rev: h.rev, MessagesSent: h.sent.Load()}
}

// CountSent comptabilise les messages écrits sur les WebSockets.
func (h *Hub) CountSent(n int) { h.sent.Add(uint64(n)) }

func NewHub(opts Options) *Hub {
	if opts.FlushInterval == 0 {
		opts.FlushInterval = 250 * time.Millisecond
	}
	if opts.History == 0 {
		opts.History = 4096
	}
	if opts.SubscriberBuffer == 0 {
		opts.SubscriberBuffer = 64
	}
	return &Hub{
		opts:       opts,
		rev:        opts.BaseRev,
		state:      map[objKey]any{},
		pendingIdx: map[objKey]int{},
		subs:       map[*Subscription]struct{}{},
	}
}

func (h *Hub) Upsert(kind Kind, key string, obj any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := objKey{kind, key}
	h.state[k] = obj
	h.queue(k, Message{Type: "upsert", Kind: kind, Obj: obj})
}

func (h *Hub) Delete(kind Kind, key string, obj any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	k := objKey{kind, key}
	delete(h.state, k)
	h.queue(k, Message{Type: "delete", Kind: kind, Obj: obj})
}

// queue ajoute un delta au lot en attente ; seul le dernier changement d'un
// objet est conservé.
func (h *Hub) queue(k objKey, m Message) {
	if i, ok := h.pendingIdx[k]; ok {
		h.pending[i] = m
		return
	}
	h.pendingIdx[k] = len(h.pending)
	h.pending = append(h.pending, m)
}

func (h *Hub) SetMetrics(m model.Metrics) {
	h.mu.Lock()
	defer h.mu.Unlock()
	msg := Message{Type: "metrics", Metrics: &m}
	h.lastMetrics = &msg
	h.broadcast([]Message{msg})
}

// Flush numérote le lot en attente, l'archive et l'envoie aux abonnés.
func (h *Hub) Flush() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) == 0 {
		return
	}
	batch := h.pending
	for i := range batch {
		h.rev++
		batch[i].Rev = h.rev
	}
	h.pending = nil
	h.pendingIdx = map[objKey]int{}

	h.history = append(h.history, batch...)
	if over := len(h.history) - h.opts.History; over > 0 {
		h.history = append([]Message(nil), h.history[over:]...)
	}
	h.broadcast(batch)
}

// broadcast envoie sans bloquer ; un abonné dont le tampon est plein est coupé
// et devra se reconnecter avec son dernier rev.
func (h *Hub) broadcast(batch []Message) {
	for s := range h.subs {
		select {
		case s.c <- batch:
		default:
			h.dropLocked(s)
		}
	}
}

func (h *Hub) Run(ctx context.Context) {
	t := time.NewTicker(h.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.Flush()
		}
	}
}

func (h *Hub) Rev() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rev
}

// MarkReady signale que l'état initial est complet (caches synchronisés).
func (h *Hub) MarkReady() {
	h.mu.Lock()
	h.ready = true
	h.mu.Unlock()
}

func (h *Hub) Ready() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready
}

type Subscription struct {
	C      <-chan []Message
	c      chan []Message
	hub    *Hub
	closed bool
}

// Close est idempotent.
func (s *Subscription) Close() {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	s.hub.dropLocked(s)
}

func (h *Hub) dropLocked(s *Subscription) {
	if s.closed {
		return
	}
	s.closed = true
	delete(h.subs, s)
	close(s.c)
}

// Subscribe renvoie ce que le client doit recevoir avant les prochains lots :
// les deltas postérieurs à lastRev s'ils sont encore en historique, sinon un
// snapshot ; puis le dernier message de métriques.
func (h *Hub) Subscribe(lastRev uint64) ([]Message, *Subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var initial []Message
	if replay, ok := h.replayLocked(lastRev); ok {
		initial = replay
	} else {
		initial = []Message{h.snapshotLocked()}
	}
	if h.lastMetrics != nil {
		initial = append(initial, *h.lastMetrics)
	}

	c := make(chan []Message, h.opts.SubscriberBuffer)
	s := &Subscription{C: c, c: c, hub: h}
	h.subs[s] = struct{}{}
	return initial, s
}

func (h *Hub) replayLocked(lastRev uint64) ([]Message, bool) {
	if lastRev == 0 || lastRev > h.rev {
		return nil, false
	}
	if lastRev == h.rev {
		return nil, true
	}
	if len(h.history) == 0 || lastRev+1 < h.history[0].Rev {
		return nil, false
	}
	i := sort.Search(len(h.history), func(i int) bool { return h.history[i].Rev > lastRev })
	return append([]Message(nil), h.history[i:]...), true
}

func (h *Hub) snapshotLocked() Message {
	m := Message{Type: "snapshot", Rev: h.rev,
		Nodes: []model.Node{}, Pods: []model.Pod{}, Workloads: []model.Workload{}, Namespaces: []model.Namespace{}}
	for _, obj := range h.state {
		switch o := obj.(type) {
		case model.Node:
			m.Nodes = append(m.Nodes, o)
		case model.Pod:
			m.Pods = append(m.Pods, o)
		case model.Workload:
			m.Workloads = append(m.Workloads, o)
		case model.Namespace:
			m.Namespaces = append(m.Namespaces, o)
		}
	}
	sort.Slice(m.Nodes, func(i, j int) bool { return m.Nodes[i].Name < m.Nodes[j].Name })
	sort.Slice(m.Pods, func(i, j int) bool { return m.Pods[i].UID < m.Pods[j].UID })
	sort.Slice(m.Namespaces, func(i, j int) bool { return m.Namespaces[i].Name < m.Namespaces[j].Name })
	sort.Slice(m.Workloads, func(i, j int) bool {
		return model.WorkloadKey(m.Workloads[i]) < model.WorkloadKey(m.Workloads[j])
	})
	return m
}
