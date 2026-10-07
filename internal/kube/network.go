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
// sain) ; Endpoints ne garde que les pods, dédoublonnés (double pile). Les
// endpoints en arrêt (Terminating, pods qui disparaissent pendant un
// déploiement) sont ignorés : ni comptés dans la santé, ni listés. Un
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
		sp := model.ServicePort{Name: p.Name, Port: p.Port, Protocol: string(p.Protocol), NodePort: p.NodePort}
		if p.TargetPort.IntVal != 0 || p.TargetPort.StrVal != "" { // sinon String() rendrait « 0 »
			sp.TargetPort = p.TargetPort.String()
		}
		m.Ports = append(m.Ports, sp)
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
			if e.Conditions.Terminating != nil && *e.Conditions.Terminating {
				continue // pod en arrêt : sur le point de disparaître
			}
			// Limite acceptée : un Service headless avec publishNotReadyAddresses
			// déclare tous ses endpoints prêts.
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
	r := model.Route{Source: model.SourceIngress, Group: "networking.k8s.io", Namespace: i.Namespace, Name: i.Name,
		Gate: gate, Gates: []string{gate}, Rules: []model.Rule{}}
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
	hostRe = regexp.MustCompile("Host\\([`\"]([^`\"]+)[`\"]")
	pathRe = regexp.MustCompile("(?:PathPrefix|Path)\\([`\"]([^`\"]+)[`\"]")
	sniRe  = regexp.MustCompile("HostSNI\\([`\"]([^`\"]+)[`\"]")
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

// TraefikLookup retrouve un TraefikService (traefik.io d'abord). nil : aucun n'est lisible.
type TraefikLookup func(namespace, name string) (*unstructured.Unstructured, bool)

// traefikMaxDepth borne la résolution des TraefikService imbriqués.
const traefikMaxDepth = 8

// ConvertIngressRoute : IngressRoute HTTP, sans résolution des TraefikService.
func ConvertIngressRoute(u *unstructured.Unstructured, exists ServiceExists) model.Route {
	return ConvertTraefikRoute(u, model.SourceIngressRoute, exists, nil)
}

// traefikRef lit une référence de service Traefik (routes[].services[],
// weighted.services[], mirroring et ses mirrors[]).
func traefikRef(ns string, m map[string]any) (refNS, name, kind, port string) {
	refNS = ns
	name, _ = m["name"].(string)
	if n, _ := m["namespace"].(string); n != "" {
		refNS = n
	}
	kind, _ = m["kind"].(string)
	if kind == "" {
		kind = "Service"
	}
	if p, ok := m["port"]; ok && p != nil {
		port = fmt.Sprint(p)
	}
	return refNS, name, kind, port
}

// weightOf : poids Traefik d'une référence, 1 par défaut, négatif ramené à 0.
func weightOf(m map[string]any) float64 {
	if w, ok := m["weight"]; ok && w != nil {
		return float64(max(toInt(w), 0))
	}
	return 1
}

// leaf : backend final d'une règle Traefik, avec sa part du trafic (0 à 1).
type leaf struct {
	b     model.Backend
	share float64
}

// leafKey : identité d'une feuille ; deux feuilles de même clé sont fusionnées.
type leafKey struct {
	ns, service, port, kind string
	mirror                  bool
	percent                 int
}

// leafSet : feuilles fusionnées (parts sommées), dans l'ordre de première apparition.
type leafSet struct {
	list []leaf
	at   map[leafKey]int
}

func (ls *leafSet) add(l leaf) {
	k := leafKey{l.b.Namespace, l.b.Service, l.b.Port, l.b.Kind, l.b.Mirror, l.b.Percent}
	if ls.at == nil {
		ls.at = map[leafKey]int{}
	}
	if i, ok := ls.at[k]; ok {
		ls.list[i].share += l.share
		return
	}
	ls.at[k] = len(ls.list)
	ls.list = append(ls.list, l)
}

// resolvedTS : feuilles d'un TraefikService (parts relatives à lui) et sa
// hauteur (niveaux de TraefikService, lui compris).
type resolvedTS struct {
	leaves []leaf
	height int
}

