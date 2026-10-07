package demo

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/no-inspi/atlas-k8s/internal/inspect"
	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// Réseau et stockage simulés : Services devant les workloads du catalogue,
// routes (Ingress nginx et IngressRoute traefik) et PVC. Recalculés à chaque
// pas depuis les pods vivants ; seuls les changements sont publiés.

type serviceDef struct {
	NS, Name string
	Workload string // workload ciblé, dans le même namespace ; vide pour un ExternalName
	Type     string // ClusterIP si vide
	Port     int32
	Headless bool
	External string
}

type volumeDef struct {
	NS, Name, Class string
	Size            int64
	Workload        string
	Ordinal         int // replica qui le monte ; -1 : tous
	Pending         bool
}

var services = []serviceDef{
	{NS: "production", Name: "api-gateway", Workload: "api-gateway", Type: "LoadBalancer", Port: 80},
	{NS: "production", Name: "orders-service", Workload: "orders-service", Port: 8080},
	{NS: "production", Name: "payment-worker", Workload: "payment-worker", Port: 8080},
	{NS: "production", Name: "frontend", Workload: "frontend", Port: 80},
	{NS: "production", Name: "frontend-canary", Workload: "frontend", Port: 80},
	{NS: "production", Name: "ml-inference", Workload: "ml-inference", Port: 8080},
	{NS: "production", Name: "postgres-payments", Workload: "postgres-payments", Port: 5432, Headless: true},
	{NS: "production", Name: "stripe-api", Type: "ExternalName", External: "api.stripe.com"},
	{NS: "staging", Name: "api-gateway", Workload: "api-gateway", Port: 80},
	{NS: "staging", Name: "orders-service", Workload: "orders-service", Port: 8080},
	{NS: "staging", Name: "checkout-preview", Workload: "checkout-preview", Port: 80},
	{NS: "monitoring", Name: "prometheus", Workload: "prometheus", Port: 9090},
	{NS: "monitoring", Name: "grafana", Workload: "grafana", Port: 3000},
	{NS: "argocd", Name: "argocd-server", Workload: "argocd-server", Port: 443},
	{NS: "kube-system", Name: "kube-dns", Workload: "coredns", Port: 53},
}

func rule(host, path, ns, svc, port string) model.Rule {
	return model.Rule{Host: host, Path: path, Backend: model.Backend{Namespace: ns, Service: svc, Port: port, Kind: "Service"}}
}

func traefikRule(host, path, ns, svc, port string) model.Rule {
	r := rule(host, path, ns, svc, port)
	r.Match = "Host(`" + host + "`)"
	if path != "" {
		r.Match += " && PathPrefix(`" + path + "`)"
	}
	return r
}

const (
	gatewayAPI      = "gateway.networking.k8s.io"
	envoyController = "gateway.envoyproxy.io/gatewayclass-controller"
)

func accepted(gw string) model.RouteParent {
	return model.RouteParent{Gateway: gw, Accepted: model.CondTrue, ResolvedRefs: model.CondTrue}
}

func weighted(r model.Rule, weight int) model.Rule {
	r.Backend.Weight = model.Weight(weight)
	return r
}

// checkoutRule : feuille du TraefikService production/checkout-split, déjà
// résolue ; un miroir n'a pas de poids.
func checkoutRule(svc, port string, weight int, mirror bool, percent int) model.Rule {
	r := traefikRule("checkout.example.com", "", "production", svc, port)
	r.Backend.Mirror, r.Backend.Percent, r.Backend.Via = mirror, percent, "production/checkout-split"
	if !mirror {
		r.Backend.Weight = model.Weight(weight)
	}
	return r
}

