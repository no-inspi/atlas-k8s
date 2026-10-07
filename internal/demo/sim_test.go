package demo

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// fakeSink garde l'état final et le journal des événements.
type fakeSink struct {
	nodes      map[string]model.Node
	pods       map[string]model.Pod
	workloads  map[string]model.Workload
	namespaces []string
	log        []string
	metrics    *model.Metrics
	statuses   map[string][]string // uid -> statuts successifs
	net        map[string]any      // « kind|clé » : Services, routes, volumes
}

func newSink() *fakeSink {
	return &fakeSink{nodes: map[string]model.Node{}, pods: map[string]model.Pod{},
		workloads: map[string]model.Workload{}, statuses: map[string][]string{}, net: map[string]any{}}
}

func (f *fakeSink) Upsert(kind stream.Kind, key string, obj any) {
	switch o := obj.(type) {
	case model.Node:
		f.nodes[key] = o
	case model.Pod:
		f.pods[key] = o
		st := f.statuses[key]
		if len(st) == 0 || st[len(st)-1] != o.DisplayStatus {
			f.statuses[key] = append(st, o.DisplayStatus)
		}
		f.log = append(f.log, fmt.Sprintf("upsert %s %s %s", o.Name, o.DisplayStatus, o.NodeName))
	case model.Workload:
		f.workloads[key] = o
	case model.Namespace:
		f.namespaces = append(f.namespaces, o.Name)
	case model.Service, model.Route, model.Volume, model.Gateway, model.PersistentVolume:
		f.net[string(kind)+"|"+key] = o
	}
}

func (f *fakeSink) Delete(kind stream.Kind, key string, obj any) {
	if kind == stream.KindService || kind == stream.KindRoute || kind == stream.KindVolume ||
		kind == stream.KindGateway || kind == stream.KindPersistentVolume {
		delete(f.net, string(kind)+"|"+key)
		return
	}
	if kind == stream.KindPod {
		f.log = append(f.log, "delete "+obj.(model.Pod).Name)
		delete(f.pods, key)
	}
}

func (f *fakeSink) SetMetrics(m model.Metrics) { f.metrics = &m }

var t0 = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

func start(seed uint64) (*Sim, *fakeSink) {
	sink := newSink()
	s := New(sink, Options{Seed: seed, Now: t0})
	return s, sink
}

// advance simule d seconds à pas de 200 ms, comme Run.
func advance(s *Sim, from time.Time, d time.Duration) time.Time {
	end := from.Add(d)
	t := from
	for t.Before(end) {
		t = t.Add(200 * time.Millisecond)
		s.Step(t)
	}
	return t
}

func podsOf(sink *fakeSink, ns, owner string) []model.Pod {
	var out []model.Pod
	for _, p := range sink.pods {
		if p.Namespace == ns && p.Owner.Name == owner {
			out = append(out, p)
		}
	}
	return out
}

func TestInitialClusterIsPlaced(t *testing.T) {
	s, sink := start(1)
	_ = s
	if len(sink.nodes) != 6 {
		t.Fatalf("attendu 6 nodes, reçu %d", len(sink.nodes))
	}
	api := podsOf(sink, "production", "api-gateway")
	if len(api) != 3 {
		t.Fatalf("api-gateway : %d pods", len(api))
	}
	seen := map[string]bool{}
	for _, p := range api {
		if p.DisplayStatus != "Running" || p.NodeName == "" {
			t.Errorf("%s : %s sur %q", p.Name, p.DisplayStatus, p.NodeName)
		}
		seen[p.NodeName] = true
	}
	if len(seen) != 3 {
		t.Errorf("les 3 replicas devraient être sur 3 nodes distincts : %v", seen)
	}
	for _, p := range sink.pods {
		if p.Owner.Kind == "Deployment" && p.Namespace == "production" && p.Owner.Name == "api-gateway" &&
			!strings.HasPrefix(p.Name, "api-gateway-") {
			t.Errorf("nom de pod inattendu %s", p.Name)
		}
	}
	if sink.workloads["Deployment/production/api-gateway"].Argo == nil {
		t.Error("api-gateway devrait porter l'information ArgoCD")
	}
	if len(sink.namespaces) != 5 {
		t.Errorf("namespaces publiés : %v", sink.namespaces)
	}
	if sink.metrics == nil || len(sink.metrics.Pods) == 0 {
		t.Error("métriques initiales absentes")
	}
}

