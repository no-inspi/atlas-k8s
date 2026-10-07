package kube

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// ConvertService réduit un Service et ses EndpointSlices. La santé compte
// toutes les adresses (un Service sans selector comme default/kubernetes reste
// sain) ; Endpoints ne garde que les pods, dédoublonnés (double pile). Un
// Service sans selector ni slice n'est pas publié (publish=false), sauf
// ExternalName.
func ConvertService(s *corev1.Service, slices []*discoveryv1.EndpointSlice) (m model.Service, publish bool) {
	m = model.Service{Namespace: s.Namespace, Name: s.Name, Type: string(s.Spec.Type), ClusterIP: s.Spec.ClusterIP,
		Headless: s.Spec.ClusterIP == corev1.ClusterIPNone, ExternalName: s.Spec.ExternalName,
		Ports: []model.ServicePort{}, Endpoints: []model.Endpoint{}}
	if m.Type == "" {
		m.Type = string(corev1.ServiceTypeClusterIP)
	}
	if m.Headless {
		m.ClusterIP = ""
	}
	for _, p := range s.Spec.Ports {
		m.Ports = append(m.Ports, model.ServicePort{Name: p.Name, Port: p.Port, TargetPort: p.TargetPort.String(),
			Protocol: string(p.Protocol), NodePort: p.NodePort})
	}
	for _, in := range s.Status.LoadBalancer.Ingress {
		if in.IP != "" {
			m.LoadBalancer = append(m.LoadBalancer, in.IP)
		} else if in.Hostname != "" {
			m.LoadBalancer = append(m.LoadBalancer, in.Hostname)
		}
	}

	// Une adresse est identifiée par son pod, sinon par sa première IP.
	readyByID := map[string]bool{}
	pods := map[string]int{}
	for _, sl := range slices {
		for _, e := range sl.Endpoints {
			ready := e.Conditions.Ready == nil || *e.Conditions.Ready
			id := ""
			if e.TargetRef != nil && e.TargetRef.Kind == "Pod" && e.TargetRef.UID != "" {
				id = "pod:" + string(e.TargetRef.UID)
				if i, ok := pods[id]; ok {
					m.Endpoints[i].Ready = m.Endpoints[i].Ready || ready
				} else {
					pods[id] = len(m.Endpoints)
					m.Endpoints = append(m.Endpoints, model.Endpoint{PodUID: string(e.TargetRef.UID), Ready: ready})
				}
			} else if len(e.Addresses) > 0 {
				id = "ip:" + e.Addresses[0]
			} else {
				continue
			}
			readyByID[id] = readyByID[id] || ready
		}
	}
	sort.Slice(m.Endpoints, func(i, j int) bool { return m.Endpoints[i].PodUID < m.Endpoints[j].PodUID })
	ready := 0
	for _, r := range readyByID {
		if r {
			ready++
		}
	}
	m.Health = model.ServiceHealth(m.Type, len(readyByID), ready)
	publish = m.Type == string(corev1.ServiceTypeExternalName) || len(s.Spec.Selector) > 0 || len(slices) > 0
	return m, publish
}

const (
	ingressClassAnnotation = "kubernetes.io/ingress.class"
	defaultClassAnnotation = "ingressclass.kubernetes.io/is-default-class"
	traefikGate            = "traefik"
)

// IngressGate : porte d'une Ingress : spec.ingressClassName, sinon l'annotation
// kubernetes.io/ingress.class, sinon l'IngressClass par défaut, sinon « default ».
func IngressGate(i *networkingv1.Ingress, classes []*networkingv1.IngressClass) string {
	if c := i.Spec.IngressClassName; c != nil && *c != "" {
		return *c
	}
	if a := i.Annotations[ingressClassAnnotation]; a != "" {
		return a
	}
	var defaults []string
	for _, c := range classes {
		if c.Annotations[defaultClassAnnotation] == "true" {
			defaults = append(defaults, c.Name)
		}
	}
	sort.Strings(defaults)
	if len(defaults) > 0 {
		return defaults[0]
	}
	return "default"
}

// ServiceExists : « ce Service existe-t-il ? ». nil quand on ne peut pas le
// savoir (Services non listables) : le backend est alors considéré présent.
type ServiceExists func(namespace, name string) bool

