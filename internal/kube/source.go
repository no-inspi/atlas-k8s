package kube

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	appslisters "k8s.io/client-go/listers/apps/v1"
	batchlisters "k8s.io/client-go/listers/batch/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	networkinglisters "k8s.io/client-go/listers/networking/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// Sink reçoit l'état du cluster ; *stream.Hub l'implémente.
type Sink interface {
	Upsert(kind stream.Kind, key string, obj any)
	Delete(kind stream.Kind, key string, obj any)
	MarkReady()
}

type Options struct {
	Node NodeOptions
	// ReconcileInterval regroupe les événements des informers (100 ms par défaut).
	ReconcileInterval time.Duration
	Log               *slog.Logger
	// Dynamic lit les types apportés par une CRD (Traefik, Gateway API) ; nil : aucun.
	Dynamic dynamic.Interface

	// Réglages des types dynamiques, raccourcis par les tests (zéro : valeur par défaut).
	dynSync  time.Duration // attente du premier list d'un essai de démarrage (30 s)
	dynRetry time.Duration // délai avant un nouvel essai après une erreur passagère (30 s)
	dynWait  time.Duration // attente globale des types servis au démarrage (30 s)
}

const (
	indexByNode     = "node"
	indexByOwner    = "owner"
	indexByInvolved = "involved"
)

// ref identifie un objet Kubernetes dans la file de réconciliation.
// id vaut "ns/name" (pods, workloads « Kind/ns/name ») ou "name" (nodes, namespaces).
type ref struct {
	kind stream.Kind
	id   string
}

// Source lit le cluster par des informers partagés (un watch par type, avec le
// ServiceAccount) et publie le modèle réduit. Chaque événement marque des objets
// « sales » ; une boucle les recalcule depuis les listers et ne publie que ce
// qui a changé.
type Source struct {
	opts    Options
	sink    Sink
	factory informers.SharedInformerFactory

	pods         corelisters.PodLister
	podIndex     cache.Indexer
	nodes        corelisters.NodeLister
	namespaces   corelisters.NamespaceLister
	deployments  appslisters.DeploymentLister
	replicasets  appslisters.ReplicaSetLister
	statefulsets appslisters.StatefulSetLister
	daemonsets   appslisters.DaemonSetLister
	jobs         batchlisters.JobLister
	events       cache.Indexer
	client       kubernetes.Interface

	// Jalon 8 : types optionnels, nil quand le ServiceAccount ne peut pas les lister.
	services   corelisters.ServiceLister
	slices     cache.Indexer
	pvcs       corelisters.PersistentVolumeClaimLister
	ingresses  networkinglisters.IngressLister
	ingressIdx cache.Indexer
	classes    networkinglisters.IngressClassLister

	// Jalon 9 : types apportés par une CRD, démarrés et arrêtés à chaud (dynkinds.go, crd.go).
	dynMu  sync.RWMutex
	dyn    map[schema.GroupVersionResource]*dynInformer // types démarrés
	wantMu sync.Mutex
	wants  map[schema.GroupVersionResource]*dynWant // types servis, démarrés ou en cours de démarrage

	synced []cache.InformerSynced

	mu    sync.Mutex
	dirty map[ref]struct{}
	// last garde le dernier modèle publié par objet : il sert à ne republier que
	// les vrais changements et à envoyer un delete avec le bon UID.
	last map[ref]any
}