// Routes de base ; l'état des backends est calculé à la publication.
var baseRoutes = []model.Route{
	{Source: "Ingress", Group: "networking.k8s.io", Namespace: "production", Name: "storefront", Gate: "nginx", Addresses: []string{"34.77.12.8"},
		Rules: []model.Rule{rule("shop.example.com", "/", "production", "frontend", "80"), rule("shop.example.com", "/api", "production", "api-gateway", "80")}},
	{Source: "Ingress", Group: "networking.k8s.io", Namespace: "staging", Name: "storefront", Gate: "nginx", Addresses: []string{"34.77.12.8"},
		Rules: []model.Rule{rule("staging.shop.example.com", "/", "staging", "checkout-preview", "80"), rule("staging.shop.example.com", "/api", "staging", "api-gateway", "80")}},
	{Source: "IngressRoute", Group: "traefik.io", Namespace: "monitoring", Name: "grafana", Gate: "traefik",
		Rules: []model.Rule{traefikRule("grafana.example.com", "", "monitoring", "grafana", "3000")}},
	{Source: "IngressRoute", Group: "traefik.io", Namespace: "production", Name: "admin", Gate: "traefik",
		Rules: []model.Rule{traefikRule("admin.example.com", "/", "production", "admin-panel", "8080")}},
	{Source: "IngressRoute", Group: "traefik.io", Namespace: "argocd", Name: "argocd", Gate: "traefik",
		Rules: []model.Rule{traefikRule("argocd.example.com", "", "argocd", "argocd-server", "443")}},
	{Source: model.SourceHTTPRoute, Group: gatewayAPI, Namespace: "production", Name: "storefront", Gate: "infra/public", Gates: []string{"infra/public"},
		Parents: []model.RouteParent{accepted("infra/public")},
		Rules: []model.Rule{weighted(rule("shop.example.com", "/", "production", "frontend", "80"), 900),
			weighted(rule("shop.example.com", "/", "production", "frontend-canary", "80"), 100)}},
	{Source: model.SourceGRPCRoute, Group: gatewayAPI, Namespace: "production", Name: "orders-grpc", Gate: "infra/internal", Gates: []string{"infra/internal"},
		Parents: []model.RouteParent{accepted("infra/internal")},
		Rules: []model.Rule{{Host: "grpc.example.com", Match: "orders.v1.Orders/PlaceOrder",
			Backend: model.Backend{Namespace: "production", Service: "orders-service", Port: "8080", Kind: "Service"}}}},
	{Source: model.SourceHTTPRoute, Group: gatewayAPI, Namespace: "staging", Name: "preview", Gate: "infra/public", Gates: []string{"infra/public"},
		Parents: []model.RouteParent{{Gateway: "infra/public", Accepted: model.CondFalse, ResolvedRefs: model.CondTrue, Reason: "NotAllowedByListeners"}},
		Rules: []model.Rule{{Host: "preview.example.com", Path: "/",
			Backend: model.Backend{Namespace: "staging", Service: "checkout-preview", Port: "80", Kind: "Service", State: model.BackendRefused}}}},
	{Source: model.SourceIngressRoute, Group: "traefik.io", Namespace: "production", Name: "checkout", Gate: "traefik",
		Rules: []model.Rule{checkoutRule("api-gateway", "80", 750, false, 0), checkoutRule("orders-service", "8080", 250, false, 0),
			checkoutRule("payment-worker", "8080", 0, true, 10)}},
	{Source: model.SourceIngressRouteTCP, Group: "traefik.io", Namespace: "production", Name: "postgres", Gate: "traefik",
		Rules: []model.Rule{{Match: "HostSNI(`*`)", Backend: model.Backend{Namespace: "production", Service: "postgres-payments", Port: "5432", Kind: "Service"}}}},
	{Source: model.SourceIngressRouteUDP, Group: "traefik.io", Namespace: "monitoring", Name: "statsd", Gate: "traefik",
		Rules: []model.Rule{{Backend: model.Backend{Namespace: "monitoring", Service: "prometheus", Port: "9125", Kind: "Service"}}}},
}