func nodeByName(sink *fakeSink, name string) model.Node { return sink.nodes[name] }

func TestGPUIsolation(t *testing.T) {
	_, sink := start(2)
	ml := podsOf(sink, "production", "ml-inference")
	if len(ml) != 1 || nodeByName(sink, ml[0].NodeName).GPU == 0 {
		t.Fatalf("ml-inference doit tourner sur le node GPU : %+v", ml)
	}
	for _, p := range sink.pods {
		if p.NodeName == "" || p.Owner.Name == "ml-inference" || p.Owner.Kind == "DaemonSet" {
			continue
		}
		if nodeByName(sink, p.NodeName).GPU > 0 {
			t.Errorf("%s ne tolère pas le taint GPU mais tourne sur %s", p.Name, p.NodeName)
		}
	}
}

func TestDaemonSetHasOnePodPerNode(t *testing.T) {
	_, sink := start(3)
	perNode := map[string]int{}
	for _, p := range podsOf(sink, "monitoring", "node-exporter") {
		perNode[p.NodeName]++
	}
	if len(perNode) != len(sink.nodes) {
		t.Fatalf("node-exporter sur %d nodes sur %d", len(perNode), len(sink.nodes))
	}
	for n, c := range perNode {
		if c != 1 {
			t.Errorf("%s : %d pods node-exporter", n, c)
		}
	}
}

func TestStatefulSetOrdinals(t *testing.T) {
	_, sink := start(4)
	names := map[string]bool{}
	for _, p := range podsOf(sink, "production", "postgres-payments") {
		names[p.Name] = true
		if p.Owner.Kind != "StatefulSet" {
			t.Errorf("%s : owner %s", p.Name, p.Owner.Kind)
		}
	}
	if !names["postgres-payments-0"] || !names["postgres-payments-1"] {
		t.Errorf("ordinals attendus -0 et -1 : %v", names)
	}
}

func TestBrokenImageAndNotReady(t *testing.T) {
	s, sink := start(5)
	advance(s, t0, 10*time.Second)
	cp := podsOf(sink, "staging", "checkout-preview")
	if len(cp) != 1 || cp[0].DisplayStatus != "ImagePullBackOff" || cp[0].Ready {
		t.Fatalf("checkout-preview : %+v", cp)
	}
	notReady := 0
	for _, p := range podsOf(sink, "staging", "orders-service") {
		if p.DisplayStatus == "Running" && !p.Ready {
			notReady++
		}
	}
	if notReady != 1 {
		t.Errorf("attendu 1 pod orders-service Running non ready, reçu %d", notReady)
	}
}

func TestCrashLoopCycle(t *testing.T) {
	s, sink := start(6)
	var crashy model.Pod
	for _, p := range podsOf(sink, "production", "payment-worker") {
		if p.DisplayStatus == "CrashLoopBackOff" {
			crashy = p
		}
	}
	if crashy.UID == "" {
		t.Fatal("un pod payment-worker doit démarrer en CrashLoopBackOff")
	}
	before := crashy.Restarts
	advance(s, t0, 60*time.Second)
	after, ok := sink.pods[crashy.UID]
	if !ok {
		t.Fatal("le pod crashy a disparu")
	}
	if after.Restarts <= before {
		t.Errorf("redémarrages %d -> %d", before, after.Restarts)
	}
	seq := strings.Join(sink.statuses[crashy.UID], ",")
	for _, want := range []string{"Running", "Error", "CrashLoopBackOff"} {
		if !strings.Contains(seq, want) {
			t.Errorf("statut %s jamais vu : %s", want, seq)
		}
	}
}

