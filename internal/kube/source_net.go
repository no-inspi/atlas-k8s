package kube

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"

	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// Réseau et stockage (jalon 8) : Services et EndpointSlices, Ingress et
// IngressClass, PVC, IngressRoute Traefik (traefik.go).

const (
	indexByClaim   = "claim"   // pods par PVC monté : « ns/claim »
	indexByService = "service" // EndpointSlices par Service : « ns/name »
	indexByBackend = "backend" // routes par Service visé : « ns/name »
)

var probeOpts = metav1.ListOptions{Limit: 1}

// probe : list d'essai. Refusé (403) ou absent (404) : le type est désactivé
// plutôt que de bloquer la synchronisation des caches. Une autre erreur
// (API server momentanément injoignable) laisse l'informer réessayer.
func (s *Source) probe(ctx context.Context, what string, list func(context.Context) error) bool {
	err := list(ctx)
	if err != nil && (apierrors.IsForbidden(err) || apierrors.IsNotFound(err)) {
		s.opts.Log.Warn("type désactivé : le ServiceAccount ne peut pas le lister", "type", what, "err", err)
		return false
	}
	return true
}

// startNetwork branche les informers du jalon 8 ; à appeler avant factory.Start.
func (s *Source) startNetwork(ctx context.Context) {
	c, f := s.client, s.factory
	if s.probe(ctx, "services", func(ctx context.Context) error { _, err := c.CoreV1().Services("").List(ctx, probeOpts); return err }) {
		inf := f.Core().V1().Services()
		s.services = inf.Lister()
		s.watch(inf.Informer(), s.onService)
	}
	if s.probe(ctx, "endpointslices", func(ctx context.Context) error {
		_, err := c.DiscoveryV1().EndpointSlices("").List(ctx, probeOpts)
		return err
	}) {
		inf := f.Discovery().V1().EndpointSlices().Informer()
		_ = inf.AddIndexers(cache.Indexers{indexByService: func(o any) ([]string, error) {
			e := o.(*discoveryv1.EndpointSlice)
			if n := e.Labels[discoveryv1.LabelServiceName]; n != "" {
				return []string{e.Namespace + "/" + n}, nil
			}
			return nil, nil
		}})
		s.slices = inf.GetIndexer()
		s.watch(inf, s.onEndpointSlice)
	}
	if s.probe(ctx, "persistentvolumeclaims", func(ctx context.Context) error {
		_, err := c.CoreV1().PersistentVolumeClaims("").List(ctx, probeOpts)
		return err
	}) {
		inf := f.Core().V1().PersistentVolumeClaims()
		s.pvcs = inf.Lister()
		s.watch(inf.Informer(), s.onPVC)
	}
	if s.probe(ctx, "ingressclasses", func(ctx context.Context) error {
		_, err := c.NetworkingV1().IngressClasses().List(ctx, probeOpts)
		return err
	}) {
		inf := f.Networking().V1().IngressClasses()
		s.classes = inf.Lister()
		s.watch(inf.Informer(), s.onIngressClass)
	}
	if s.probe(ctx, "ingresses", func(ctx context.Context) error {
		_, err := c.NetworkingV1().Ingresses("").List(ctx, probeOpts)
		return err
	}) {
		inf := f.Networking().V1().Ingresses()
		_ = inf.Informer().AddIndexers(cache.Indexers{indexByBackend: func(o any) ([]string, error) {
			return routeBackends(ConvertIngress(o.(*networkingv1.Ingress), "", nil)), nil
		}})
		s.ingresses, s.ingressIdx = inf.Lister(), inf.Informer().GetIndexer()
		s.watch(inf.Informer(), s.onIngress)
	}
	s.startTraefik(ctx)
}

/* ---------- événements ---------- */

// onService : le Service, et les routes qui le visent (leur backend apparaît ou disparaît).
func (s *Source) onService(o any) {
	sv, ok := o.(*corev1.Service)
	if !ok {
		return
	}
	id := sv.Namespace + "/" + sv.Name
	s.mark(ref{stream.KindService, id})
	s.markRoutesTo(id)
}

func (s *Source) markRoutesTo(svc string) {
	if s.ingressIdx != nil {
		objs, _ := s.ingressIdx.ByIndex(indexByBackend, svc)
		for _, o := range objs {
			s.onIngress(o)
		}
	}
	for _, t := range s.traefik {
		objs, _ := t.index.ByIndex(indexByBackend, svc)
		for _, o := range objs {
			s.onIngressRoute(o)
		}
	}
}

