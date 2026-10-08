package kube

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// Gateway API (v1), lue en unstructured : Gateways, HTTPRoute et GRPCRoute.

const gatewayGroup = "gateway.networking.k8s.io"

// condition : état tri-valué d'une condition de statut, avec sa raison et son
// message ; unknown si elle est absente (aucun contrôleur ne l'a écrite).
func condition(conds []any, typ string) (status, reason, message string) {
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != typ {
			continue
		}
		reason, _ = m["reason"].(string)
		message, _ = m["message"].(string)
		switch m["status"] {
		case "True":
			return model.CondTrue, reason, message
		case "False":
			return model.CondFalse, reason, message
		}
		return model.CondUnknown, reason, message
	}
	return model.CondUnknown, "", ""
}

// toInt lit un nombre JSON (int64 ou float64 selon le décodeur).
func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// permilles répartit 1000 ‰ entre des parts (≥ 0) proportionnellement, par la
// méthode du plus fort reste : la somme fait exactement 1000 (0 si toutes les
// parts sont nulles), une part nulle reste à 0. À reste égal, l'ordre l'emporte.
func permilles(shares []float64) []int {
	out := make([]int, len(shares))
	total := 0.0
	for _, s := range shares {
		total += max(s, 0)
	}
	if total <= 0 {
		return out
	}
	rest := make([]float64, len(shares))
	left := 1000
	for i, s := range shares {
		x := 1000 * max(s, 0) / total
		out[i] = int(math.Floor(x))
		rest[i] = x - float64(out[i])
		left -= out[i]
	}
	idx := make([]int, len(shares))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return rest[idx[a]] > rest[idx[b]] })
	for _, i := range idx {
		if left <= 0 {
			break
		}
		if shares[i] > 0 {
			out[i]++
			left--
		}
	}
	return out
}

// ConvertGateway réduit un Gateway. Raison et message : ceux de Programmed,
// sinon d'Accepted.
func ConvertGateway(u *unstructured.Unstructured) model.Gateway {
	g := model.Gateway{Namespace: u.GetNamespace(), Name: u.GetName(), Listeners: []model.Listener{}}
	g.Class, _, _ = unstructured.NestedString(u.Object, "spec", "gatewayClassName")
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	var aReason, aMessage string
	g.Accepted, aReason, aMessage = condition(conds, "Accepted")
	g.Programmed, g.Reason, g.Message = condition(conds, "Programmed")
	if g.Reason == "" && g.Message == "" {
		g.Reason, g.Message = aReason, aMessage
	}
	addrs, _, _ := unstructured.NestedSlice(u.Object, "status", "addresses")
	for _, a := range addrs {
		if m, ok := a.(map[string]any); ok {
			if v, _ := m["value"].(string); v != "" {
				g.Addresses = append(g.Addresses, v)
			}
		}
	}
	byName := map[string]map[string]any{}
	sl, _, _ := unstructured.NestedSlice(u.Object, "status", "listeners")
	for _, l := range sl {
		if m, ok := l.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				byName[n] = m
			}
		}
	}
	ls, _, _ := unstructured.NestedSlice(u.Object, "spec", "listeners")
	for _, l := range ls {
		m, ok := l.(map[string]any)
		if !ok {
			continue
		}
		li := model.Listener{Port: int32(toInt(m["port"])), Ready: model.CondUnknown}
		li.Name, _ = m["name"].(string)
		li.Protocol, _ = m["protocol"].(string)
		li.Hostname, _ = m["hostname"].(string)
		if st, ok := byName[li.Name]; ok {
			li.AttachedRoutes = int32(toInt(st["attachedRoutes"]))
			c, _ := st["conditions"].([]any)
			li.Ready, _, _ = condition(c, "Programmed")
		}
		g.Listeners = append(g.Listeners, li)
	}
	return g
}

// parentKey : « ns/name » d'une référence de parent si elle désigne un
// Gateway (kind et groupe par défaut), sinon "" (Service d'un maillage…).
func parentKey(ns string, ref any) string {
	m, ok := ref.(map[string]any)
	if !ok {
		return ""
	}
	if k, _ := m["kind"].(string); k != "" && k != "Gateway" {
		return ""
	}
	if g, ok := m["group"].(string); ok && g != gatewayGroup {
		return ""
	}
	name, _ := m["name"].(string)
	if name == "" {
		return ""
	}
	if n, _ := m["namespace"].(string); n != "" {
		ns = n
	}
	return ns + "/" + name
}