var volumes = []volumeDef{
	{NS: "production", Name: "data-postgres-payments-0", Class: "standard-rwo", Size: 20 * gi, Workload: "postgres-payments", Ordinal: 0},
	{NS: "production", Name: "data-postgres-payments-1", Class: "standard-rwo", Size: 20 * gi, Workload: "postgres-payments", Ordinal: 1},
	{NS: "monitoring", Name: "prometheus-data", Class: "premium-rwo", Size: 50 * gi, Workload: "prometheus", Ordinal: -1},
	{NS: "staging", Name: "uploads-preview", Class: "standard-rwo", Size: 5 * gi, Pending: true},
}

// gateways : attachedRoutes est calculé à la publication.
var gateways = []model.Gateway{
	{Namespace: "infra", Name: "public", Class: "eg", Accepted: model.CondTrue, Programmed: model.CondTrue, Reason: "Programmed",
		Addresses: []string{"34.120.5.10"}, Listeners: []model.Listener{
			{Name: "http", Protocol: "HTTP", Port: 80, Hostname: "*.example.com", Ready: model.CondTrue},
			{Name: "https", Protocol: "HTTPS", Port: 443, Hostname: "*.example.com", Ready: model.CondTrue},
		}},
	{Namespace: "infra", Name: "internal", Class: "eg", Accepted: model.CondTrue, Programmed: model.CondFalse, Reason: "AddressNotAssigned",
		Message: "No addresses have been assigned to the Gateway", Listeners: []model.Listener{
			{Name: "https", Protocol: "HTTPS", Port: 443, Ready: model.CondFalse},
		}},
}

// traefikServices : TraefikService simulés, pour le YAML de l'inspecteur ; les
// routes qui les visent portent déjà leurs Services résolus.
var traefikServices = map[string]map[string]any{
	"production/checkout-split": {"weighted": map[string]any{"services": []any{
		map[string]any{"name": "api-gateway", "port": 80, "weight": 3},
		map[string]any{"name": "checkout-mirror", "kind": "TraefikService", "weight": 1},
	}}},
	"production/checkout-mirror": {"mirroring": map[string]any{"name": "orders-service", "port": 8080,
		"mirrors": []any{map[string]any{"name": "payment-worker", "port": 8080, "percent": 10}}}},
}

// orphanPVs : PersistentVolumes sans PVC.
var orphanPVs = []model.PersistentVolume{
	{Name: "pv-old-uploads", StorageClass: "standard-rwo", Capacity: 5 * gi, AccessModes: []string{"ReadWriteOnce"},
		ReclaimPolicy: "Retain", Phase: "Released", ClaimRef: "staging/old-uploads"},
	{Name: "pv-archive-2025", StorageClass: "standard-rwo", Capacity: 100 * gi, AccessModes: []string{"ReadWriteOnce"},
		ReclaimPolicy: "Retain", Phase: "Released", ClaimRef: "production/archive-2025"},
	{Name: "pv-spare-01", StorageClass: "premium-rwo", Capacity: 50 * gi, AccessModes: []string{"ReadWriteOnce"},
		ReclaimPolicy: "Retain", Phase: "Available"},
}

// teamNetwork : Services, routes et PVC d'une équipe simulée (banc de performance).
func teamNetwork(i int, ns string) ([]serviceDef, []model.Route, []volumeDef) {
	svcs := []serviceDef{
		{NS: ns, Name: "api", Workload: "api", Port: 8080},
		{NS: ns, Name: "web", Workload: "web", Port: 80},
		{NS: ns, Name: "gateway", Workload: "gateway", Port: 80},
		{NS: ns, Name: "db", Workload: "db", Port: 5432, Headless: true},
	}
	host := ns + ".example.com"
	routes := []model.Route{{Source: "Ingress", Group: "networking.k8s.io", Namespace: ns, Name: "gateway", Gate: "nginx",
		Rules: []model.Rule{rule(host, "/", ns, "gateway", "80")}}}
	if i%3 == 0 {
		routes = append(routes, model.Route{Source: "IngressRoute", Group: "traefik.io", Namespace: ns, Name: "web", Gate: "traefik",
			Rules: []model.Rule{traefikRule("web."+host, "", ns, "web", "80")}})
	}
	if i%3 == 1 {
		gw := teamGateway(i)
		routes = append(routes, model.Route{Source: model.SourceHTTPRoute, Group: gatewayAPI, Namespace: ns, Name: "web-http",
			Gate: gw, Gates: []string{gw}, Parents: []model.RouteParent{accepted(gw)},
			Rules: []model.Rule{rule("app."+host, "/", ns, "web", "80")}})
	}
	vols := []volumeDef{
		{NS: ns, Name: "data-db-0", Class: "standard-rwo", Size: 10 * gi, Workload: "db", Ordinal: 0},
		{NS: ns, Name: "data-db-1", Class: "standard-rwo", Size: 10 * gi, Workload: "db", Ordinal: 1},
	}
	return svcs, routes, vols
}

