package kube

import (
	"context"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Types optionnels apportés par une CRD (Traefik, Gateway API) : un informer
// dynamique par ressource, lu en unstructured, démarré quand la CRD est servie
// et arrêté quand elle disparaît (crd.go).

// Groupes Traefik : v3 (traefik.io), puis l'ancien groupe v2. À nom égal, traefik.io l'emporte.
var traefikGroups = []string{"traefik.io", "traefik.containo.us"}

func gvrIngressRoute(g string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "ingressroutes"}
}

// dynKind : type optionnel apporté par une CRD. on et indexers ne doivent pas
// référencer dynKinds (cycle d'initialisation) : passer par s.dynRunning().
type dynKind struct {
	gvr      schema.GroupVersionResource
	on       func(s *Source, o any) // marque l'objet (et ce qui en dépend)
	indexers func(s *Source) cache.Indexers
}

// crd : nom de la CRD qui apporte le type.
func (k dynKind) crd() string { return k.gvr.Resource + "." + k.gvr.Group }

// dynKinds : registre complet, dans l'ordre de démarrage. Ajouter un type,
// c'est ajouter une entrée.
var dynKinds = []dynKind{
	ingressRouteKind("traefik.io"),
	ingressRouteKind("traefik.containo.us"),
}

func ingressRouteKind(g string) dynKind {
	return dynKind{gvr: gvrIngressRoute(g), on: (*Source).onIngressRoute, indexers: func(*Source) cache.Indexers {
		return cache.Indexers{indexByBackend: func(o any) ([]string, error) {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return nil, nil
			}
			return routeBackends(ConvertIngressRoute(u, nil)), nil
		}}
	}}
}

type dynInformer struct {
	kind dynKind
	inf  cache.SharedIndexInformer
	stop chan struct{}
}

// dynSyncTimeout borne l'attente du premier list d'un type démarré à chaud.
const dynSyncTimeout = 30 * time.Second

// dynIndexer : cache du type, nil s'il n'est pas démarré.
func (s *Source) dynIndexer(gvr schema.GroupVersionResource) cache.Indexer {
	s.dynMu.RLock()
	defer s.dynMu.RUnlock()
	if d := s.dyn[gvr]; d != nil {
		return d.inf.GetIndexer()
	}
	return nil
}

// dynRunning : types démarrés, dans un ordre quelconque.
func (s *Source) dynRunning() []*dynInformer {
	s.dynMu.RLock()
	defer s.dynMu.RUnlock()
	out := make([]*dynInformer, 0, len(s.dyn))
	for _, d := range s.dyn {
		out = append(out, d)
	}
	return out
}

// startDyn démarre l'informer d'un type si la sonde l'autorise, attend sa
// synchronisation, l'inscrit puis marque tous ses objets. Sans effet s'il
// tourne déjà.
func (s *Source) startDyn(ctx context.Context, k dynKind) bool {
	dyn := s.opts.Dynamic
	if dyn == nil {
		return false
	}
	s.dynStart.Lock()
	defer s.dynStart.Unlock()
	if s.dynIndexer(k.gvr) != nil {
		return true
	}
	if !s.probe(ctx, k.crd(), func(ctx context.Context) error {
		_, err := dyn.Resource(k.gvr).List(ctx, probeOpts)
		return err
	}) {
		return false
	}
	inf := dynamicinformer.NewFilteredDynamicInformer(dyn, k.gvr, metav1.NamespaceAll, 0,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, nil).Informer()
	_ = inf.SetTransform(transform)
	if k.indexers != nil {
		_ = inf.AddIndexers(k.indexers(s))
	}
	s.handle(inf, func(o any) { k.on(s, o) })
	stop := make(chan struct{})
	go inf.Run(stop)
	syncCtx, cancel := context.WithTimeout(ctx, dynSyncTimeout)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), inf.HasSynced) {
		close(stop)
		s.opts.Log.Warn("type dynamique non synchronisé", "type", k.crd())
		return false
	}
	s.dynMu.Lock()
	s.dyn[k.gvr] = &dynInformer{kind: k, inf: inf, stop: stop}
	s.dynMu.Unlock()
	// Les événements reçus avant l'inscription ne trouvaient pas le type : on remarque tout.
	for _, o := range inf.GetStore().List() {
		k.on(s, o)
	}
	s.opts.Log.Info("type dynamique démarré", "type", k.crd())
	return true
}

// stopDyn arrête un type et retire ses objets du flux. Le type quitte d'abord
// le registre, puis chaque objet est marqué : la réconciliation, qui ne le
// trouve plus, publie leur suppression (et recalcule ce qui en dépendait).
func (s *Source) stopDyn(gvr schema.GroupVersionResource) {
	s.dynStart.Lock()
	defer s.dynStart.Unlock()
	s.dynMu.Lock()
	d := s.dyn[gvr]
	delete(s.dyn, gvr)
	s.dynMu.Unlock()
	if d == nil {
		return
	}
	close(d.stop)
	for _, o := range d.inf.GetStore().List() {
		d.kind.on(s, o)
	}
	s.opts.Log.Info("type dynamique arrêté", "type", d.kind.crd())
}

// stopAllDyn : arrêt de la source, sans rien publier.
func (s *Source) stopAllDyn() {
	s.dynStart.Lock()
	defer s.dynStart.Unlock()
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	for gvr, d := range s.dyn {
		close(d.stop)
		delete(s.dyn, gvr)
	}
}

// markDynamic marque tous les objets des types démarrés.
func (s *Source) markDynamic() {
	for _, d := range s.dynRunning() {
		for _, o := range d.inf.GetStore().List() {
			d.kind.on(s, o)
		}
	}
}

// servedKinds : types du registre que l'API server sert, d'après la découverte.
// Repli quand les CRD ne peuvent pas être suivies : un type installé ensuite
// attend le prochain redémarrage.
func servedKinds(d discovery.DiscoveryInterface, log *slog.Logger) []dynKind {
	served := map[string]map[string]bool{} // groupe/version → ressources servies
	var out []dynKind
	for _, k := range dynKinds {
		gv := k.gvr.GroupVersion().String()
		res, ok := served[gv]
		if !ok {
			res = map[string]bool{}
			rl, err := d.ServerResourcesForGroupVersion(gv)
			if err != nil && !apierrors.IsNotFound(err) {
				log.Warn("découverte impossible", "groupVersion", gv, "err", err)
			}
			if rl != nil {
				for _, r := range rl.APIResources {
					res[r.Name] = true
				}
			}
			served[gv] = res
		}
		if res[k.gvr.Resource] {
			out = append(out, k)
		}
	}
	return out
}

// startDynamic démarre les types servis au démarrage (remplacé par le suivi des CRD, crd.go).
func (s *Source) startDynamic(ctx context.Context) {
	if s.opts.Dynamic == nil {
		return
	}
	for _, k := range servedKinds(s.client.Discovery(), s.opts.Log) {
		s.startDyn(ctx, k)
	}
}
