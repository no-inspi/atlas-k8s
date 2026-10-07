package demo

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/no-inspi/atlas-k8s/internal/inspect"
	"github.com/no-inspi/atlas-k8s/internal/model"
)

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
				"backend": map[string]any{"service": map[string]any{"name": rule.Backend.Service, "port": map[string]any{"number": portValue(rule.Backend.Port)}}}})
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