// teamGateway : Gateway partagé par dix équipes, porté par la première.
func teamGateway(i int) string { return fmt.Sprintf("team-%03d/edge", (i-1)/10*10+1) }

func teamGateways(i int, ns string) []model.Gateway {
	if (i-1)%10 != 0 {
		return nil
	}
	return []model.Gateway{{Namespace: ns, Name: "edge", Class: "eg", Accepted: model.CondTrue, Programmed: model.CondTrue, Reason: "Programmed",
		Addresses: []string{fmt.Sprintf("34.120.9.%d", i%250)},
		Listeners: []model.Listener{{Name: "http", Protocol: "HTTP", Port: 80, Ready: model.CondTrue}}}}
}

// teamPVs : un PV Released une équipe sur cinq (1 PV pour 10 PVC).
func teamPVs(i int, ns string) []model.PersistentVolume {
	if i%5 != 0 {
		return nil
	}
	return []model.PersistentVolume{{Name: "pv-" + ns + "-archive", StorageClass: "standard-rwo", Capacity: 10 * gi,
		AccessModes: []string{"ReadWriteOnce"}, ReclaimPolicy: "Retain", Phase: "Released", ClaimRef: ns + "/data-db-2"}}
}

func hash32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}

// publie ce qui a changé.
func (s *Sim) flushNetwork() {
	seen := map[string]bool{}
	publish := func(kind stream.Kind, key string, obj any) {
		k := string(kind) + "|" + key
		seen[k] = true
		if prev, ok := s.netLast[k]; ok && reflect.DeepEqual(prev, obj) {
			return
		}
		s.netLast[k] = obj
		s.sink.Upsert(kind, key, obj)
	}
	pods := map[string][]*simPod{} // « ns/workload » → pods placés, hors Terminating
	for _, p := range s.pods {
		if p.pod.NodeName == "" || p.pod.DisplayStatus == "Terminating" {
			continue
		}
		k := p.wl.def.NS + "/" + p.wl.def.Name
		pods[k] = append(pods[k], p)
	}
	exists := map[string]bool{}
	for _, d := range s.catalog.services {
		m := serviceModel(d, pods[d.NS+"/"+d.Workload])
		exists[model.ServiceKey(m)] = true
		publish(stream.KindService, model.ServiceKey(m), m)
	}
	for _, r := range s.catalog.routes {
		m := withBackendStates(r, exists)
		publish(stream.KindRoute, model.RouteKey(m), m)
	}
	for _, d := range s.catalog.volumes {
		m := volumeModel(d, pods[d.NS+"/"+d.Workload])
		publish(stream.KindVolume, model.VolumeKey(m), m)
	}
	for _, g := range s.catalog.gateways { // attachedRoutes précalculé par catalogFor
		publish(stream.KindGateway, model.GatewayKey(g), g)
	}
	for _, p := range s.catalog.pvs {
		publish(stream.KindPersistentVolume, model.PersistentVolumeKey(p), p)
	}
	for k, prev := range s.netLast {
		if !seen[k] {
			kind, key, _ := strings.Cut(k, "|")
			delete(s.netLast, k)
			s.sink.Delete(stream.Kind(kind), key, prev)
		}
	}
}