// traefikResolver résout des TraefikService en mémoïsant chaque nœud résolu :
// un nœud visé plusieurs fois (éventail, diamant) n'est parcouru qu'une fois,
// d'où un coût en O(nœuds + arêtes). Les échecs ne sont pas mémoïsés (une
// limite de profondeur dépend du chemin) mais interrompent toute la résolution.
type traefikResolver struct {
	lookup TraefikLookup
	memo   map[string]resolvedTS
	path   map[string]bool // chemin courant, pour détecter les cycles
}

// resolve : TraefikService ns/name, atteint au niveau depth (1 : racine).
func (t *traefikResolver) resolve(ns, name string, depth int) (resolvedTS, bool) {
	key := ns + "/" + name
	if depth > traefikMaxDepth || t.path[key] {
		return resolvedTS{}, false
	}
	if r, ok := t.memo[key]; ok {
		return r, depth+r.height-1 <= traefikMaxDepth
	}
	u, ok := t.lookup(ns, name)
	if !ok {
		return resolvedTS{}, false
	}
	t.path[key] = true
	defer delete(t.path, key)
	var acc leafSet
	height := 1
	// child ajoute une référence avec sa part. Miroir : ses feuilles deviennent
	// des miroirs de pourcentage percent. Le percent d'un miroir est local à
	// son TraefikService de mirroring : ni multiplié par la part de ce
	// TraefikService, ni par le percent d'un mirroring englobant (une feuille
	// déjà miroir garde le sien).
	child := func(m map[string]any, share float64, mirror bool, percent int) bool {
		rns, rname, kind, port := traefikRef(ns, m)
		if kind != "TraefikService" {
			acc.add(leaf{b: model.Backend{Namespace: rns, Service: rname, Port: port, Kind: kind, Mirror: mirror, Percent: percent}, share: share})
			return true
		}
		sub, ok := t.resolve(rns, rname, depth+1)
		if !ok {
			return false
		}
		height = max(height, sub.height+1)
		for _, l := range sub.leaves {
			l.share *= share
			if mirror && !l.b.Mirror {
				l.b.Mirror, l.b.Percent = true, percent
			}
			acc.add(l)
		}
		return true
	}
	if ws, found, _ := unstructured.NestedSlice(u.Object, "spec", "weighted", "services"); found {
		total := 0.0
		for _, x := range ws {
			if m, ok := x.(map[string]any); ok {
				total += weightOf(m)
			}
		}
		for _, x := range ws {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			s := 0.0
			if total > 0 {
				s = weightOf(m) / total
			}
			if !child(m, s, false, 0) {
				return resolvedTS{}, false
			}
		}
	} else if main, found, _ := unstructured.NestedMap(u.Object, "spec", "mirroring"); found {
		if !child(main, 1, false, 0) {
			return resolvedTS{}, false
		}
		mirrors, _ := main["mirrors"].([]any)
		for _, x := range mirrors {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			if !child(m, 0, true, int(toInt(m["percent"]))) {
				return resolvedTS{}, false
			}
		}
	} else {
		return resolvedTS{}, false
	}
	r := resolvedTS{leaves: acc.list, height: height}
	t.memo[key] = r
	return r, true
}

// resolveTraefik résout le TraefikService ns/name jusqu'à ses Services, feuilles
// identiques fusionnées. weighted : chaque enfant reçoit sa part normalisée ;
// mirroring : le service principal garde toute la part, chaque miroir devient
// une feuille mirror avec son percent. ok=false : pas de résolveur, introuvable,
// cycle, plus de traefikMaxDepth niveaux, ou ni weighted ni mirroring.
func resolveTraefik(lookup TraefikLookup, ns, name string) ([]leaf, bool) {
	if lookup == nil {
		return nil, false
	}
	t := &traefikResolver{lookup: lookup, memo: map[string]resolvedTS{}, path: map[string]bool{}}
	r, ok := t.resolve(ns, name, 1)
	if !ok {
		return nil, false
	}
	return r.leaves, true
}