func backendState(exists ServiceExists, ns, name string) string {
	if exists == nil || exists(ns, name) {
		return model.BackendOK
	}
	return model.BackendMissing
}

func ConvertIngress(i *networkingv1.Ingress, gate string, exists ServiceExists) model.Route {
	r := model.Route{Source: "Ingress", Group: "networking.k8s.io", Namespace: i.Namespace, Name: i.Name, Gate: gate, Rules: []model.Rule{}}
	add := func(host, path string, b *networkingv1.IngressBackend) {
		if b == nil || b.Service == nil {
			return // backend « resource » : hors jalon
		}
		port := b.Service.Port.Name
		if port == "" && b.Service.Port.Number != 0 {
			port = strconv.Itoa(int(b.Service.Port.Number))
		}
		r.Rules = append(r.Rules, model.Rule{Host: host, Path: path, Backend: model.Backend{
			Namespace: i.Namespace, Service: b.Service.Name, Port: port, Kind: "Service", State: backendState(exists, i.Namespace, b.Service.Name)}})
	}
	add("", "", i.Spec.DefaultBackend)
	for _, rule := range i.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, p := range rule.HTTP.Paths {
			b := p.Backend
			add(rule.Host, p.Path, &b)
		}
	}
	for _, lb := range i.Status.LoadBalancer.Ingress {
		if lb.IP != "" {
			r.Addresses = append(r.Addresses, lb.IP)
		} else if lb.Hostname != "" {
			r.Addresses = append(r.Addresses, lb.Hostname)
		}
	}
	return r
}

var (
	hostRe = regexp.MustCompile("Host\\(`([^`]+)`")
	pathRe = regexp.MustCompile("(?:PathPrefix|Path)\\(`([^`]+)`")
)

// parseMatch extrait le premier Host() et le premier Path()/PathPrefix() d'une règle Traefik.
func parseMatch(match string) (host, path string) {
	if m := hostRe.FindStringSubmatch(match); m != nil {
		host = m[1]
	}
	if m := pathRe.FindStringSubmatch(match); m != nil {
		path = m[1]
	}
	return host, path
}

// ConvertIngressRoute lit une IngressRoute Traefik (traefik.io ou traefik.containo.us).
func ConvertIngressRoute(u *unstructured.Unstructured, exists ServiceExists) model.Route {
	gate := u.GetAnnotations()[ingressClassAnnotation]
	if gate == "" {
		gate = traefikGate
	}
	r := model.Route{Source: "IngressRoute", Group: u.GroupVersionKind().Group, Namespace: u.GetNamespace(), Name: u.GetName(), Gate: gate, Rules: []model.Rule{}}
	routes, _, _ := unstructured.NestedSlice(u.Object, "spec", "routes")
	for _, ro := range routes {
		rm, ok := ro.(map[string]any)
		if !ok {
			continue
		}
		match, _ := rm["match"].(string)
		host, path := parseMatch(match)
		services, _ := rm["services"].([]any)
		for _, so := range services {
			sm, ok := so.(map[string]any)
			if !ok {
				continue
			}
			name, _ := sm["name"].(string)
			ns, _ := sm["namespace"].(string)
			if ns == "" {
				ns = r.Namespace
			}
			kind, _ := sm["kind"].(string)
			if kind == "" {
				kind = "Service"
			}
			port := ""
			if p, ok := sm["port"]; ok && p != nil {
				port = fmt.Sprint(p)
			}
			state := model.BackendIndirect
			if kind == "Service" {
				state = backendState(exists, ns, name)
			}
			r.Rules = append(r.Rules, model.Rule{Host: host, Path: path, Match: match,
				Backend: model.Backend{Namespace: ns, Service: name, Port: port, Kind: kind, State: state}})
		}
	}
	return r
}

// routeBackends : Services visés (« ns/name »), pour l'index des routes par Service.
func routeBackends(r model.Route) []string {
	var out []string
	seen := map[string]bool{}
	for _, rule := range r.Rules {
		b := rule.Backend
		if k := b.Namespace + "/" + b.Service; b.Kind == "Service" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}
