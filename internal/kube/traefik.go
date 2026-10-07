package kube

import (
	"context"

	"k8s.io/client-go/tools/cache"
)

// traefikInformer : cache des IngressRoute d'un groupe Traefik.
type traefikInformer struct {
	group  string
	lister cache.GenericLister
	index  cache.Indexer
}

func (s *Source) startTraefik(context.Context) {}
