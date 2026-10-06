package kube

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/no-inspi/cluster-atlas/internal/access"
	"github.com/no-inspi/cluster-atlas/internal/inspect"
	"github.com/no-inspi/cluster-atlas/internal/model"
)

// EventSource fournit les événements d'un pod depuis le cache partagé.
type EventSource interface {
	PodEvents(namespace, name string) []model.Event
}

// Authorize répond « l'utilisateur a-t-il ce droit ? » (Reviewer.Allowed).
// nil : toujours oui (mode sans authentification).
type Authorize func(ctx context.Context, u access.User, a access.Attributes) bool

// Inspector répond à l'inspecteur au nom de l'utilisateur : propriétaires, YAML
// et logs passent par ses clients impersonnés ; les événements viennent du
// cache partagé après vérification de son droit « list events ».
type Inspector struct {
	clients   access.ClientSource
	events    EventSource
	authorize Authorize
}

func NewInspector(clients access.ClientSource, events EventSource, authorize Authorize) *Inspector {
	return &Inspector{clients: clients, events: events, authorize: authorize}
}

var _ inspect.Backend = (*Inspector)(nil)

func (in *Inspector) Owners(ctx context.Context, u access.User, ns, pod string) ([]inspect.Ref, error) {
	kc, err := in.clients.Kube(u)
	if err != nil {
		return nil, err
	}
	p, err := kc.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	chain := []inspect.Ref{inspect.RefFor("Pod", ns, pod)}
	ctl := metav1.GetControllerOf(p)
	if ctl == nil {
		return chain, nil
	}
	chain = append([]inspect.Ref{inspect.RefFor(ctl.Kind, ns, ctl.Name)}, chain...)
	if ctl.Kind == "ReplicaSet" {
		rs, err := kc.AppsV1().ReplicaSets(ns).Get(ctx, ctl.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if d := metav1.GetControllerOf(rs); d != nil && d.Kind == "Deployment" {
			chain = append([]inspect.Ref{inspect.RefFor("Deployment", ns, d.Name)}, chain...)
		}
	}
	return chain, nil
}

func (in *Inspector) YAML(ctx context.Context, u access.User, ref inspect.Ref) (inspect.Doc, error) {
	k, err := inspect.Lookup(ref.Group, ref.Version, ref.Kind)
	if err != nil {
		return inspect.Doc{}, err
	}
	dc, err := in.clients.Dynamic(u)
	if err != nil {
		return inspect.Doc{}, err
	}
	gvr := schema.GroupVersionResource{Group: k.Group, Version: k.Version, Resource: k.Resource}
	obj, err := dc.Resource(gvr).Namespace(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return inspect.Doc{}, err
	}
	return document(ref, obj)
}

// document nettoie l'objet (sans managedFields) et le sérialise en YAML.
func document(ref inspect.Ref, obj *unstructured.Unstructured) (inspect.Doc, error) {
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	b, err := yaml.Marshal(obj.Object)
	if err != nil {
		return inspect.Doc{}, err
	}
	meta := metav1.ObjectMeta{Annotations: obj.GetAnnotations(), Labels: obj.GetLabels()}
	return inspect.Doc{Ref: ref, YAML: string(b), Argo: argoInfo(meta)}, nil
}

func (in *Inspector) Logs(ctx context.Context, u access.User, ns, pod string, o inspect.LogOptions) (io.ReadCloser, error) {
	kc, err := in.clients.Kube(u)
	if err != nil {
		return nil, err
	}
	opts := &corev1.PodLogOptions{Container: o.Container, Previous: o.Previous, Follow: o.Follow, Timestamps: true}
	if o.TailLines > 0 {
		opts.TailLines = &o.TailLines
	}
	return kc.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
}

func (in *Inspector) Events(ctx context.Context, u access.User, ns, pod string) ([]model.Event, error) {
	if in.authorize != nil && !in.authorize(ctx, u, access.Attributes{Verb: "list", Resource: "events", Namespace: ns}) {
		return nil, apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "",
			fmt.Errorf("User %q cannot list resource \"events\" in API group \"\" in the namespace %q", u.Name, ns))
	}
	evs := in.events.PodEvents(ns, pod)
	inspect.SortEvents(evs)
	return evs, nil
}
