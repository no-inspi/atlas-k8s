package kube

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Suivi des CRD (jalon 9) : un type du registre démarre quand sa CRD est
// installée et sert la version attendue, et s'arrête quand elle disparaît ou
// ne la sert plus. Un démarrage en échec passager est réessayé (wantDyn).
// Sans droit de lister les CRD, repli sur la découverte au démarrage.

var gvrCRD = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// startDynamic démarre le suivi des CRD et attend, au plus opts.dynWait en
// tout, que les types déjà servis aient fini leur premier essai de démarrage
// (en parallèle). Un type encore en cours ensuite démarre en arrière-plan, et
// ses objets suivent le premier snapshot. Rend la main dès que ctx est annulé.
func (s *Source) startDynamic(ctx context.Context) {
	dyn := s.opts.Dynamic
	if dyn == nil {
		return
	}
	wait, cancel := context.WithTimeout(ctx, s.opts.dynWait)
	defer cancel()
	if !s.probe(wait, "customresourcedefinitions", func(ctx context.Context) error {
		_, err := dyn.Resource(gvrCRD).List(ctx, probeOpts)
		return err
	}) {
		s.opts.Log.Warn("CRD non suivies : types dynamiques découverts au démarrage seulement")
		for _, k := range servedKinds(s.client.Discovery(), s.opts.Log) {
			s.wantDyn(ctx, k)
		}
	} else {
		inf := dynamicinformer.NewFilteredDynamicInformer(dyn, gvrCRD, metav1.NamespaceAll, 0, cache.Indexers{}, nil).Informer()
		_ = inf.SetTransform(slimCRD)
		reg, _ := inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    func(o any) { s.syncCRD(ctx, o) },
			UpdateFunc: func(_, cur any) { s.syncCRD(ctx, cur) },
			DeleteFunc: func(o any) {
				if d, ok := o.(cache.DeletedFinalStateUnknown); ok {
					o = d.Obj
				}
				s.dropCRD(o)
			},
		})
		go inf.Run(ctx.Done())
		// reg.HasSynced : le handler a vu les CRD déjà installées, les types servis sont déclarés.
		if !cache.WaitForCacheSync(wait.Done(), reg.HasSynced) {
			if ctx.Err() == nil {
				s.opts.Log.Warn("CRD non synchronisées à temps : types dynamiques démarrés plus tard")
			}
			return
		}
	}
	for _, first := range s.wantedFirsts() {
		select {
		case <-first:
		case <-wait.Done():
			if ctx.Err() == nil {
				s.opts.Log.Warn("types dynamiques encore en démarrage : ils suivront le premier snapshot")
			}
			return
		}
	}
}

// syncCRD déclare servis ou non les types qu'apporte une CRD selon qu'elle
// sert leur version. Ne bloque pas : le handler de l'informer des CRD reste libre.
func (s *Source) syncCRD(ctx context.Context, o any) {
	u, ok := o.(*unstructured.Unstructured)
	if !ok {
		return
	}
	for _, k := range dynKinds {
		if k.crd() != u.GetName() {
			continue
		}
		if crdServes(u, k.gvr.Version) {
			s.wantDyn(ctx, k)
		} else {
			s.unwantDyn(k.gvr)
		}
	}
}

func (s *Source) dropCRD(o any) {
	u, ok := o.(*unstructured.Unstructured)
	if !ok {
		return
	}
	for _, k := range dynKinds {
		if k.crd() == u.GetName() {
			s.unwantDyn(k.gvr)
		}
	}
}

// crdServes : la CRD sert-elle cette version ?
func crdServes(u *unstructured.Unstructured, version string) bool {
	vs, _, _ := unstructured.NestedSlice(u.Object, "spec", "versions")
	for _, v := range vs {
		if m, ok := v.(map[string]any); ok && m["name"] == version && m["served"] == true {
			return true
		}
	}
	return false
}

// slimCRD : une CRD pèse surtout par son schéma ; on ne garde que le nom et,
// par version, son nom et served.
func slimCRD(obj any) (any, error) {
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		return obj, nil
	}
	vs, _, _ := unstructured.NestedSlice(u.Object, "spec", "versions")
	slim := make([]any, 0, len(vs))
	for _, v := range vs {
		if m, ok := v.(map[string]any); ok {
			slim = append(slim, map[string]any{"name": m["name"], "served": m["served"]})
		}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": u.GetAPIVersion(), "kind": u.GetKind(),
		"metadata": map[string]any{"name": u.GetName(), "uid": string(u.GetUID()), "resourceVersion": u.GetResourceVersion()},
		"spec":     map[string]any{"versions": slim},
	}}, nil
}
