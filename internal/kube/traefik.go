package kube

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// Traefik dans la source : IngressRoute (dynkinds.go), IngressRouteTCP et
// IngressRouteUDP, TraefikService résolus jusqu'aux Services.

func gvrIngressRouteTCP(g string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "ingressroutetcps"}
}

func gvrIngressRouteUDP(g string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "ingressrouteudps"}
}

func gvrTraefikService(g string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "traefikservices"}
}

const indexByTraefikService = "traefikservice" // routes et TraefikService par TraefikService visé : « ns/name »

var traefikRouteSources = []string{model.SourceIngressRoute, model.SourceIngressRouteTCP, model.SourceIngressRouteUDP}

func traefikRouteGVR(g, source string) schema.GroupVersionResource {
	switch source {
	case model.SourceIngressRouteTCP:
		return gvrIngressRouteTCP(g)
	case model.SourceIngressRouteUDP:
		return gvrIngressRouteUDP(g)
	}
	return gvrIngressRoute(g)
}

// traefikIndexers : Services et TraefikService visés directement, pour les
// routes Traefik comme pour les TraefikService.
func traefikIndexers(*Source) cache.Indexers {
	return cache.Indexers{
		indexByBackend: func(o any) ([]string, error) {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return nil, nil
			}
			svcs, _ := traefikRefs(u)
			return svcs, nil
		},
		indexByTraefikService: func(o any) ([]string, error) {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return nil, nil
			}
			_, ts := traefikRefs(u)
			return ts, nil
		},
	}
}

func traefikRouteKind(g, source string) dynKind {
	return dynKind{gvr: traefikRouteGVR(g, source), on: markRoute(source), indexers: traefikIndexers}
}

func traefikServiceKind(g string) dynKind {
	return dynKind{gvr: gvrTraefikService(g), on: (*Source).onTraefikService, indexers: traefikIndexers}
}

// onTraefikService : routes qui atteignent ce TraefikService (sa résolution a changé).
func (s *Source) onTraefikService(o any) {
	if u, ok := o.(*unstructured.Unstructured); ok {
		s.markTraefikUsers(u.GetNamespace() + "/" + u.GetName())
	}
}

// markTraefikUsers marque les routes qui visent le TraefikService key,
// directement ou par d'autres TraefikService, sur traefikMaxDepth niveaux au
// plus. Parcours en largeur, chaque TraefikService visité une fois : un
// diamant ou un cycle ne coûte pas plus qu'une chaîne.
func (s *Source) markTraefikUsers(key string) {
	seen := map[string]bool{key: true}
	level := []string{key}
	for depth := 1; depth <= traefikMaxDepth && len(level) > 0; depth++ {
		var next []string
		for _, k := range level {
			for _, g := range traefikGroups {
				for _, source := range traefikRouteSources {
					s.markIndexed(traefikRouteGVR(g, source), indexByTraefikService, k, markRoute(source))
				}
				s.markIndexed(gvrTraefikService(g), indexByTraefikService, k, func(_ *Source, o any) {
					if u, ok := o.(*unstructured.Unstructured); ok {
						if p := u.GetNamespace() + "/" + u.GetName(); !seen[p] {
							seen[p] = true
							next = append(next, p)
						}
					}
				})
			}
		}
		level = next
	}
}

// traefikLookup : TraefikService du cache, traefik.io d'abord.
func (s *Source) traefikLookup() TraefikLookup {
	return func(ns, name string) (*unstructured.Unstructured, bool) {
		for _, g := range traefikGroups {
			if u, _ := s.dynGet(gvrTraefikService(g), ns+"/"+name); u != nil {
				return u, true
			}
		}
		return nil, false
	}
}