func TestScaleBeyondCapacityLeavesPodsPending(t *testing.T) {
	s, sink := start(7)
	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "ml-inference", 3)
	advance(s, t0, 5*time.Second)
	pending := 0
	for _, p := range podsOf(sink, "production", "ml-inference") {
		if p.DisplayStatus == "Pending" {
			pending++
			if p.NodeName != "" || !strings.Contains(p.StatusMessage, "nodes are available") {
				t.Errorf("pod pending mal décrit : %+v", p)
			}
		}
	}
	if pending != 2 {
		t.Errorf("attendu 2 pods Pending (un seul GPU), reçu %d", pending)
	}
	if w := sink.workloads["Deployment/production/ml-inference"]; w.Replicas != 3 || w.ReadyReplicas != 1 {
		t.Errorf("workload = %+v", w)
	}

	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "ml-inference", 1)
	advance(s, t0.Add(5*time.Second), 3*time.Second)
	if n := len(podsOf(sink, "production", "ml-inference")); n != 1 {
		t.Errorf("après scale down : %d pods", n)
	}
}

func TestNewPodGoesThroughContainerCreating(t *testing.T) {
	s, sink := start(8)
	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "frontend", 3)
	advance(s, t0, 5*time.Second)
	var fresh string
	for uid, st := range sink.statuses {
		if sink.pods[uid].Owner.Name == "frontend" && len(st) > 1 {
			fresh = strings.Join(st, ",")
		}
	}
	if fresh != "Pending,ContainerCreating,Running" {
		t.Errorf("cycle d'un nouveau pod = %q", fresh)
	}
}

func TestJobCompletesThenDisappears(t *testing.T) {
	s, sink := start(9)
	advance(s, t0, 90*time.Second)
	completed := false
	for _, st := range sink.statuses {
		if strings.HasSuffix(strings.Join(st, ","), "Running,Completed") {
			completed = true
		}
	}
	if !completed {
		t.Fatal("aucun pod de Job n'est passé à Completed")
	}
	deleted := 0
	for _, l := range sink.log {
		if strings.HasPrefix(l, "delete db-backup-") {
			deleted++
		}
	}
	if deleted == 0 {
		t.Error("les pods de Job terminés devraient être supprimés après 30 s")
	}
}

func TestNodeRequestedTracksPods(t *testing.T) {
	_, sink := start(10)
	for name, n := range sink.nodes {
		var cpu int64
		for _, p := range sink.pods {
			if p.NodeName == name && p.DisplayStatus != "Completed" {
				cpu += p.Requests.CPU
			}
		}
		if n.Requested.CPU != cpu {
			t.Errorf("%s : requested %d, somme des pods %d", name, n.Requested.CPU, cpu)
		}
		if n.Requested.CPU > n.Allocatable.CPU {
			t.Errorf("%s surréservé", name)
		}
	}
}

func TestDeterministic(t *testing.T) {
	run := func() string {
		s, sink := start(42)
		advance(s, t0, 40*time.Second)
		return strings.Join(sink.log, "\n")
	}
	if run() != run() {
		t.Fatal("même seed et même horloge doivent produire la même séquence")
	}
}

func TestScaledCluster(t *testing.T) {
	sink := newSink()
	s := New(sink, Options{Seed: 1, Now: t0, Scale: Scale{Nodes: 100, PodsPerNode: 30}})
	advance(s, t0, 5*time.Second)
	if len(sink.nodes) != 100 {
		t.Fatalf("nodes = %d", len(sink.nodes))
	}
	if n := len(sink.pods); n < 2700 || n > 3300 {
		t.Errorf("pods = %d, attendu environ 3 000", n)
	}
	pending := 0
	for _, p := range sink.pods {
		if p.NodeName == "" {
			pending++
		}
	}
	if pending > 30 {
		t.Errorf("%d pods sans node : la capacité simulée doit suffire", pending)
	}
	if len(sink.namespaces) < 50 {
		t.Errorf("namespaces = %d", len(sink.namespaces))
	}
}