func NewSource(client kubernetes.Interface, sink Sink, opts Options) *Source {
	if opts.ReconcileInterval == 0 {
		opts.ReconcileInterval = 100 * time.Millisecond
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	opts.dynSync = cmp.Or(opts.dynSync, dynSyncTimeout)
	opts.dynRetry = cmp.Or(opts.dynRetry, dynRetryInterval)
	opts.dynWait = cmp.Or(opts.dynWait, dynStartupWait)
	f := informers.NewSharedInformerFactoryWithOptions(client, 0, informers.WithTransform(transform))
	s := &Source{opts: opts, sink: sink, factory: f, client: client, dirty: map[ref]struct{}{}, last: map[ref]any{},
		dyn: map[schema.GroupVersionResource]*dynInformer{}, wants: map[schema.GroupVersionResource]*dynWant{}}

	pods := f.Core().V1().Pods()
	_ = pods.Informer().AddIndexers(cache.Indexers{
		indexByNode: func(obj any) ([]string, error) { return []string{obj.(*corev1.Pod).Spec.NodeName}, nil },
		indexByOwner: func(obj any) ([]string, error) {
			if c := metav1.GetControllerOf(obj.(*corev1.Pod)); c != nil {
				return []string{string(c.UID)}, nil
			}
			return nil, nil
		},
		indexByClaim: func(obj any) ([]string, error) {
			p := obj.(*corev1.Pod)
			var out []string
			for _, c := range PodClaims(p) {
				out = append(out, p.Namespace+"/"+c)
			}
			return out, nil
		},
	})
	s.pods, s.podIndex = pods.Lister(), pods.Informer().GetIndexer()
	s.nodes = f.Core().V1().Nodes().Lister()
	s.namespaces = f.Core().V1().Namespaces().Lister()
	s.deployments = f.Apps().V1().Deployments().Lister()
	s.replicasets = f.Apps().V1().ReplicaSets().Lister()
	s.statefulsets = f.Apps().V1().StatefulSets().Lister()
	s.daemonsets = f.Apps().V1().DaemonSets().Lister()
	s.jobs = f.Batch().V1().Jobs().Lister()

	// Événements : cache partagé pour l'inspecteur, hors du flux ; on n'attend pas
	// leur synchronisation pour déclarer l'application prête.
	evs := f.Core().V1().Events().Informer()
	_ = evs.AddIndexers(cache.Indexers{indexByInvolved: func(obj any) ([]string, error) {
		o := obj.(*corev1.Event).InvolvedObject
		return []string{o.Namespace + "/" + o.Kind + "/" + o.Name}, nil
	}})
	s.events = evs.GetIndexer()

	s.watch(pods.Informer(), s.onPod)
	s.watch(f.Core().V1().Nodes().Informer(), func(o any) { s.mark(ref{stream.KindNode, o.(*corev1.Node).Name}) })
	s.watch(f.Core().V1().Namespaces().Informer(), func(o any) { s.mark(ref{stream.KindNamespace, o.(*corev1.Namespace).Name}) })
	s.watch(f.Apps().V1().Deployments().Informer(), s.onWorkload("Deployment"))
	s.watch(f.Apps().V1().ReplicaSets().Informer(), s.onReplicaSet)
	s.watch(f.Apps().V1().StatefulSets().Informer(), s.onWorkload("StatefulSet"))
	s.watch(f.Apps().V1().DaemonSets().Informer(), s.onWorkload("DaemonSet"))
	s.watch(f.Batch().V1().Jobs().Informer(), s.onWorkload("Job"))
	return s
}

// Run démarre les informers, attend leur synchronisation, démarre les types
// dynamiques, publie l'état complet, signale que le hub est prêt puis suit les
// changements.
func (s *Source) Run(ctx context.Context) error {
	s.startNetwork(ctx)
	s.factory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), s.synced...) {
		return errors.New("synchronisation des caches interrompue")
	}
	// Après les caches typés : l'état des backends des routes dynamiques est juste dès leur premier calcul.
	s.startDynamic(ctx)
	if ctx.Err() != nil {
		s.shutdown()
		return nil
	}
	if err := s.markAll(); err != nil {
		s.shutdown()
		return err
	}
	s.reconcile()
	s.sink.MarkReady()
	s.opts.Log.Info("caches synchronisés", "pods", len(s.podIndex.List()))

	t := time.NewTicker(s.opts.ReconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.shutdown()
			return nil
		case <-t.C:
			s.reconcile()
		}
	}
}

// shutdown arrête les informers, sans rien publier.
func (s *Source) shutdown() {
	s.factory.Shutdown()
	s.stopAllDyn()
}

