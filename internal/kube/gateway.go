package kube

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// Gateway API dans la source : Gateways (portes), HTTPRoute et GRPCRoute, par le registre dynamique.

var (
	gvrGateway   = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "gateways"}
	gvrHTTPRoute = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "httproutes"}
	gvrGRPCRoute = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "grpcroutes"}
)

const indexByGateway = "gateway" // routes par Gateway parent : « ns/name »

var gatewayRouteSources = []string{model.SourceHTTPRoute, model.SourceGRPCRoute}

func gatewayRouteGVR(source string) schema.GroupVersionResource {
	if source == model.SourceGRPCRoute {
		return gvrGRPCRoute
	}
	return gvrHTTPRoute
}

func gatewayKind() dynKind { return dynKind{gvr: gvrGateway, on: (*Source).onGateway} }

func gatewayRouteKind(source string) dynKind {
	return dynKind{gvr: gatewayRouteGVR(source), on: markRoute(source), indexers: func(*Source) cache.Indexers {
		return cache.Indexers{
			indexByBackend: func(o any) ([]string, error) {
				u, ok := o.(*unstructured.Unstructured)
				if !ok {
					return nil, nil
				}
				return routeBackends(ConvertGatewayRoute(u, source, nil)), nil
			},
			indexByGateway: func(o any) ([]string, error) {
				u, ok := o.(*unstructured.Unstructured)
				if !ok {
					return nil, nil
				}
				return parentGateways(u), nil
			},
		}
	}}
}

// markRoute : marqueur des routes d'une source, clé « Source/ns/name ».
func markRoute(source string) func(*Source, any) {
	return func(s *Source, o any) {
		if u, ok := o.(*unstructured.Unstructured); ok {
			s.mark(ref{stream.KindRoute, source + "/" + u.GetNamespace() + "/" + u.GetName()})
		}
	}
}

// onGateway : le Gateway, et les routes qui s'y rattachent.
func (s *Source) onGateway(o any) {
	u, ok := o.(*unstructured.Unstructured)
	if !ok {
		return
	}
	id := u.GetNamespace() + "/" + u.GetName()
	s.mark(ref{stream.KindGateway, id})
	for _, source := range gatewayRouteSources {
		s.markIndexed(gatewayRouteGVR(source), indexByGateway, id, markRoute(source))
	}
}

// markIndexed applique on aux objets d'un type démarré qui ont cette clé d'index.
func (s *Source) markIndexed(gvr schema.GroupVersionResource, index, key string, on func(*Source, any)) {
	idx := s.dynIndexer(gvr)
	if idx == nil {
		return
	}
	objs, err := idx.ByIndex(index, key)
	if err != nil {
		return // type sans cet index
	}
	for _, o := range objs {
		on(s, o)
	}
}

// dynGet : objet « ns/name » d'un type du registre ; nil si le type n'est pas
// démarré ou si l'objet n'existe pas.
func (s *Source) dynGet(gvr schema.GroupVersionResource, id string) (*unstructured.Unstructured, error) {
	idx := s.dynIndexer(gvr)
	if idx == nil {
		return nil, nil
	}
	o, ok, err := idx.GetByKey(id)
	if err != nil || !ok {
		return nil, err
	}
	u, _ := o.(*unstructured.Unstructured)
	return u, nil
}

func (s *Source) buildGateway(id string) (any, string, error) {
	u, err := s.dynGet(gvrGateway, id)
	if err != nil || u == nil {
		return nil, "", err
	}
	return ConvertGateway(u), id, nil
}
