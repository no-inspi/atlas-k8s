package kube

import (
	"context"
	"log/slog"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// MetricsSink reçoit les instantanés ; *stream.Hub l'implémente.
type MetricsSink interface{ SetMetrics(model.Metrics) }

// PodUIDFunc retrouve l'UID d'un pod (les PodMetrics ne portent que ns/nom).
type PodUIDFunc func(namespace, name string) (string, bool)

const MetricsInterval = 15 * time.Second

// MetricsPoller relève metrics-server toutes les 15 s. S'il est absent, rien
// n'est publié et le front affiche « indisponible ».
type MetricsPoller struct {
	client  metricsclient.MetricsV1beta1Interface
	sink    MetricsSink
	uid     PodUIDFunc
	log     *slog.Logger
	failing bool
}

func NewMetricsPoller(c metricsclient.MetricsV1beta1Interface, sink MetricsSink, uid PodUIDFunc, log *slog.Logger) *MetricsPoller {
	return &MetricsPoller{client: c, sink: sink, uid: uid, log: log}
}

func (p *MetricsPoller) Run(ctx context.Context) {
	p.Poll(ctx)
	t := time.NewTicker(MetricsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Poll(ctx)
		}
	}
}

func (p *MetricsPoller) Poll(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pods, err := p.client.PodMetricses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		p.fail(err)
		return
	}
	nodes, err := p.client.NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		p.fail(err)
		return
	}
	if p.failing {
		p.log.Info("metrics-server de nouveau disponible")
		p.failing = false
	}

	m := model.Metrics{Pods: map[string]model.Usage{}, Nodes: map[string]model.Usage{}}
	for _, pm := range pods.Items {
		uid, ok := p.uid(pm.Namespace, pm.Name)
		if !ok {
			continue
		}
		var u model.Usage
		for _, c := range pm.Containers {
			u.CPU += c.Usage.Cpu().MilliValue()
			u.Memory += c.Usage.Memory().Value()
		}
		m.Pods[uid] = u
	}
	for _, nm := range nodes.Items {
		m.Nodes[nm.Name] = model.Usage{CPU: nm.Usage.Cpu().MilliValue(), Memory: nm.Usage.Memory().Value()}
	}
	p.sink.SetMetrics(m)
}

// fail journalise une seule fois la perte de metrics-server.
func (p *MetricsPoller) fail(err error) {
	if !p.failing {
		p.log.Warn("metrics-server indisponible : usage CPU/mémoire non affiché", "err", err)
		p.failing = true
	}
}