func TestDemoNetworkAndStorage(t *testing.T) {
	s, sink := start(5)
	api := sink.net["service|production/api-gateway"].(model.Service)
	if api.Health != model.HealthOK || len(api.Endpoints) != 3 || api.Type != "LoadBalancer" || len(api.LoadBalancer) != 1 {
		t.Errorf("api-gateway = %+v", api)
	}
	if h := sink.net["service|staging/checkout-preview"].(model.Service).Health; h != model.HealthDown {
		t.Errorf("checkout-preview (image introuvable) = %s, attendu down", h)
	}
	if h := sink.net["service|production/stripe-api"].(model.Service).Health; h != model.HealthExternal {
		t.Errorf("stripe-api = %s", h)
	}
	if !sink.net["service|production/postgres-payments"].(model.Service).Headless {
		t.Error("postgres-payments doit être headless")
	}
	shop := sink.net["route|Ingress/production/storefront"].(model.Route)
	if shop.Gate != "nginx" || shop.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("storefront = %+v", shop)
	}
	admin := sink.net["route|IngressRoute/production/admin"].(model.Route)
	if admin.Gate != "traefik" || admin.Group != "traefik.io" || admin.Rules[0].Backend.State != model.BackendMissing {
		t.Errorf("admin = %+v", admin)
	}
	pg := sink.net["volume|production/data-postgres-payments-0"].(model.Volume)
	if pg.Phase != "Bound" || len(pg.Pods) != 1 || pg.StorageClass != "standard-rwo" {
		t.Errorf("data-postgres-payments-0 = %+v", pg)
	}
	if v := sink.net["volume|staging/uploads-preview"].(model.Volume); v.Phase != "Pending" || len(v.Pods) != 0 {
		t.Errorf("uploads-preview = %+v", v)
	}

	for k, o := range sink.net {
		if r, ok := o.(model.Route); ok && (len(r.Gates) == 0 || r.Gates[0] != r.Gate) {
			t.Errorf("%s : gates %v, gate %q", k, r.Gates, r.Gate)
		}
	}
	// Les endpoints suivent les pods.
	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "api-gateway", 1)
	advance(s, t0, 5*time.Second)
	if n := len(sink.net["service|production/api-gateway"].(model.Service).Endpoints); n != 1 {
		t.Errorf("après scale à 1 : %d endpoints", n)
	}
}

func TestScaledCatalogHasNetwork(t *testing.T) {
	c := catalogFor(Scale{Nodes: 10, PodsPerNode: 30}) // 10 équipes
	if len(c.services) != len(services)+40 || len(c.volumes) != len(volumes)+20 {
		t.Errorf("services = %d, volumes = %d", len(c.services), len(c.volumes))
	}
	bySource := map[string]int{}
	for _, r := range c.routes {
		bySource[r.Source]++
	}
	if bySource[model.SourceIngressRoute] != 4+3 { // grafana, admin, argocd, checkout, puis les équipes 3, 6 et 9
		t.Errorf("IngressRoute = %d", bySource[model.SourceIngressRoute])
	}
	if bySource[model.SourceHTTPRoute] != 2+4 { // storefront, preview, puis les équipes 1, 4, 7 et 10
		t.Errorf("HTTPRoute = %d", bySource[model.SourceHTTPRoute])
	}
	if len(c.gateways) != 2+1 || c.gateways[2].Namespace != "team-001" || len(c.pvs) != 3+2 { // PV : équipes 5 et 10
		t.Errorf("gateways = %d, pvs = %d", len(c.gateways), len(c.pvs))
	}
	for _, r := range c.routes {
		if r.Source == model.SourceHTTPRoute && r.Namespace == "team-007" && r.Gate != "team-001/edge" {
			t.Errorf("team-007/web-http : porte %q", r.Gate)
		}
	}
}