func serviceModel(d serviceDef, pods []*simPod) model.Service {
	typ := d.Type
	if typ == "" {
		typ = "ClusterIP"
	}
	m := model.Service{Namespace: d.NS, Name: d.Name, Type: typ, Headless: d.Headless, ExternalName: d.External,
		Ports: []model.ServicePort{}, Endpoints: []model.Endpoint{}}
	if typ != "ExternalName" {
		m.Ports = []model.ServicePort{{Name: "http", Port: d.Port, TargetPort: "http", Protocol: "TCP"}}
		if !d.Headless {
			h := hash32(d.NS + "/" + d.Name)
			m.ClusterIP = fmt.Sprintf("10.96.%d.%d", h>>8&255, h&255)
		}
		if typ == "LoadBalancer" {
			m.LoadBalancer = []string{"34.77.12.8"}
		}
	}
	ready := 0
	for _, p := range pods {
		m.Endpoints = append(m.Endpoints, model.Endpoint{PodUID: p.pod.UID, Ready: p.pod.Ready})
		if p.pod.Ready {
			ready++
		}
	}
	sort.Slice(m.Endpoints, func(i, j int) bool { return m.Endpoints[i].PodUID < m.Endpoints[j].PodUID })
	m.Health = model.ServiceHealth(typ, len(m.Endpoints), ready)
	return m
}

func withBackendStates(r model.Route, exists map[string]bool) model.Route {
	out := r
	if len(out.Gates) == 0 {
		out.Gates = []string{r.Gate}
	}
	out.Rules = make([]model.Rule, len(r.Rules))
	for i, rule := range r.Rules {
		b := rule.Backend
		switch {
		case b.State == model.BackendRefused: // refusé par le catalogue
		case b.Kind == "Service" && exists[b.Namespace+"/"+b.Service]:
			b.State = model.BackendOK
		default:
			b.State = model.BackendMissing
		}
		rule.Backend = b
		out.Rules[i] = rule
	}
	return out
}

// withAttachedRoutes : chaque listener compte les routes acceptées par son
// Gateway. Les routes du catalogue sont statiques : calculé une seule fois, à
// la construction du catalogue (O(G+R)), pas à chaque pas.
func withAttachedRoutes(gws []model.Gateway, routes []model.Route) []model.Gateway {
	n := map[string]int32{}
	for _, r := range routes {
		for _, p := range r.Parents {
			if p.Accepted != model.CondFalse {
				n[p.Gateway]++
			}
		}
	}
	out := make([]model.Gateway, len(gws))
	for i, g := range gws {
		g.Listeners = append([]model.Listener(nil), g.Listeners...) // ne modifie pas le catalogue global
		for j := range g.Listeners {
			g.Listeners[j].AttachedRoutes = n[model.GatewayKey(g)]
		}
		out[i] = g
	}
	return out
}

func volumeModel(d volumeDef, pods []*simPod) model.Volume {
	v := model.Volume{Namespace: d.NS, Name: d.Name, StorageClass: d.Class, Requested: d.Size,
		AccessModes: []string{"ReadWriteOnce"}, Phase: "Pending", Pods: []string{}}
	if !d.Pending {
		v.Phase, v.Capacity = "Bound", d.Size
		v.VolumeName = fmt.Sprintf("pvc-%08x", hash32(d.NS+"/"+d.Name))
		for _, p := range pods {
			if d.Ordinal < 0 || p.ordinal == d.Ordinal {
				v.Pods = append(v.Pods, p.pod.UID)
			}
		}
		sort.Strings(v.Pods)
	}
	return v
}

