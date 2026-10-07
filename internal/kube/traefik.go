package kube

import (
	"context"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Groupes des IngressRoute Traefik : v3 (traefik.io), puis l'ancien groupe v2.
var traefikGroups = []string{"traefik.io", "traefik.containo.us"}

// traefikInformer : cache des IngressRoute d'un groupe Traefik.
type traefikInformer struct {
	group  string
	lister cache.GenericLister
	index  cache.Indexer
}

// traefikGroupsServed : groupes dont l'API server sert les IngressRoute.
// La découverte a lieu au démarrage : une CRD installée ensuite est prise en
// compte au prochain redémarrage.
func traefikGroupsServed(d discovery.DiscoveryInterface, log *slog.Logger) []string {
	var out []string
	for _, g := range traefikGroups {
		rl, err := d.ServerResourcesForGroupVersion(g + "/v1alpha1")
		if err != nil {
			if !apierrors.IsNotFound(err) {
				log.Warn("découverte Traefik impossible", "group", g, "err", err)
			}
			continue
		}
		for _, r := range rl.APIResources {
			if r.Name == "ingressroutes" {
				out = append(out, g)
				break
			}
		}
	}
	if len(out) > 0 {
		log.Info("IngressRoute Traefik servies", "groups", out)
	}
	return out
}

func (s *Source) startTraefik(ctx context.Context) {
	dyn := s.opts.Dynamic
	if dyn == nil {
		return
	}
	for _, g := range traefikGroupsServed(s.client.Discovery(), s.opts.Log) {
		gvr := schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "ingressroutes"}
		if !s.probe(ctx, "ingressroutes."+g, func(ctx context.Context) error {
			_, err := dyn.Resource(gvr).List(ctx, probeOpts)
			return err
		}) {
			continue
		}
		if s.dynFactory == nil {
			s.dynFactory = dynamicinformer.NewDynamicSharedInformerFactory(dyn, 0)
		}
		inf := s.dynFactory.ForResource(gvr)
		_ = inf.Informer().SetTransform(transform)
		_ = inf.Informer().AddIndexers(cache.Indexers{indexByBackend: func(o any) ([]string, error) {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return nil, nil
			}
			return routeBackends(ConvertIngressRoute(u, nil)), nil
		}})
		s.traefik = append(s.traefik, traefikInformer{group: g, lister: inf.Lister(), index: inf.Informer().GetIndexer()})
		s.watch(inf.Informer(), s.onIngressRoute)
	}
}