// ConvertTraefikRoute lit une IngressRoute, IngressRouteTCP ou IngressRouteUDP
// (traefik.io ou traefik.containo.us). Chaque service de chaque routes[] donne
// une règle ; un TraefikService est remplacé par ses Services (via : son nom),
// ou par une seule règle missing s'il ne se résout pas. Les poids, en pour
// mille de la règle source (plus fort reste : somme de 1000), ne sont publiés
// que s'il y a plusieurs backends non miroirs.
func ConvertTraefikRoute(u *unstructured.Unstructured, source string, exists ServiceExists, lookup TraefikLookup) model.Route {
	gate := u.GetAnnotations()[ingressClassAnnotation]
	if gate == "" {
		gate = traefikGate
	}
	r := model.Route{Source: source, Group: u.GroupVersionKind().Group, Namespace: u.GetNamespace(), Name: u.GetName(),
		Gate: gate, Gates: []string{gate}, Rules: []model.Rule{}}
	routes, _, _ := unstructured.NestedSlice(u.Object, "spec", "routes")
	for _, ro := range routes {
		rm, ok := ro.(map[string]any)
		if !ok {
			continue
		}
		match, _ := rm["match"].(string)
		host, path := parseMatch(match)
		if source != model.SourceIngressRoute {
			host, path = "", ""
			if m := sniRe.FindStringSubmatch(match); m != nil && m[1] != "*" {
				host = m[1]
			}
		}
		services, _ := rm["services"].([]any)
		total := 0.0
		for _, so := range services {
			if sm, ok := so.(map[string]any); ok {
				total += weightOf(sm)
			}
		}
		var leaves []leaf
		for _, so := range services {
			sm, ok := so.(map[string]any)
			if !ok {
				continue
			}
			share := 0.0
			if total > 0 {
				share = weightOf(sm) / total
			}
			ns, name, kind, port := traefikRef(r.Namespace, sm)
			if kind != "TraefikService" {
				leaves = append(leaves, leaf{b: model.Backend{Namespace: ns, Service: name, Port: port, Kind: kind}, share: share})
				continue
			}
			sub, ok := resolveTraefik(lookup, ns, name)
			if !ok {
				leaves = append(leaves, leaf{b: model.Backend{Namespace: ns, Service: name, Kind: kind, State: model.BackendMissing}, share: share})
				continue
			}
			for _, l := range sub {
				l.b.Via = ns + "/" + name
				l.share *= share
				leaves = append(leaves, l)
			}
		}
		var shares []float64
		for _, l := range leaves {
			if !l.b.Mirror {
				shares = append(shares, l.share)
			}
		}
		pm := permilles(shares)
		next := 0
		for _, l := range leaves {
			b := l.b
			if b.State == "" {
				b.State = backendState(exists, b.Namespace, b.Service)
			}
			if !b.Mirror {
				if len(shares) > 1 {
					b.Weight = model.Weight(pm[next])
				}
				next++
			}
			r.Rules = append(r.Rules, model.Rule{Host: host, Path: path, Match: match, Backend: b})
		}
	}
	return r
}