func (s *Sim) netObject(ref inspect.Ref) map[string]any {
	meta := map[string]any{"name": ref.Name, "namespace": ref.Namespace}
	switch ref.Kind {
	case "Service":
		m, ok := s.netLast["service|"+ref.Namespace+"/"+ref.Name].(model.Service)
		if !ok {
			return nil
		}
		spec := map[string]any{"type": m.Type}
		if m.Type == "ExternalName" {
			spec["externalName"] = m.ExternalName
		} else {
			workload := m.Name
			for _, d := range s.catalog.services {
				if d.NS == m.Namespace && d.Name == m.Name {
					workload = d.Workload
					break
				}
			}
			spec["selector"] = map[string]any{"app.kubernetes.io/name": workload}
			spec["clusterIP"] = m.ClusterIP
			if m.Headless {
				spec["clusterIP"] = "None"
			}
			ports := []any{}
			for _, p := range m.Ports {
				ports = append(ports, map[string]any{"name": p.Name, "port": p.Port, "targetPort": p.TargetPort, "protocol": p.Protocol})
			}
			spec["ports"] = ports
		}
		return map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": meta, "spec": spec}
	case "PersistentVolumeClaim":
		v, ok := s.netLast["volume|"+ref.Namespace+"/"+ref.Name].(model.Volume)
		if !ok {
			return nil
		}
		size := fmt.Sprintf("%dGi", v.Requested/gi)
		status := map[string]any{"phase": v.Phase}
		spec := map[string]any{"accessModes": v.AccessModes, "storageClassName": v.StorageClass,
			"resources": map[string]any{"requests": map[string]any{"storage": size}}}
		if v.Phase == "Bound" {
			spec["volumeName"] = v.VolumeName
			status["capacity"] = map[string]any{"storage": size}
		}
		return map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": meta, "spec": spec, "status": status}
	case "Ingress":
		r, ok := s.netLast["route|Ingress/"+ref.Namespace+"/"+ref.Name].(model.Route)
		if !ok {
			return nil
		}
		byHost := map[string][]any{}
		var hosts []string
		for _, rule := range r.Rules {
			if _, ok := byHost[rule.Host]; !ok {
				hosts = append(hosts, rule.Host)
			}
			byHost[rule.Host] = append(byHost[rule.Host], map[string]any{"path": rule.Path, "pathType": "Prefix",
				"backend": map[string]any{"service": map[string]any{"name": rule.Backend.Service, "port": map[string]any{"number": rule.Backend.Port}}}})
		}
		rules := []any{}
		for _, h := range hosts {
			rules = append(rules, map[string]any{"host": h, "http": map[string]any{"paths": byHost[h]}})
		}
		return map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": meta,
			"spec": map[string]any{"ingressClassName": r.Gate, "rules": rules}}
	case "IngressRoute", "IngressRouteTCP", "IngressRouteUDP":
		r, ok := s.netLast["route|"+ref.Kind+"/"+ref.Namespace+"/"+ref.Name].(model.Route)
		if !ok || ref.Group != r.Group {
			return nil
		}
		return traefikObject(r, meta)
	case "TraefikService":
		spec, ok := traefikServices[ref.Namespace+"/"+ref.Name]
		if !ok || ref.Group != "traefik.io" {
			return nil
		}
		return map[string]any{"apiVersion": "traefik.io/v1alpha1", "kind": "TraefikService", "metadata": meta, "spec": spec}
	case "HTTPRoute", "GRPCRoute":
		r, ok := s.netLast["route|"+ref.Kind+"/"+ref.Namespace+"/"+ref.Name].(model.Route)
		if !ok {
			return nil
		}
		return gatewayRouteObject(r, meta)
	case "Gateway":
		g, ok := s.netLast["gateway|"+ref.Namespace+"/"+ref.Name].(model.Gateway)
		if !ok {
			return nil
		}
		return gatewayObject(g, meta)
	case "GatewayClass":
		for _, g := range s.catalog.gateways {
			if g.Class == ref.Name {
				return map[string]any{"apiVersion": gatewayAPI + "/v1", "kind": "GatewayClass", "metadata": map[string]any{"name": ref.Name},
					"spec":   map[string]any{"controllerName": envoyController},
					"status": map[string]any{"conditions": []any{condObj("Accepted", model.CondTrue, "", "")}}}
			}
		}
	case "PersistentVolume":
		p, ok := s.netLast["persistentVolume|"+ref.Name].(model.PersistentVolume)
		if !ok {
			return nil
		}
		return pvObject(p)
	}
	return nil
}

