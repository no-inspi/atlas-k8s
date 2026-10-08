package kube

import (
	"context"
	"log/slog"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"

	"github.com/no-inspi/atlas-k8s/internal/model"
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

// dynKinds : registre complet. Les types démarrent en parallèle, dans un ordre
// quelconque : quand un TraefikService ou un Gateway démarre (ou change), ses
// objets remarquent les routes qui les visent (index), qui convergent donc
// quel que soit l'ordre. Ajouter un type, c'est ajouter une entrée.
var dynKinds = []dynKind{
	traefikServiceKind("traefik.io"),
	traefikServiceKind("traefik.containo.us"),
	ingressRouteKind("traefik.io"),
	ingressRouteKind("traefik.containo.us"),
	traefikRouteKind("traefik.io", model.SourceIngressRouteTCP),
	traefikRouteKind("traefik.containo.us", model.SourceIngressRouteTCP),
	traefikRouteKind("traefik.io", model.SourceIngressRouteUDP),
	traefikRouteKind("traefik.containo.us", model.SourceIngressRouteUDP),
	gatewayKind(),
	gatewayRouteKind(model.SourceHTTPRoute),
	gatewayRouteKind(model.SourceGRPCRoute),
}

func ingressRouteKind(g string) dynKind {
	return dynKind{gvr: gvrIngressRoute(g), on: (*Source).onIngressRoute, indexers: traefikIndexers}
}

type dynInformer struct {
	kind dynKind
	inf  cache.SharedIndexInformer
	stop chan struct{}
}

const (
	// dynSyncTimeout borne l'attente du premier list d'un essai de démarrage.
	dynSyncTimeout = 30 * time.Second
	// dynRetryInterval : délai avant un nouvel essai d'un type servi dont le
	// démarrage a échoué pour une raison passagère.
	dynRetryInterval = 30 * time.Second
	// dynStartupWait borne l'attente des types servis avant le premier snapshot.
	dynStartupWait = 30 * time.Second
)

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
// tourne déjà. Il démarre le type hors du suivi des types voulus (wantDyn) :
// réservé aux tests ; le code de production passe par wantDyn.
func (s *Source) startDyn(ctx context.Context, k dynKind) bool {
	ok, _ := s.tryStartDyn(ctx, k)
	return ok
}

// tryStartDyn : comme startDyn ; retry signale un échec passager (API server
// lent ou en erreur), qui vaut un nouvel essai. Refus (403), type absent (404)
// ou ctx annulé : pas de nouvel essai, sauf événement de CRD (wantDyn).
//
// Pas de verrou pendant les attentes : deux essais concurrents du même type ne
// coûtent qu'un list ; l'inscription, sous dynMu, n'en garde qu'un, et jamais
// après l'annulation de ctx (unwantDyn, stopAllDyn annulent avant de retirer).
func (s *Source) tryStartDyn(ctx context.Context, k dynKind) (ok, retry bool) {
	dyn := s.opts.Dynamic
	if dyn == nil {
		return false, false
	}
	if s.dynIndexer(k.gvr) != nil {
		return true, false
	}
	if !s.probe(ctx, k.crd(), func(ctx context.Context) error {
		_, err := dyn.Resource(k.gvr).List(ctx, probeOpts)
		return err
	}) {
		return false, false
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
	syncCtx, cancel := context.WithTimeout(ctx, s.opts.dynSync)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), inf.HasSynced) {
		close(stop)
		if ctx.Err() != nil {
			return false, false
		}
		s.opts.Log.Warn("type dynamique non synchronisé", "type", k.crd())
		return false, true
	}
	s.dynMu.Lock()
	if _, running := s.dyn[k.gvr]; running || ctx.Err() != nil {
		s.dynMu.Unlock()
		close(stop) // un autre essai l'a emporté, ou le type n'est plus voulu
		return running, false
	}
	s.dyn[k.gvr] = &dynInformer{kind: k, inf: inf, stop: stop}
	s.dynMu.Unlock()
	// Les événements reçus avant l'inscription ne trouvaient pas le type : on remarque tout.
	for _, o := range inf.GetStore().List() {
		k.on(s, o)
	}
	s.opts.Log.Info("type dynamique démarré", "type", k.crd())
	return true, false
}

