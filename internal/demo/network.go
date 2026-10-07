package demo

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
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
}

var volumes = []volumeDef{
	{NS: "production", Name: "data-postgres-payments-0", Class: "standard-rwo", Size: 20 * gi, Workload: "postgres-payments", Ordinal: 0},
	{NS: "production", Name: "data-postgres-payments-1", Class: "standard-rwo", Size: 20 * gi, Workload: "postgres-payments", Ordinal: 1},
	{NS: "monitoring", Name: "prometheus-data", Class: "premium-rwo", Size: 50 * gi, Workload: "prometheus", Ordinal: -1},
	{NS: "staging", Name: "uploads-preview", Class: "standard-rwo", Size: 5 * gi, Pending: true},
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
	vols := []volumeDef{
		{NS: ns, Name: "data-db-0", Class: "standard-rwo", Size: 10 * gi, Workload: "db", Ordinal: 0},
		{NS: ns, Name: "data-db-1", Class: "standard-rwo", Size: 10 * gi, Workload: "db", Ordinal: 1},
	}
	return svcs, routes, vols
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
	out.Rules = make([]model.Rule, len(r.Rules))
	for i, rule := range r.Rules {
		b := rule.Backend
		switch {
		case b.Kind != "Service":
			b.State = model.BackendIndirect
		case exists[b.Namespace+"/"+b.Service]:
			b.State = model.BackendOK
		default:
			b.State = model.BackendMissing
		}
		rule.Backend = b
		out.Rules[i] = rule
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
			spec["selector"] = map[string]any{"app.kubernetes.io/name": m.Name}
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
	case "Ingress", "IngressRoute":
		r, ok := s.netLast["route|"+ref.Kind+"/"+ref.Namespace+"/"+ref.Name].(model.Route)
		if !ok {
			return nil
		}
		if ref.Kind == "Ingress" {
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
		}
		routes := []any{}
		for _, rule := range r.Rules {
			routes = append(routes, map[string]any{"match": rule.Match, "kind": "Rule",
				"services": []any{map[string]any{"name": rule.Backend.Service, "port": rule.Backend.Port}}})
		}
		return map[string]any{"apiVersion": r.Group + "/v1alpha1", "kind": "IngressRoute", "metadata": meta,
			"spec": map[string]any{"entryPoints": []any{"websecure"}, "routes": routes}}
	}
	return nil
}

// netEvents : un PVC en attente attend son premier consommateur (StorageClass
// en WaitForFirstConsumer), comme sur GKE ou kind.
func (s *Sim) netEvents(kind, ns, name string) []model.Event {
	if kind == "PersistentVolumeClaim" {
		if v, ok := s.netLast["volume|"+ns+"/"+name].(model.Volume); ok && v.Phase == "Pending" {
			return []model.Event{{Type: "Normal", Reason: "WaitForFirstConsumer", Count: 12, Source: "persistentvolume-controller",
				Message: "waiting for first consumer to be created before binding", FirstSeen: s.now.Add(-time.Hour), LastSeen: s.now}}
		}
	}
	return []model.Event{}
}