/* ---------- événements ---------- */

func (s *Source) watch(inf cache.SharedIndexInformer, on func(any)) {
	s.synced = append(s.synced, inf.HasSynced)
	s.handle(inf, on)
}

// handle branche on sur les événements d'un informer, sans que /readyz l'attende.
func (s *Source) handle(inf cache.SharedIndexInformer, on func(any)) {
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: on,
		UpdateFunc: func(old, cur any) {
			on(old)
			on(cur)
		},
		DeleteFunc: func(obj any) {
			if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = d.Obj
			}
			on(obj)
		},
	})
}

func (s *Source) mark(refs ...ref) {
	s.mu.Lock()
	for _, r := range refs {
		s.dirty[r] = struct{}{}
	}
	s.mu.Unlock()
}

// onPod : le pod, et le node qui l'héberge (ses requests changent).
func (s *Source) onPod(o any) {
	p, ok := o.(*corev1.Pod)
	if !ok {
		return
	}
	s.mark(ref{stream.KindPod, p.Namespace + "/" + p.Name})
	if p.Spec.NodeName != "" {
		s.mark(ref{stream.KindNode, p.Spec.NodeName})
	}
	if s.pvcs != nil {
		for _, c := range PodClaims(p) {
			s.mark(ref{stream.KindVolume, p.Namespace + "/" + c})
		}
	}
}

func (s *Source) onWorkload(kind string) func(any) {
	return func(o any) {
		m, err := metaOf(o)
		if err == nil {
			s.mark(ref{stream.KindWorkload, kind + "/" + m.GetNamespace() + "/" + m.GetName()})
		}
	}
}

// onReplicaSet : le RS (publié s'il est orphelin) et ses pods, dont le
// propriétaire racine dépend du RS.
func (s *Source) onReplicaSet(o any) {
	rs, ok := o.(*appsv1.ReplicaSet)
	if !ok {
		return
	}
	s.mark(ref{stream.KindWorkload, "ReplicaSet/" + rs.Namespace + "/" + rs.Name})
	pods, _ := s.podIndex.ByIndex(indexByOwner, string(rs.UID))
	for _, p := range pods {
		s.onPod(p)
	}
}

func metaOf(o any) (metav1.Object, error) {
	m, ok := o.(metav1.Object)
	if !ok {
		return nil, fmt.Errorf("objet inattendu %T", o)
	}
	return m, nil
}

func (s *Source) markAll() error {
	sel := labels.Everything()
	nodes, err := s.nodes.List(sel)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		s.mark(ref{stream.KindNode, n.Name})
	}
	nss, _ := s.namespaces.List(sel)
	for _, n := range nss {
		s.mark(ref{stream.KindNamespace, n.Name})
	}
	pods, _ := s.pods.List(sel)
	for _, p := range pods {
		s.onPod(p)
	}
	ds, _ := s.deployments.List(sel)
	for _, o := range ds {
		s.onWorkload("Deployment")(o)
	}
	rss, _ := s.replicasets.List(sel)
	for _, o := range rss {
		s.onWorkload("ReplicaSet")(o)
	}
	sts, _ := s.statefulsets.List(sel)
	for _, o := range sts {
		s.onWorkload("StatefulSet")(o)
	}
	dss, _ := s.daemonsets.List(sel)
	for _, o := range dss {
		s.onWorkload("DaemonSet")(o)
	}
	jobs, _ := s.jobs.List(sel)
	for _, o := range jobs {
		s.onWorkload("Job")(o)
	}
	s.markNetwork()
	return nil
}

/* ---------- réconciliation ---------- */

