package kube

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// netFixtures : fixtures() plus un Service api (pod-1 derrière), une Ingress
// vers api et vers un Service ghost absent, un PVC data monté par le pod db-0.
func netFixtures() []runtime.Object {
	pt := networkingv1.PathTypePrefix
	backend := func(svc string) networkingv1.IngressBackend {
		return networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: svc, Port: networkingv1.ServiceBackendPort{Number: 80}}}
	}
	return append(fixtures(),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
			Spec: corev1.ServiceSpec{ClusterIP: "10.96.0.10", Selector: map[string]string{"app": "api"}}},
		&discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: "api-x", Namespace: "prod", Labels: map[string]string{discoveryv1.LabelServiceName: "api"}},
			Endpoints: []discoveryv1.Endpoint{podEp("pod-1", bptr(true))}},
		&networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "prod"},
			Spec: networkingv1.IngressSpec{IngressClassName: sptr("nginx"), Rules: []networkingv1.IngressRule{{Host: "shop",
				IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
					{Path: "/", PathType: &pt, Backend: backend("api")},
					{Path: "/old", PathType: &pt, Backend: backend("ghost")},
				}}}}}}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "prod"}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db-0", Namespace: "prod", UID: "pod-db"},
			Spec: corev1.PodSpec{NodeName: "n1", Containers: []corev1.Container{{Name: "db"}}, Volumes: []corev1.Volume{
				{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"}}}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning}},
	)
}

func TestNetworkObjectsArePublished(t *testing.T) {
	client, sk := startSource(t, netFixtures()...)
	ctx := context.Background()

	svc, ok := sk.get(stream.KindService, "prod/api")
	if !ok || svc.(model.Service).Health != model.HealthOK || svc.(model.Service).Endpoints[0].PodUID != "pod-1" {
		t.Fatalf("service = %v %+v", ok, svc)
	}
	r, _ := sk.get(stream.KindRoute, "Ingress/prod/storefront")
	if rules := r.(model.Route).Rules; r.(model.Route).Gate != "nginx" || rules[0].Backend.State != model.BackendOK || rules[1].Backend.State != model.BackendMissing {
		t.Fatalf("route = %+v", r)
	}
	v, _ := sk.get(stream.KindVolume, "prod/data")
	if pods := v.(model.Volume).Pods; len(pods) != 1 || pods[0] != "pod-db" {
		t.Fatalf("volume = %+v", v)
	}

	// Un endpoint qui passe non ready met à jour le Service.
	sl, _ := client.DiscoveryV1().EndpointSlices("prod").Get(ctx, "api-x", metav1.GetOptions{})
	sl.Endpoints[0].Conditions.Ready = bptr(false)
	if _, err := client.DiscoveryV1().EndpointSlices("prod").Update(ctx, sl, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "service down", func() bool {
		o, _ := sk.get(stream.KindService, "prod/api")
		return o.(model.Service).Health == model.HealthDown
	})

	// Créer le Service manquant répare la route.
	ghost := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "ghost", Namespace: "prod"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "ghost"}}}
	if _, err := client.CoreV1().Services("prod").Create(ctx, ghost, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "route réparée", func() bool {
		o, _ := sk.get(stream.KindRoute, "Ingress/prod/storefront")
		return o.(model.Route).Rules[1].Backend.State == model.BackendOK
	})

	// Le pod qui montait le PVC disparaît.
	if err := client.CoreV1().Pods("prod").Delete(ctx, "db-0", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "volume sans pod", func() bool {
		o, _ := sk.get(stream.KindVolume, "prod/data")
		return len(o.(model.Volume).Pods) == 0
	})
}

func TestForbiddenTypeIsDisabled(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	client.PrependReactor("list", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", errors.New("refusé"))
	})
	_, sk := startSourceWith(t, client, Options{})
	if _, ok := sk.get(stream.KindService, "prod/api"); ok {
		t.Error("Service publié malgré le refus")
	}
	r, ok := sk.get(stream.KindRoute, "Ingress/prod/storefront")
	if !ok || r.(model.Route).Rules[1].Backend.State != model.BackendOK {
		t.Errorf("sans Services, un backend est réputé présent : %+v", r)
	}
	if _, ok := sk.get(stream.KindVolume, "prod/data"); !ok {
		t.Error("les PVC restent publiés")
	}
}

// Sans EndpointSlices, chaque Service paraîtrait down : les Services sont désactivés aussi.
func TestForbiddenEndpointSlicesDisableServices(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	client.PrependReactor("list", "endpointslices", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "discovery.k8s.io", Resource: "endpointslices"}, "", errors.New("refusé"))
	})
	_, sk := startSourceWith(t, client, Options{})
	if _, ok := sk.get(stream.KindService, "prod/api"); ok {
		t.Error("Service publié sans EndpointSlices")
	}
	r, ok := sk.get(stream.KindRoute, "Ingress/prod/storefront")
	if !ok || r.(model.Route).Rules[1].Backend.State != model.BackendOK {
		t.Errorf("sans Services, un backend est réputé présent : %+v", r)
	}
	if _, ok := sk.get(stream.KindVolume, "prod/data"); !ok {
		t.Error("les PVC restent publiés")
	}
}

func TestServiceDeletedTurnsBackendMissing(t *testing.T) {
	client, sk := startSource(t, netFixtures()...)
	if err := client.CoreV1().Services("prod").Delete(context.Background(), "api", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backend manquant", func() bool {
		o, _ := sk.get(stream.KindRoute, "Ingress/prod/storefront")
		return o.(model.Route).Rules[0].Backend.State == model.BackendMissing
	})
}

func TestSucceededPodLeavesVolume(t *testing.T) {
	client, sk := startSource(t, netFixtures()...)
	ctx := context.Background()
	p, _ := client.CoreV1().Pods("prod").Get(ctx, "db-0", metav1.GetOptions{})
	p.Status.Phase = corev1.PodSucceeded
	if _, err := client.CoreV1().Pods("prod").UpdateStatus(ctx, p, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "pod terminé hors du volume", func() bool {
		o, _ := sk.get(stream.KindVolume, "prod/data")
		return len(o.(model.Volume).Pods) == 0
	})
}

func TestDefaultIngressClassRegatesIngress(t *testing.T) {
	bare := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "bare", Namespace: "prod"}}
	client, sk := startSource(t, append(netFixtures(), bare)...)
	id := "Ingress/prod/bare"
	if o, ok := sk.get(stream.KindRoute, id); !ok || o.(model.Route).Gate != "default" {
		t.Fatalf("route = %v %+v", ok, o)
	}
	cls := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "traefik",
		Annotations: map[string]string{defaultClassAnnotation: "true"}}}
	if _, err := client.NetworkingV1().IngressClasses().Create(context.Background(), cls, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "porte par défaut", func() bool {
		o, _ := sk.get(stream.KindRoute, id)
		return o.(model.Route).Gate == "traefik"
	})
}