// w : poids publié d'un backend, -1 s'il n'en a pas.
func w(b model.Backend) int {
	if b.Weight == nil {
		return -1
	}
	return *b.Weight
}

func TestDemoGatewayAPITraefikAndPV(t *testing.T) {
	_, sink := start(5)
	pub := sink.net["gateway|infra/public"].(model.Gateway)
	if pub.Programmed != model.CondTrue || pub.Class != "eg" || pub.Addresses[0] != "34.120.5.10" || len(pub.Listeners) != 2 || pub.Listeners[0].AttachedRoutes != 1 {
		t.Errorf("infra/public = %+v", pub)
	}
	if in := sink.net["gateway|infra/internal"].(model.Gateway); in.Programmed != model.CondFalse || in.Reason != "AddressNotAssigned" || in.Listeners[0].Name != "https" || in.Listeners[0].Ready != model.CondFalse || in.Listeners[0].AttachedRoutes != 1 {
		t.Errorf("infra/internal = %+v", in)
	}
	shop := sink.net["route|HTTPRoute/production/storefront"].(model.Route)
	if shop.Gate != "infra/public" || len(shop.Gates) != 1 || len(shop.Rules) != 2 || w(shop.Rules[0].Backend) != 900 ||
		w(shop.Rules[1].Backend) != 100 || shop.Rules[1].Backend.Service != "frontend-canary" || shop.Rules[1].Backend.State != model.BackendOK {
		t.Errorf("storefront = %+v", shop)
	}
	if r := sink.net["route|HTTPRoute/staging/preview"].(model.Route); r.Rules[0].Backend.Service != "checkout-preview" ||
		r.Rules[0].Backend.State != model.BackendRefused || r.Parents[0].Reason != "NotAllowedByListeners" {
		t.Errorf("preview = %+v", r)
	}
	if r := sink.net["route|GRPCRoute/production/orders-grpc"].(model.Route); r.Gate != "infra/internal" || r.Rules[0].Match != "orders.v1.Orders/PlaceOrder" || r.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("orders-grpc = %+v", r)
	}
	co := sink.net["route|IngressRoute/production/checkout"].(model.Route)
	if len(co.Rules) != 3 || w(co.Rules[0].Backend) != 750 || w(co.Rules[1].Backend) != 250 || co.Rules[2].Backend.Weight != nil || co.Rules[0].Backend.Via != "production/checkout-split" ||
		!co.Rules[2].Backend.Mirror || co.Rules[2].Backend.Percent != 10 || co.Rules[2].Backend.State != model.BackendOK {
		t.Errorf("checkout = %+v", co.Rules)
	}
	if r := sink.net["route|IngressRouteTCP/production/postgres"].(model.Route); r.Gate != "traefik" || r.Rules[0].Backend.Port != "5432" || r.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("postgres = %+v", r)
	}
	if r := sink.net["route|IngressRouteUDP/monitoring/statsd"].(model.Route); r.Rules[0].Backend.Service != "prometheus" || r.Rules[0].Backend.Port != "9125" || r.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("statsd = %+v", r)
	}
	if pv := sink.net["persistentVolume|pv-old-uploads"].(model.PersistentVolume); pv.Phase != "Released" || pv.ClaimRef != "staging/old-uploads" || pv.Capacity != 5*gi || pv.ReclaimPolicy != "Retain" {
		t.Errorf("pv-old-uploads = %+v", pv)
	}
	if pv := sink.net["persistentVolume|pv-archive-2025"].(model.PersistentVolume); pv.Phase != "Released" || pv.ClaimRef != "production/archive-2025" || pv.Capacity != 100*gi {
		t.Errorf("pv-archive-2025 = %+v", pv)
	}
	if pv, ok := sink.net["persistentVolume|pv-spare-01"].(model.PersistentVolume); !ok || pv.Phase != "Available" || pv.StorageClass != "premium-rwo" || pv.Capacity != 50*gi {
		t.Errorf("pv-spare-01 = %v %+v", ok, pv)
	}
}