func (s *Source) reconcile() {
	s.mu.Lock()
	dirty := s.dirty
	s.dirty = map[ref]struct{}{}
	s.mu.Unlock()

	for r := range dirty {
		obj, key, err := s.build(r)
		if err != nil && !apierrors.IsNotFound(err) {
			s.opts.Log.Warn("réconciliation impossible", "kind", r.kind, "id", r.id, "err", err)
			continue
		}
		prev, had := s.last[r]
		if obj == nil {
			if had {
				delete(s.last, r)
				s.sink.Delete(r.kind, publishedKey(r, prev), prev)
			}
			continue
		}
		if had && reflect.DeepEqual(prev, obj) {
			continue
		}
		// Un pod recréé sous le même nom (StatefulSet) change d'UID : on retire l'ancien.
		if had && publishedKey(r, prev) != key {
			s.sink.Delete(r.kind, publishedKey(r, prev), prev)
		}
		s.last[r] = obj
		s.sink.Upsert(r.kind, key, obj)
	}
}

// publishedKey : clé de l'objet dans le hub (UID pour un pod).
func publishedKey(r ref, obj any) string {
	if p, ok := obj.(model.Pod); ok {
		return p.UID
	}
	return r.id
}

// build renvoie le modèle à publier, ou nil si l'objet n'existe plus (ou ne
// doit pas être publié, comme un ReplicaSet géré par un Deployment).
func (s *Source) build(r ref) (any, string, error) {
	switch r.kind {
	case stream.KindNode:
		n, err := s.nodes.Get(r.id)
		if err != nil {
			return nil, "", err
		}
		return ConvertNode(n, s.requestedOn(n.Name), s.opts.Node), r.id, nil
	case stream.KindNamespace:
		n, err := s.namespaces.Get(r.id)
		if err != nil {
			return nil, "", err
		}
		return ConvertNamespace(n), r.id, nil
	case stream.KindPod:
		ns, name, _ := cache.SplitMetaNamespaceKey(r.id)
		p, err := s.pods.Pods(ns).Get(name)
		if err != nil {
			return nil, "", err
		}
		m := ConvertPod(p, RootOwner(p, s.replicasets))
		return m, m.UID, nil
	case stream.KindWorkload:
		w, err := s.workload(r.id)
		if err != nil || w == nil {
			return nil, "", err
		}
		return *w, r.id, nil
	case stream.KindService:
		return s.buildService(r.id)
	case stream.KindRoute:
		return s.buildRoute(r.id)
	case stream.KindVolume:
		return s.buildVolume(r.id)
	}
	return nil, "", fmt.Errorf("type inconnu %q", r.kind)
}

func (s *Source) workload(id string) (*model.Workload, error) {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("identifiant de workload invalide %q", id)
	}
	kind, ns, name := parts[0], parts[1], parts[2]
	var w model.Workload
	switch kind {
	case "Deployment":
		d, err := s.deployments.Deployments(ns).Get(name)
		if err != nil {
			return nil, err
		}
		w = DeploymentWorkload(d)
	case "ReplicaSet":
		r, err := s.replicasets.ReplicaSets(ns).Get(name)
		if err != nil {
			return nil, err
		}
		if c := metav1.GetControllerOf(r); c != nil && c.Kind == "Deployment" {
			return nil, nil // représenté par son Deployment
		}
		w = ReplicaSetWorkload(r)
	case "StatefulSet":
		o, err := s.statefulsets.StatefulSets(ns).Get(name)
		if err != nil {
			return nil, err
		}
		w = StatefulSetWorkload(o)
	case "DaemonSet":
		o, err := s.daemonsets.DaemonSets(ns).Get(name)
		if err != nil {
			return nil, err
		}
		w = DaemonSetWorkload(o)
	case "Job":
		o, err := s.jobs.Jobs(ns).Get(name)
		if err != nil {
			return nil, err
		}
		w = JobWorkload(o)
	default:
		return nil, fmt.Errorf("workload inconnu %q", kind)
	}
	return &w, nil
}

// requestedOn additionne les requests des pods non terminés du node, comme le
// scheduler pour décider s'il reste de la place.
func (s *Source) requestedOn(node string) model.Resources {
	objs, _ := s.podIndex.ByIndex(indexByNode, node)
	var r model.Resources
	for _, o := range objs {
		p := o.(*corev1.Pod)
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		req, _ := PodResources(p)
		r.CPU += req.CPU
		r.Memory += req.Memory
		r.Pods++
	}
	return r
}

