package kube

import (
	"context"
	"io"
	"log/slog"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

type metricsSink struct{ got []model.Metrics }

func (m *metricsSink) SetMetrics(x model.Metrics) { m.got = append(m.got, x) }

func TestPollMetrics(t *testing.T) {
	client := metricsfake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.PodMetricsList{Items: []metricsv1beta1.PodMetrics{
			{ObjectMeta: metav1.ObjectMeta{Name: "api-x", Namespace: "prod"}, Containers: []metricsv1beta1.ContainerMetrics{
				{Name: "a", Usage: rl("120m", "100Mi")}, {Name: "b", Usage: rl("30m", "28Mi")}}},
			{ObjectMeta: metav1.ObjectMeta{Name: "gone", Namespace: "prod"}, Containers: []metricsv1beta1.ContainerMetrics{{Name: "a", Usage: rl("1", "1Mi")}}},
		}}, nil
	})
	client.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.NodeMetricsList{Items: []metricsv1beta1.NodeMetrics{
			{ObjectMeta: metav1.ObjectMeta{Name: "n1"}, Usage: rl("1500m", "3Gi")}}}, nil
	})
	sk := &metricsSink{}
	p := NewMetricsPoller(client.MetricsV1beta1(), sk, func(ns, name string) (string, bool) {
		if ns == "prod" && name == "api-x" {
			return "uid-1", true
		}
		return "", false
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	p.Poll(context.Background())
	if len(sk.got) != 1 {
		t.Fatalf("attendu 1 publication, reçu %d", len(sk.got))
	}
	m := sk.got[0]
	if m.Pods["uid-1"] != (model.Usage{CPU: 150, Memory: 128 << 20}) || len(m.Pods) != 1 {
		t.Errorf("pods = %+v", m.Pods)
	}
	if m.Nodes["n1"] != (model.Usage{CPU: 1500, Memory: 3 << 30}) {
		t.Errorf("nodes = %+v", m.Nodes)
	}
}

func TestPollMetricsWithoutMetricsServer(t *testing.T) {
	client := metricsfake.NewSimpleClientset()
	client.PrependReactor("list", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "metrics.k8s.io", Resource: "pods"}, "")
	})
	sk := &metricsSink{}
	p := NewMetricsPoller(client.MetricsV1beta1(), sk, func(string, string) (string, bool) { return "", false },
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.Poll(context.Background())
	p.Poll(context.Background())
	if len(sk.got) != 0 {
		t.Errorf("rien ne doit être publié sans metrics-server : %+v", sk.got)
	}
}