// portValue : un port numérique en nombre, un port nommé tel quel.
func portValue(p string) any {
	if n, err := strconv.Atoi(p); err == nil {
		return n
	}
	return p
}

// condObj : condition de statut Kubernetes depuis un tri-état.
func condObj(typ, tri, reason, message string) map[string]any {
	st := "Unknown"
	switch tri {
	case model.CondTrue:
		st = "True"
	case model.CondFalse:
		st = "False"
	}
	if reason == "" {
		reason = typ
	}
	c := map[string]any{"type": typ, "status": st, "reason": reason}
	if message != "" {
		c["message"] = message
	}
	return c
}

// traefikObject : une route Traefik ; les feuilles d'un TraefikService
// redeviennent une seule référence à lui.
func traefikObject(r model.Route, meta map[string]any) map[string]any {
	routes := []any{}
	byMatch := map[string]int{}
	for _, rule := range r.Rules {
		svc := map[string]any{"name": rule.Backend.Service, "port": portValue(rule.Backend.Port)}
		if rule.Backend.Via != "" {
			_, name, _ := strings.Cut(rule.Backend.Via, "/")
			svc = map[string]any{"name": name, "kind": "TraefikService"}
		}
		i, ok := byMatch[rule.Match]
		if !ok {
			i = len(routes)
			byMatch[rule.Match] = i
			ro := map[string]any{"services": []any{}}
			if r.Source == model.SourceIngressRoute {
				ro["kind"] = "Rule"
			}
			if rule.Match != "" {
				ro["match"] = rule.Match
			}
			routes = append(routes, ro)
		}
		ro := routes[i].(map[string]any)
		svcs := ro["services"].([]any)
		dup := false
		for _, x := range svcs {
			dup = dup || reflect.DeepEqual(x, svc)
		}
		if !dup {
			ro["services"] = append(svcs, svc)
		}
	}
	entry := map[string]string{model.SourceIngressRoute: "websecure", model.SourceIngressRouteTCP: "postgres", model.SourceIngressRouteUDP: "statsd"}[r.Source]
	return map[string]any{"apiVersion": r.Group + "/v1alpha1", "kind": r.Source, "metadata": meta,
		"spec": map[string]any{"entryPoints": []any{entry}, "routes": routes}}
}

// gatewayRouteObject : une HTTPRoute ou une GRPCRoute, règles regroupées par chemin ou méthode.
func gatewayRouteObject(r model.Route, meta map[string]any) map[string]any {
	parents := []any{}
	for _, g := range r.Gates {
		ns, name, _ := strings.Cut(g, "/")
		parents = append(parents, map[string]any{"name": name, "namespace": ns})
	}
	spec := map[string]any{"parentRefs": parents}
	if len(r.Rules) > 0 && r.Rules[0].Host != "" {
		spec["hostnames"] = []any{r.Rules[0].Host}
	}
	rules := []any{}
	idx := map[string]int{}
	for _, rule := range r.Rules {
		k := rule.Path + "|" + rule.Match
		i, ok := idx[k]
		if !ok {
			i = len(rules)
			idx[k] = i
			ro := map[string]any{"backendRefs": []any{}}
			if rule.Path != "" {
				ro["matches"] = []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": rule.Path}}}
			}
			if rule.Match != "" {
				svc, meth, _ := strings.Cut(rule.Match, "/")
				m := map[string]any{"service": svc}
				if meth != "" {
					m["method"] = meth
				}
				ro["matches"] = []any{map[string]any{"method": m}}
			}
			rules = append(rules, ro)
		}
		ro := rules[i].(map[string]any)
		ref := map[string]any{"name": rule.Backend.Service, "port": portValue(rule.Backend.Port)}
		if rule.Backend.Namespace != r.Namespace {
			ref["namespace"] = rule.Backend.Namespace
		}
		if rule.Backend.Weight != nil {
			ref["weight"] = *rule.Backend.Weight
		}
		ro["backendRefs"] = append(ro["backendRefs"].([]any), ref)
	}
	spec["rules"] = rules
	status := []any{}
	for _, p := range r.Parents {
		ns, name, _ := strings.Cut(p.Gateway, "/")
		reason := ""
		if p.Accepted == model.CondFalse {
			reason = p.Reason
		}
		status = append(status, map[string]any{"parentRef": map[string]any{"name": name, "namespace": ns}, "controllerName": envoyController,
			"conditions": []any{condObj("Accepted", p.Accepted, reason, ""), condObj("ResolvedRefs", p.ResolvedRefs, "", "")}})
	}
	return map[string]any{"apiVersion": gatewayAPI + "/v1", "kind": r.Source, "metadata": meta, "spec": spec,
		"status": map[string]any{"parents": status}}
}