/* ---------- transformations ---------- */

// transform allège les objets avant leur mise en cache : la vue n'a besoin ni
// des managedFields, ni des volumes (sauf les PVC montés), variables d'environnement ou templates.
// Les onglets YAML (jalon 5) relisent l'objet complet depuis l'API server.
func transform(obj any) (any, error) {
	m, ok := obj.(metav1.Object)
	if !ok {
		return obj, nil
	}
	m.SetManagedFields(nil)
	if a := m.GetAnnotations(); a != nil {
		delete(a, corev1.LastAppliedConfigAnnotation)
	}
	switch o := obj.(type) {
	case *unstructured.Unstructured:
		// GetAnnotations y renvoie une copie : on retire dans l'objet lui-même.
		unstructured.RemoveNestedField(o.Object, "metadata", "annotations", corev1.LastAppliedConfigAnnotation)
	case *corev1.Pod:
		o.Spec.Volumes = claimVolumes(o.Spec.Volumes) // PVC montés (jalon 8), rien d'autre
		slimContainers(o.Spec.InitContainers)
		slimContainers(o.Spec.Containers)
		o.Spec.EphemeralContainers = nil
	case *appsv1.Deployment:
		o.Spec.Template = corev1.PodTemplateSpec{}
	case *appsv1.ReplicaSet:
		o.Spec.Template = corev1.PodTemplateSpec{}
	case *appsv1.StatefulSet:
		o.Spec.Template = corev1.PodTemplateSpec{}
		o.Spec.VolumeClaimTemplates = nil
	case *appsv1.DaemonSet:
		o.Spec.Template = corev1.PodTemplateSpec{}
	case *batchv1.Job:
		o.Spec.Template = corev1.PodTemplateSpec{}
	}
	return obj, nil
}

// slimContainers garde nom, image, ressources et politique de redémarrage
// (nécessaire pour reconnaître les sidecars).
func slimContainers(cs []corev1.Container) {
	for i := range cs {
		c := &cs[i]
		cs[i] = corev1.Container{Name: c.Name, Image: c.Image, Resources: c.Resources, RestartPolicy: c.RestartPolicy}
	}
}

// ObjectEvents renvoie les événements d'un objet depuis le cache partagé.
func (s *Source) ObjectEvents(namespace, kind, name string) []model.Event {
	objs, _ := s.events.ByIndex(indexByInvolved, namespace+"/"+kind+"/"+name)
	out := make([]model.Event, 0, len(objs))
	for _, o := range objs {
		out = append(out, ConvertEvent(o.(*corev1.Event)))
	}
	return out
}

// ConvertEvent lit indifféremment les anciens champs (count, lastTimestamp) et
// les nouveaux (series, eventTime).
func ConvertEvent(e *corev1.Event) model.Event {
	m := model.Event{Type: e.Type, Reason: e.Reason, Message: e.Message, Count: e.Count,
		FirstSeen: e.FirstTimestamp.Time, LastSeen: e.LastTimestamp.Time, Source: e.Source.Component}
	if e.ReportingController != "" && m.Source == "" {
		m.Source = e.ReportingController
	}
	if e.Series != nil {
		m.Count = e.Series.Count
		m.LastSeen = e.Series.LastObservedTime.Time
	}
	if m.LastSeen.IsZero() {
		m.LastSeen = e.EventTime.Time
	}
	if m.LastSeen.IsZero() {
		m.LastSeen = e.CreationTimestamp.Time
	}
	if m.FirstSeen.IsZero() {
		m.FirstSeen = m.LastSeen
	}
	if m.Count == 0 {
		m.Count = 1
	}
	return m
}

// PodUID retrouve l'UID d'un pod dans le cache (pour les métriques).
func (s *Source) PodUID(namespace, name string) (string, bool) {
	p, err := s.pods.Pods(namespace).Get(name)
	if err != nil {
		return "", false
	}
	return string(p.UID), true
}