// parentGateways : Gateways visés par spec.parentRefs, dans l'ordre, sans doublon.
func parentGateways(u *unstructured.Unstructured) []string {
	refs, _, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
	var out []string
	seen := map[string]bool{}
	for _, r := range refs {
		if k := parentKey(u.GetNamespace(), r); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// firstMatch : premier chemin (HTTPRoute) ou première méthode « service/méthode » (GRPCRoute).
func firstMatch(rule map[string]any, source string) (path, match string) {
	ms, _ := rule["matches"].([]any)
	for _, x := range ms {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		if source == model.SourceGRPCRoute {
			meth, _ := m["method"].(map[string]any)
			svc, _ := meth["service"].(string)
			name, _ := meth["method"].(string)
			if svc != "" || name != "" {
				return "", strings.TrimSuffix(svc+"/"+name, "/")
			}
			continue
		}
		p, _ := m["path"].(map[string]any)
		if v, _ := p["value"].(string); v != "" {
			return v, ""
		}
	}
	return "", ""
}

// gatewayBackend : un backendRef. Seul un Service (groupe core) peut être résolu ;
// un autre kind est publié manquant, avec son kind qualifié par son groupe.
func gatewayBackend(routeNS string, m map[string]any, exists ServiceExists) model.Backend {
	b := model.Backend{Namespace: routeNS, Kind: "Service"}
	b.Service, _ = m["name"].(string)
	if ns, _ := m["namespace"].(string); ns != "" {
		b.Namespace = ns
	}
	if k, _ := m["kind"].(string); k != "" {
		b.Kind = k
	}
	if g, _ := m["group"].(string); g != "" && g != "core" {
		b.Kind += "." + g
	}
	if p := toInt(m["port"]); p > 0 {
		b.Port = fmt.Sprint(p)
	}
	if b.Kind == "Service" {
		b.State = backendState(exists, b.Namespace, b.Service)
	} else {
		b.State = model.BackendMissing
	}
	return b
}

// ConvertGatewayRoute lit une HTTPRoute ou une GRPCRoute (source SourceHTTPRoute
// ou SourceGRPCRoute) : une règle par backendRef, pondérée en pour mille quand
// la règle source en a plusieurs. État : refused si tous ses Gateways la
// refusent (Accepted=False) ; si ResolvedRefs=False, les backends suspects
// (autre namespace, autre kind, introuvables) sont manquants, ou tous à défaut.
func ConvertGatewayRoute(u *unstructured.Unstructured, source string, exists ServiceExists) model.Route {
	r := model.Route{Source: source, Group: gatewayGroup, Namespace: u.GetNamespace(), Name: u.GetName(),
		Gates: parentGateways(u), Rules: []model.Rule{}}
	if len(r.Gates) == 0 {
		r.Gates = []string{model.NoGateway}
	}
	r.Gate = r.Gates[0]

	// Une porte peut avoir plusieurs entrées (sectionName, contrôleurs) : elle
	// n'est refusée que si toutes refusent.
	notRefused := map[string]bool{}
	seen := map[string]bool{}
	unresolved := false
	ps, _, _ := unstructured.NestedSlice(u.Object, "status", "parents")
	for _, p := range ps {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		key := parentKey(r.Namespace, m["parentRef"])
		if key == "" {
			continue
		}
		conds, _ := m["conditions"].([]any)
		rp := model.RouteParent{Gateway: key}
		var ar, rr string
		rp.Accepted, ar, _ = condition(conds, "Accepted")
		rp.ResolvedRefs, rr, _ = condition(conds, "ResolvedRefs")
		switch {
		case rp.Accepted == model.CondFalse:
			rp.Reason = ar
		case rp.ResolvedRefs == model.CondFalse:
			rp.Reason = rr
		}
		r.Parents = append(r.Parents, rp)
		seen[key] = true
		if rp.Accepted != model.CondFalse {
			notRefused[key] = true
		}
		unresolved = unresolved || rp.ResolvedRefs == model.CondFalse
	}
	refused := r.Gate != model.NoGateway
	for _, g := range r.Gates {
		if !seen[g] || notRefused[g] {
			refused = false
		}
	}

	host := ""
	if hs, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "hostnames"); len(hs) > 0 {
		host = hs[0]
	}
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	for _, ro := range rules {
		rm, ok := ro.(map[string]any)
		if !ok {
			continue
		}
		path, match := firstMatch(rm, source)
		refs, _ := rm["backendRefs"].([]any)
		ws := make([]float64, len(refs))
		for i, x := range refs {
			ws[i] = 1
			if m, ok := x.(map[string]any); ok {
				if w, ok := m["weight"]; ok && w != nil {
					ws[i] = float64(toInt(w))
				}
			}
		}
		pm := permilles(ws)
		for i, x := range refs {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			b := gatewayBackend(r.Namespace, m, exists)
			if len(refs) > 1 {
				b.Weight = model.Weight(pm[i])
			}
			r.Rules = append(r.Rules, model.Rule{Host: host, Path: path, Match: match, Backend: b})
		}
	}

	switch {
	case refused:
		for i := range r.Rules {
			r.Rules[i].Backend.State = model.BackendRefused
		}
	case unresolved:
		marked := false
		for i := range r.Rules {
			b := &r.Rules[i].Backend
			if b.State != model.BackendOK || b.Kind != "Service" || b.Namespace != r.Namespace {
				b.State = model.BackendMissing
				marked = true
			}
		}
		if !marked {
			for i := range r.Rules {
				r.Rules[i].Backend.State = model.BackendMissing
			}
		}
	}
	return r
}