func gatewayObject(g model.Gateway, meta map[string]any) map[string]any {
	listeners, lstatus := []any{}, []any{}
	for _, l := range g.Listeners {
		spec := map[string]any{"name": l.Name, "protocol": l.Protocol, "port": l.Port}
		if l.Hostname != "" {
			spec["hostname"] = l.Hostname
		}
		listeners = append(listeners, spec)
		lstatus = append(lstatus, map[string]any{"name": l.Name, "attachedRoutes": l.AttachedRoutes,
			"conditions": []any{condObj("Programmed", l.Ready, "", "")}})
	}
	status := map[string]any{"listeners": lstatus, "conditions": []any{
		condObj("Accepted", g.Accepted, "", ""), condObj("Programmed", g.Programmed, g.Reason, g.Message)}}
	if len(g.Addresses) > 0 {
		addrs := []any{}
		for _, a := range g.Addresses {
			addrs = append(addrs, map[string]any{"type": "IPAddress", "value": a})
		}
		status["addresses"] = addrs
	}
	return map[string]any{"apiVersion": gatewayAPI + "/v1", "kind": "Gateway", "metadata": meta,
		"spec": map[string]any{"gatewayClassName": g.Class, "listeners": listeners}, "status": status}
}

func pvObject(p model.PersistentVolume) map[string]any {
	spec := map[string]any{"capacity": map[string]any{"storage": fmt.Sprintf("%dGi", p.Capacity/gi)}, "accessModes": p.AccessModes,
		"persistentVolumeReclaimPolicy": p.ReclaimPolicy, "storageClassName": p.StorageClass}
	if ns, name, ok := strings.Cut(p.ClaimRef, "/"); ok {
		spec["claimRef"] = map[string]any{"kind": "PersistentVolumeClaim", "namespace": ns, "name": name}
	}
	return map[string]any{"apiVersion": "v1", "kind": "PersistentVolume", "metadata": map[string]any{"name": p.Name},
		"spec": spec, "status": map[string]any{"phase": p.Phase}}
}

// netEvents : un PVC en attente attend son premier consommateur (StorageClass
// en WaitForFirstConsumer), comme sur GKE ou kind ; un Gateway non programmé
// répète son avertissement.
func (s *Sim) netEvents(kind, ns, name string) []model.Event {
	switch kind {
	case "PersistentVolumeClaim":
		if v, ok := s.netLast["volume|"+ns+"/"+name].(model.Volume); ok && v.Phase == "Pending" {
			return []model.Event{{Type: "Normal", Reason: "WaitForFirstConsumer", Count: 12, Source: "persistentvolume-controller",
				Message: "waiting for first consumer to be created before binding", FirstSeen: s.now.Add(-time.Hour), LastSeen: s.now}}
		}
	case "Gateway":
		if g, ok := s.netLast["gateway|"+ns+"/"+name].(model.Gateway); ok && g.Programmed == model.CondFalse {
			return []model.Event{{Type: "Warning", Reason: g.Reason, Count: 30, Source: "envoy-gateway",
				Message: g.Message, FirstSeen: s.now.Add(-2 * time.Hour), LastSeen: s.now}}
		}
	}
	return []model.Event{}
}