// traefikRefs : Services et TraefikService visés directement par une route
// Traefik ou par un TraefikService (index de la source), « ns/name », sans doublon.
func traefikRefs(u *unstructured.Unstructured) (services, tservices []string) {
	ns := u.GetNamespace()
	seen := map[string]bool{}
	add := func(x any) {
		m, ok := x.(map[string]any)
		if !ok {
			return
		}
		rns, name, kind, _ := traefikRef(ns, m)
		k := kind + ":" + rns + "/" + name
		if name == "" || seen[k] {
			return
		}
		seen[k] = true
		if kind == "TraefikService" {
			tservices = append(tservices, rns+"/"+name)
		} else {
			services = append(services, rns+"/"+name)
		}
	}
	routes, _, _ := unstructured.NestedSlice(u.Object, "spec", "routes")
	for _, ro := range routes {
		rm, _ := ro.(map[string]any)
		svcs, _ := rm["services"].([]any)
		for _, s := range svcs {
			add(s)
		}
	}
	ws, _, _ := unstructured.NestedSlice(u.Object, "spec", "weighted", "services")
	for _, s := range ws {
		add(s)
	}
	if main, found, _ := unstructured.NestedMap(u.Object, "spec", "mirroring"); found {
		add(main)
		mirrors, _ := main["mirrors"].([]any)
		for _, s := range mirrors {
			add(s)
		}
	}
	return services, tservices
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

// ConvertPVC réduit un PersistentVolumeClaim ; pods : UID des pods qui le montent.
func ConvertPVC(p *corev1.PersistentVolumeClaim, pods []string) model.Volume {
	v := model.Volume{Namespace: p.Namespace, Name: p.Name, Phase: string(p.Status.Phase), VolumeName: p.Spec.VolumeName,
		AccessModes: []string{}, Pods: pods}
	if v.Phase == "" {
		v.Phase = string(corev1.ClaimPending)
	}
	if p.Spec.StorageClassName != nil {
		v.StorageClass = *p.Spec.StorageClassName
	}
	if q, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		v.Requested = q.Value()
	}
	if q, ok := p.Status.Capacity[corev1.ResourceStorage]; ok {
		v.Capacity = q.Value()
	}
	for _, m := range p.Spec.AccessModes {
		v.AccessModes = append(v.AccessModes, string(m))
	}
	if v.Pods == nil {
		v.Pods = []string{}
	}
	return v
}

// PodClaims : PVC montés par le pod. Un volume éphémère crée le PVC « <pod>-<volume> ».
func PodClaims(p *corev1.Pod) []string {
	var out []string
	for _, v := range p.Spec.Volumes {
		switch {
		case v.PersistentVolumeClaim != nil:
			out = append(out, v.PersistentVolumeClaim.ClaimName)
		case v.Ephemeral != nil:
			out = append(out, p.Name+"-"+v.Name)
		}
	}
	return out
}

// claimVolumes : seuls volumes gardés en cache, sans leurs détails.
func claimVolumes(vs []corev1.Volume) []corev1.Volume {
	var out []corev1.Volume
	for _, v := range vs {
		switch {
		case v.PersistentVolumeClaim != nil:
			out = append(out, corev1.Volume{Name: v.Name, VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: v.PersistentVolumeClaim.ClaimName}}})
		case v.Ephemeral != nil:
			out = append(out, corev1.Volume{Name: v.Name, VolumeSource: corev1.VolumeSource{Ephemeral: &corev1.EphemeralVolumeSource{}}})
		}
	}
	return out
}

// ConvertPV réduit un PersistentVolume ; ClaimRef : « ns/name » du PVC qui l'a réclamé.
func ConvertPV(pv *corev1.PersistentVolume) model.PersistentVolume {
	m := model.PersistentVolume{Name: pv.Name, StorageClass: pv.Spec.StorageClassName, Phase: string(pv.Status.Phase),
		ReclaimPolicy: string(pv.Spec.PersistentVolumeReclaimPolicy), AccessModes: []string{}}
	if m.Phase == "" {
		m.Phase = string(corev1.VolumePending)
	}
	if q, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
		m.Capacity = q.Value()
	}
	for _, a := range pv.Spec.AccessModes {
		m.AccessModes = append(m.AccessModes, string(a))
	}
	if c := pv.Spec.ClaimRef; c != nil && c.Name != "" {
		m.ClaimRef = c.Namespace + "/" + c.Name
	}
	return m
}

// ClaimExists : « ce PVC existe-t-il ? ». nil quand les PVC ne sont pas listables.
type ClaimExists func(namespace, name string) bool

// PublishPV : un PV n'est publié que s'il n'est lié à aucun PVC existant
// (Available, Released, Failed, ou Bound à un PVC disparu). Sans lecture des
// PVC, un PV Bound n'est jamais publié.
func PublishPV(pv *corev1.PersistentVolume, exists ClaimExists) bool {
	switch pv.Status.Phase {
	case corev1.VolumeAvailable, corev1.VolumeReleased, corev1.VolumeFailed:
		return true
	case corev1.VolumeBound:
		c := pv.Spec.ClaimRef
		return exists != nil && c != nil && !exists(c.Namespace, c.Name)
	}
	return false
}