// stopDyn arrête un type et retire ses objets du flux. Le type quitte d'abord
// le registre, puis chaque objet est marqué : la réconciliation, qui ne le
// trouve plus, publie leur suppression (et recalcule ce qui en dépendait).
// Seul celui qui retire l'informer du registre ferme son canal.
func (s *Source) stopDyn(gvr schema.GroupVersionResource) {
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

// stopAllDyn : arrêt de la source, sans rien publier. Le ctx de Run est déjà
// annulé : aucun essai en cours ne peut plus s'inscrire.
func (s *Source) stopAllDyn() {
	s.wantMu.Lock()
	for gvr, w := range s.wants {
		w.cancel()
		delete(s.wants, gvr)
	}
	s.wantMu.Unlock()
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	for gvr, d := range s.dyn {
		close(d.stop)
		delete(s.dyn, gvr)
	}
}

// dynWant : type servi (CRD présente, ou trouvé par la découverte en repli),
// que la source cherche à faire tourner.
type dynWant struct {
	ctx    context.Context // annulé quand le type n'est plus servi
	cancel context.CancelFunc
	busy   bool // un essai (ou l'attente d'un nouvel essai) est en cours ; sous wantMu
	// again : événement de CRD reçu pendant un essai ou son attente (capacité 1,
	// envoi sous wantMu). Il vaut un nouvel essai immédiat : sans lui, un 404
	// de la sonde d'une CRD pas encore servie perdrait l'UPDATE Established.
	again chan struct{}
	once  sync.Once
	first chan struct{} // fermé à la fin du premier essai
}

// wantDyn déclare un type servi et lance, s'il ne tourne pas et qu'aucun essai
// n'est en cours, un essai de démarrage en arrière-plan, renouvelé toutes les
// dynRetry tant que l'échec est passager. Si un essai est en cours, il en
// demande un nouveau dès sa fin, quel qu'en soit le résultat. Ne bloque pas.
// Le canal rendu est fermé à la fin du premier essai.
func (s *Source) wantDyn(ctx context.Context, k dynKind) <-chan struct{} {
	s.wantMu.Lock()
	defer s.wantMu.Unlock()
	w := s.wants[k.gvr]
	if w == nil {
		wctx, cancel := context.WithCancel(ctx)
		w = &dynWant{ctx: wctx, cancel: cancel, again: make(chan struct{}, 1), first: make(chan struct{})}
		s.wants[k.gvr] = w
	}
	switch {
	case w.busy:
		select {
		case w.again <- struct{}{}:
		default: // déjà demandé
		}
	case s.dynIndexer(k.gvr) == nil:
		w.busy = true
		go s.keepStarting(w, k)
	}
	return w.first
}

func (s *Source) keepStarting(w *dynWant, k dynKind) {
	defer w.once.Do(func() { close(w.first) })
	for {
		ok, retry := s.tryStartDyn(w.ctx, k)
		w.once.Do(func() { close(w.first) })
		// La décision de s'arrêter et busy=false sous le même verrou que
		// l'envoi de wantDyn : aucune demande ne se perd entre les deux.
		s.wantMu.Lock()
		again := false
		select {
		case <-w.again:
			again = true
		default:
		}
		if w.ctx.Err() != nil || (!again && (ok || !retry)) {
			w.busy = false
			s.wantMu.Unlock()
			return
		}
		s.wantMu.Unlock()
		if again && !retry { // après un succès, un 404 ou un 403 : sans délai
			s.opts.Log.Debug("essai relancé par un événement de CRD", "type", k.crd())
			continue
		}
		if !s.waitRetry(w, k, again) {
			return
		}
	}
}

// waitRetry : attente après un échec passager, dynRetry, ramenée à dynAgain
// (depuis la fin de l'essai) par un événement de CRD : une CRD modifiée en
// continu pendant que l'API server est lent ne relance pas plus d'un essai
// par dynAgain. Faux si le type n'est plus voulu.
func (s *Source) waitRetry(w *dynWant, k dynKind, again bool) bool {
	end := time.Now()
	delay := s.opts.dynRetry
	if again {
		delay = s.opts.dynAgain
		s.opts.Log.Debug("essai relancé par un événement de CRD", "type", k.crd(), "dans", delay)
	} else {
		s.opts.Log.Info("nouvel essai du type dynamique", "type", k.crd(), "dans", delay)
	}
	t := time.NewTimer(delay)
	defer t.Stop()
	for {
		select {
		case <-w.ctx.Done():
			s.wantMu.Lock()
			w.busy = false
			s.wantMu.Unlock()
			return false
		case <-w.again:
			if !again {
				again = true
				s.opts.Log.Debug("essai relancé par un événement de CRD", "type", k.crd())
				t.Reset(time.Until(end.Add(s.opts.dynAgain)))
			}
		case <-t.C:
			return true
		}
	}
}

// unwantDyn : le type n'est plus servi ; ses essais cessent et il s'arrête.
func (s *Source) unwantDyn(gvr schema.GroupVersionResource) {
	s.wantMu.Lock()
	if w := s.wants[gvr]; w != nil {
		w.cancel() // avant stopDyn : un essai en cours ne s'inscrira plus
		delete(s.wants, gvr)
	}
	s.wantMu.Unlock()
	s.stopDyn(gvr)
}

// wantedFirsts : fins des premiers essais des types servis.
func (s *Source) wantedFirsts() []<-chan struct{} {
	s.wantMu.Lock()
	defer s.wantMu.Unlock()
	out := make([]<-chan struct{}, 0, len(s.wants))
	for _, w := range s.wants {
		out = append(out, w.first)
	}
	return out
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
