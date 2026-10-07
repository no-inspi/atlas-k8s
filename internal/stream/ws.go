package stream

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

const (
	writeTimeout = 10 * time.Second
	pingInterval = 30 * time.Second

	// StatusResync : les droits du client ont changé, il doit se reconnecter
	// sans rev pour recevoir un snapshot complet, refiltré.
	StatusResync websocket.StatusCode = 4000
	// StatusSessionExpired : la session a expiré, le client doit se reconnecter.
	StatusSessionExpired websocket.StatusCode = 4401
)

// recheckInterval : fréquence de réévaluation des droits et de la session.
var recheckInterval = 60 * time.Second

// Client décrit ce qu'une connexion a le droit de recevoir.
type Client struct {
	Filter Filter
	// Expired indique la fin de la session (nil : jamais).
	Expired func() bool
}

// ClientFunc construit le Client d'une requête (identité de la session).
type ClientFunc func(r *http.Request) (Client, error)

// Unfiltered : modes démo et auth none.
func Unfiltered(*http.Request) (Client, error) { return Client{Filter: AllowAll{}}, nil }

// Handler sert /api/stream. Le client peut passer ?rev=N pour reprendre là où
// il s'était arrêté. Chaque message JSON part dans sa propre frame texte, après
// filtrage par les droits du client. L'Origin est vérifiée par
// websocket.Accept (même hôte uniquement).
func Handler(h *Hub, log *slog.Logger, clientFor ClientFunc) http.Handler {
	interval := recheckInterval
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastRev, _ := strconv.ParseUint(r.URL.Query().Get("rev"), 10, 64)
		client, err := clientFor(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			CompressionMode: websocket.CompressionContextTakeover,
		})
		if err != nil {
			log.Debug("stream: upgrade refusé", "err", err)
			return
		}
		defer c.CloseNow()

		// Le client n'envoie rien : CloseRead gère les frames de contrôle et
		// annule le contexte à la fermeture côté navigateur.
		ctx := c.CloseRead(r.Context())

		initial, sub := h.Subscribe(lastRev)
		defer sub.Close()

		v := newView(client.Filter)
		if err := send(ctx, h, c, v.apply(ctx, initial)); err != nil {
			return
		}
		ping := time.NewTicker(pingInterval)
		defer ping.Stop()
		recheck := time.NewTicker(interval)
		defer recheck.Stop()
		expired := func() bool { return client.Expired != nil && client.Expired() }
		for {
			select {
			case <-ctx.Done():
				return
			case batch, ok := <-sub.C:
				if !ok {
					c.Close(websocket.StatusTryAgainLater, "client trop lent, reconnectez-vous")
					return
				}
				if expired() {
					c.Close(StatusSessionExpired, "session expirée")
					return
				}
				if err := send(ctx, h, c, v.apply(ctx, batch)); err != nil {
					return
				}
			case <-recheck.C:
				if expired() {
					c.Close(StatusSessionExpired, "session expirée")
					return
				}
				if client.Filter.Changed(ctx) {
					c.Close(StatusResync, "droits modifiés, rechargement")
					return
				}
			case <-ping.C:
				pctx, cancel := context.WithTimeout(ctx, writeTimeout)
				err := c.Ping(pctx)
				cancel()
				if err != nil {
					return
				}
			}
		}
	})
}

func send(ctx context.Context, h *Hub, c *websocket.Conn, msgs []Message) error {
	for _, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			return err
		}
		wctx, cancel := context.WithTimeout(ctx, writeTimeout)
		err = c.Write(wctx, websocket.MessageText, b)
		cancel()
		if err != nil {
			return err
		}
	}
	h.CountSent(len(msgs))
	return nil
}

// view garde ce qu'un client a reçu : un delete n'est envoyé que pour un objet
// connu du client, et les métriques ne portent que sur ses pods et nodes.
type view struct {
	f     Filter
	pods  map[string]bool
	nodes map[string]bool
}

func newView(f Filter) *view {
	return &view{f: f, pods: map[string]bool{}, nodes: map[string]bool{}}
}

func (v *view) apply(ctx context.Context, msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Type {
		case "snapshot":
			out = append(out, v.snapshot(ctx, m))
		case "metrics":
			out = append(out, v.metrics(m))
		case "upsert", "delete":
			if fm, ok := v.delta(ctx, m); ok {
				out = append(out, fm)
			}
		}
	}
	return out
}

func (v *view) snapshot(ctx context.Context, m Message) Message {
	v.pods, v.nodes = map[string]bool{}, map[string]bool{}
	s := Message{Type: "snapshot", Rev: m.Rev,
		Nodes: []model.Node{}, Pods: []model.Pod{}, Workloads: []model.Workload{}, Namespaces: []model.Namespace{},
		Services: []model.Service{}, Routes: []model.Route{}, Volumes: []model.Volume{}}
	for _, n := range m.Nodes {
		if v.f.Allow(ctx, KindNode, n) {
			v.nodes[n.Name] = true
			s.Nodes = append(s.Nodes, n)
		}
	}
	for _, p := range m.Pods {
		if v.f.Allow(ctx, KindPod, p) {
			v.pods[p.UID] = true
			s.Pods = append(s.Pods, p)
		}
	}
	for _, w := range m.Workloads {
		if v.f.Allow(ctx, KindWorkload, w) {
			s.Workloads = append(s.Workloads, w)
		}
	}
	for _, n := range m.Namespaces {
		if v.f.Allow(ctx, KindNamespace, n) {
			s.Namespaces = append(s.Namespaces, n)
		}
	}
	for _, o := range m.Services {
		if v.f.Allow(ctx, KindService, o) {
			s.Services = append(s.Services, o)
		}
	}
	for _, o := range m.Routes {
		if v.f.Allow(ctx, KindRoute, o) {
			s.Routes = append(s.Routes, o)
		}
	}
	for _, o := range m.Volumes {
		if v.f.Allow(ctx, KindVolume, o) {
			s.Volumes = append(s.Volumes, o)
		}
	}
	return s
}

// delta : un objet devenu invisible (droit retiré) part en delete pour que le
// client l'oublie.
func (v *view) delta(ctx context.Context, m Message) (Message, bool) {
	var known map[string]bool
	var id string
	switch o := m.Obj.(type) {
	case model.Pod:
		known, id = v.pods, o.UID
	case model.Node:
		known, id = v.nodes, o.Name
	}
	if known == nil { // workloads, namespaces, Services, routes et volumes : visibles selon le droit courant
		return m, v.f.Allow(ctx, m.Kind, m.Obj)
	}
	if m.Type == "delete" {
		ok := known[id]
		delete(known, id)
		return m, ok
	}
	if v.f.Allow(ctx, m.Kind, m.Obj) {
		known[id] = true
		return m, true
	}
	if known[id] {
		delete(known, id)
		m.Type = "delete"
		return m, true
	}
	return m, false
}

func (v *view) metrics(m Message) Message {
	if m.Metrics == nil {
		return m
	}
	f := model.Metrics{Pods: map[string]model.Usage{}, Nodes: map[string]model.Usage{}}
	for uid, u := range m.Metrics.Pods {
		if v.pods[uid] {
			f.Pods[uid] = u
		}
	}
	for n, u := range m.Metrics.Nodes {
		if v.nodes[n] {
			f.Nodes[n] = u
		}
	}
	return Message{Type: "metrics", Metrics: &f}
}