func (s *Source) onEndpointSlice(o any) {
	e, ok := o.(*discoveryv1.EndpointSlice)
	if !ok {
		return
	}
	if n := e.Labels[discoveryv1.LabelServiceName]; n != "" {
		s.mark(ref{stream.KindService, e.Namespace + "/" + n})
	}
}

func (s *Source) onPVC(o any) {
	if p, ok := o.(*corev1.PersistentVolumeClaim); ok {
		s.mark(ref{stream.KindVolume, p.Namespace + "/" + p.Name})
	}
}

func (s *Source) onIngress(o any) {
	if i, ok := o.(*networkingv1.Ingress); ok {
		s.mark(ref{stream.KindRoute, "Ingress/" + i.Namespace + "/" + i.Name})
	}
}

// onIngressClass : la classe par défaut a pu changer, toutes les Ingress sont à revoir.
func (s *Source) onIngressClass(any) {
	if s.ingresses == nil {
		return
	}
	all, _ := s.ingresses.List(labels.Everything())
	for _, i := range all {
		s.onIngress(i)
	}
}

func (s *Source) onIngressRoute(o any) {
	if u, ok := o.(*unstructured.Unstructured); ok {
		s.mark(ref{stream.KindRoute, "IngressRoute/" + u.GetNamespace() + "/" + u.GetName()})
	}
}

func (s *Source) markNetwork() {
	sel := labels.Everything()
	if s.services != nil {
		all, _ := s.services.List(sel)
		for _, o := range all {
			s.onService(o)
		}
	}
	if s.pvcs != nil {
		all, _ := s.pvcs.List(sel)
		for _, o := range all {
			s.onPVC(o)
		}
	}
	if s.ingresses != nil {
		all, _ := s.ingresses.List(sel)
		for _, o := range all {
			s.onIngress(o)
		}
	}
	for _, t := range s.traefik {
		all, _ := t.lister.List(sel)
		for _, o := range all {
			s.onIngressRoute(o)
		}
	}
}

/* ---------- construction ---------- */

func (s *Source) buildService(id string) (any, string, error) {
	if s.services == nil {
		return nil, "", nil
	}
	ns, name, _ := cache.SplitMetaNamespaceKey(id)
	sv, err := s.services.Services(ns).Get(name)
	if err != nil {
		return nil, "", err
	}
	var slices []*discoveryv1.EndpointSlice
	if s.slices != nil {
		objs, _ := s.slices.ByIndex(indexByService, id)
		for _, o := range objs {
			slices = append(slices, o.(*discoveryv1.EndpointSlice))
		}
	}
	m, publish := ConvertService(sv, slices)
	if !publish {
		return nil, "", nil
	}
	return m, id, nil
}

func (s *Source) serviceExists() ServiceExists {
	if s.services == nil {
		return nil
	}
	return func(ns, name string) bool {
		_, err := s.services.Services(ns).Get(name)
		return err == nil
	}
}

func (s *Source) buildRoute(id string) (any, string, error) {
	source, rest, _ := strings.Cut(id, "/")
	ns, name, _ := cache.SplitMetaNamespaceKey(rest)
	switch source {
	case "Ingress":
		if s.ingresses == nil {
			return nil, "", nil
		}
		i, err := s.ingresses.Ingresses(ns).Get(name)
		if err != nil {
			return nil, "", err
		}
		var classes []*networkingv1.IngressClass
		if s.classes != nil {
			classes, _ = s.classes.List(labels.Everything())
		}
		return ConvertIngress(i, IngressGate(i, classes), s.serviceExists()), id, nil
	case "IngressRoute":
		// traefik.io avant traefik.containo.us (ordre de traefikGroups).
		for _, t := range s.traefik {
			o, err := t.lister.ByNamespace(ns).Get(name)
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return nil, "", err
			}
			return ConvertIngressRoute(o.(*unstructured.Unstructured), s.serviceExists()), id, nil
		}
		return nil, "", nil
	}
	return nil, "", fmt.Errorf("route inconnue %q", id)
}

func (s *Source) buildVolume(id string) (any, string, error) {
	if s.pvcs == nil {
		return nil, "", nil
	}
	ns, name, _ := cache.SplitMetaNamespaceKey(id)
	p, err := s.pvcs.PersistentVolumeClaims(ns).Get(name)
	if err != nil {
		return nil, "", err
	}
	objs, _ := s.podIndex.ByIndex(indexByClaim, id)
	uids := []string{}
	for _, o := range objs {
		pod := o.(*corev1.Pod)
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		uids = append(uids, string(pod.UID))
	}
	sort.Strings(uids)
	return ConvertPVC(p, uids), id, nil
}
