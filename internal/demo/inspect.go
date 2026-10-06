package demo

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/no-inspi/atlas-k8s/internal/access"
	"github.com/no-inspi/atlas-k8s/internal/inspect"
	"github.com/no-inspi/atlas-k8s/internal/model"
)

// Le simulateur répond aussi à l'inspecteur : événements et logs générés au
// fil des transitions (comme dans le prototype), YAML reconstruit depuis le
// catalogue. L'utilisateur est ignoré : en démo, tout est visible.

const (
	maxLogLines   = 500
	maxPodEvents  = 50
	logLineChance = 0.11 // par pas de 200 ms : environ une ligne toutes les 2 s
)

type logLine struct {
	at   time.Time
	text string
}

/* ---------- événements ---------- */

// event ajoute un événement, ou incrémente le précédent s'il est identique
// (comme le fait l'event recorder de Kubernetes).
func (s *Sim) event(p *simPod, typ, reason, msg, source string) {
	if n := len(p.events); n > 0 {
		last := &p.events[n-1]
		if last.Reason == reason && last.Message == msg {
			last.Count++
			last.LastSeen = s.now
			return
		}
	}
	p.events = append(p.events, model.Event{Type: typ, Reason: reason, Message: msg, Count: 1, FirstSeen: s.now, LastSeen: s.now, Source: source})
	if len(p.events) > maxPodEvents {
		p.events = p.events[1:]
	}
}

// onTransition produit événements et logs d'un changement de statut.
func (s *Sim) onTransition(p *simPod, prev, next string) {
	d := p.wl.def
	switch next {
	case "ContainerCreating":
		s.event(p, "Normal", "Pulling", fmt.Sprintf("Pulling image %q", d.Image), "kubelet")
	case "Running":
		if prev == "Running" {
			return
		}
		s.event(p, "Normal", "Pulled", fmt.Sprintf("Container image %q already present on machine", d.Image), "kubelet")
		s.event(p, "Normal", "Created", "Created container "+d.Name, "kubelet")
		s.event(p, "Normal", "Started", "Started container "+d.Name, "kubelet")
		p.logs = nil
		s.seedLogs(p)
		if p.notReady {
			s.event(p, "Warning", "Unhealthy", "Readiness probe failed: HTTP probe failed with statuscode: 503", "kubelet")
		}
	case "ImagePullBackOff":
		s.event(p, "Warning", "Failed", fmt.Sprintf("Failed to pull image %q: rpc error: code = NotFound desc = failed to pull and unpack image %q: not found", d.Image, d.Image), "kubelet")
		s.event(p, "Warning", "Failed", "Error: ErrImagePull", "kubelet")
		s.event(p, "Normal", "BackOff", fmt.Sprintf("Back-off pulling image %q", d.Image), "kubelet")
	case "Error":
		s.crashLines(p)
		p.prevLogs = append([]logLine(nil), p.logs...)
	case "CrashLoopBackOff":
		s.event(p, "Warning", "BackOff", fmt.Sprintf("Back-off restarting failed container %s in pod %s_%s", d.Name, p.pod.Name, d.NS), "kubelet")
	case "Terminating":
		s.event(p, "Normal", "Killing", "Stopping container "+d.Name, "kubelet")
	}
}

// backdate replace les événements d'un pod démarré « en régime » à sa date de
// création, sauf ceux qui se répètent encore (BackOff, Unhealthy).
func (s *Sim) backdate(p *simPod) {
	for i := range p.events {
		e := &p.events[i]
		e.FirstSeen = p.pod.CreatedAt.Add(time.Duration(i) * time.Second)
		if e.Reason != "BackOff" && e.Reason != "Unhealthy" {
			e.LastSeen = e.FirstSeen
		} else {
			e.Count = max(1, p.pod.Restarts)
		}
	}
	spread := func(lines []logLine, end time.Time) {
		for i := range lines {
			lines[i].at = end.Add(-time.Duration(len(lines)-i) * 3 * time.Second)
		}
	}
	spread(p.logs, s.now)
	// L'instance précédente s'est arrêtée avant le dernier redémarrage.
	spread(p.prevLogs, s.now.Add(-secs(backoffSec)))
}

/* ---------- logs ---------- */

var paths = []string{"/api/v1/orders", "/api/v1/orders/{id}", "/api/v1/customers/{id}", "/api/v1/quotes", "/healthz", "/api/v1/invoices/{id}/pdf"}

func (s *Sim) pick(xs ...string) string { return xs[s.rng.IntN(len(xs))] }

// logText reprend les générateurs du prototype, selon le type d'application.
func (s *Sim) logText(d workloadDef) string {
	id := 1000 + s.rng.IntN(99000)
	switch {
	case d.Name == "api-gateway" || d.Name == "orders-service" || d.Name == "checkout-preview":
		r := s.rng.Float64()
		code, lv := 200, "INFO "
		if r > 0.96 {
			code, lv = 503, "ERROR"
		} else if r > 0.9 {
			code, lv = 404, "WARN "
		}
		return fmt.Sprintf("%s %s %s %d %dms trace=%s", lv, s.pick("GET", "GET", "GET", "POST", "PUT"),
			strings.ReplaceAll(s.pick(paths...), "{id}", fmt.Sprint(id)), code, 3+s.rng.IntN(120), s.hex(8))
	case d.Name == "payment-worker":
		return "INFO  " + s.pick(fmt.Sprintf("processing payment batch #%d (%d items)", id, 4+s.rng.IntN(36)),
			fmt.Sprintf("ack message from queue payments.pending offset=%d", id), fmt.Sprintf("settled %d transactions", 1+s.rng.IntN(19)))
	case d.Name == "frontend" || d.Name == "grafana":
		return fmt.Sprintf("INFO  10.52.%d.%d \"GET %s HTTP/1.1\" 200 %d", s.rng.IntN(6), 2+s.rng.IntN(248),
			s.pick("/", "/assets/app.js", "/assets/app.css", "/favicon.svg", "/dashboard"), 200+s.rng.IntN(48000))
	case d.Name == "ml-inference":
		return fmt.Sprintf("INFO  batch=%s latency_ms=%d gpu_util=%d%% queue=%d", s.pick("8", "16", "32"), 18+s.rng.IntN(57), 40+s.rng.IntN(52), s.rng.IntN(6))
	case d.Name == "prometheus":
		return "INFO  " + s.pick(fmt.Sprintf("caller=compact.go msg=\"write block\" duration=%.2fs", s.between(0.2, 2)),
			fmt.Sprintf("caller=head.go msg=\"Head GC completed\" duration=%dms", 20+s.rng.IntN(160)),
			"caller=scrape.go msg=\"scrape ok\" target="+s.pick("api-gateway", "orders-service", "node-exporter"))
	case d.Name == "coredns":
		return fmt.Sprintf("INFO  10.52.%d.%d:%d - \"A IN %s.svc.cluster.local. udp\" NOERROR qr,aa,rd %db %.3fms", s.rng.IntN(6), 2+s.rng.IntN(248),
			30000+s.rng.IntN(30000), s.pick("orders-service.production", "api-gateway.production", "prometheus.monitoring"), 80+s.rng.IntN(60), s.between(0.05, 0.9))
	case strings.HasPrefix(d.Name, "argocd"):
		return "INFO  " + s.pick(fmt.Sprintf("Reconciliation completed application=%s dur=%dms", s.pick("production-apps", "staging-apps", "monitoring-apps"), 80+s.rng.IntN(520)),
			"git fetch origin --tags --force --prune")
	case d.Name == "postgres-payments":
		return "INFO  " + s.pick("checkpoint starting: time", fmt.Sprintf("checkpoint complete: wrote %d buffers", 10+s.rng.IntN(400)), "automatic vacuum of table \"payments.public.transactions\"")
	case d.Name == "db-backup":
		return fmt.Sprintf("INFO  dumped table %s (%d rows)", s.pick("transactions", "customers", "invoices"), 1000+s.rng.IntN(90000))
	}
	return fmt.Sprintf("INFO  scraped %d targets in %dms", 20+s.rng.IntN(20), 30+s.rng.IntN(60))
}

func (s *Sim) appendLog(p *simPod, text string) {
	p.logs = append(p.logs, logLine{at: s.now, text: text})
	p.logSeq++
	if len(p.logs) > maxLogLines {
		p.logs = p.logs[len(p.logs)-maxLogLines:]
	}
}

func (s *Sim) seedLogs(p *simPod) {
	d := p.wl.def
	s.appendLog(p, fmt.Sprintf("INFO  Starting %s (%s)", d.Name, d.Image))
	s.appendLog(p, "INFO  Listening on :8080")
	for range 20 {
		s.appendLog(p, s.logText(d))
	}
}

// crashLines : les dernières lignes d'un container qui crashe, écrites au
// moment du crash (y compris pour un replica qui vient d'hériter du rôle).
func (s *Sim) crashLines(p *simPod) {
	s.appendLog(p, "ERROR dial tcp 10.60.3.12:5432: connect: connection refused (postgres-payments)")
	s.appendLog(p, "ERROR failed to init repository: retries exhausted (5/5)")
	s.appendLog(p, "FATAL panic: cannot start without database connection")
}

// tickLogs fait parler les pods Running.
func (s *Sim) tickLogs() {
	for _, p := range s.pods {
		if p.pod.DisplayStatus == "Running" && s.rng.Float64() < logLineChance {
			s.appendLog(p, s.logText(p.wl.def))
		}
	}
}

/* ---------- inspect.Backend ---------- */

var _ inspect.Backend = (*Sim)(nil)

func (s *Sim) findPod(ns, name string) *simPod {
	for _, p := range s.pods {
		if p.pod.Namespace == ns && p.pod.Name == name {
			return p
		}
	}
	return nil
}

func notFound(resource, name string) error {
	return apierrors.NewNotFound(schema.GroupResource{Resource: resource}, name)
}

func (s *Sim) Owners(_ context.Context, _ access.User, ns, name string) ([]inspect.Ref, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findPod(ns, name)
	if p == nil {
		return nil, notFound("pods", name)
	}
	d := p.wl.def
	chain := []inspect.Ref{inspect.RefFor(d.Kind, ns, d.Name)}
	if d.Kind == "Deployment" {
		chain = append(chain, inspect.RefFor("ReplicaSet", ns, d.Name+"-"+p.wl.hash))
	}
	return append(chain, inspect.RefFor("Pod", ns, name)), nil
}

func (s *Sim) Events(_ context.Context, _ access.User, ns, name string) ([]model.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findPod(ns, name)
	if p == nil {
		return []model.Event{}, nil
	}
	evs := append([]model.Event(nil), p.events...)
	inspect.SortEvents(evs)
	return evs, nil
}

func (s *Sim) Logs(ctx context.Context, _ access.User, ns, name string, o inspect.LogOptions) (io.ReadCloser, error) {
	s.mu.Lock()
	p := s.findPod(ns, name)
	if p == nil {
		s.mu.Unlock()
		return nil, notFound("pods", name)
	}
	d := p.wl.def
	if o.Container != "" && o.Container != d.Name {
		s.mu.Unlock()
		return nil, apierrors.NewBadRequest(fmt.Sprintf("container %s is not valid for pod %s", o.Container, name))
	}
	src := p.logs
	if o.Previous {
		if len(p.prevLogs) == 0 {
			s.mu.Unlock()
			return nil, apierrors.NewBadRequest(fmt.Sprintf("previous terminated container %q in pod %q not found", d.Name, name))
		}
		src = p.prevLogs
	}
	if o.TailLines > 0 && int(o.TailLines) < len(src) {
		src = src[len(src)-int(o.TailLines):]
	}
	initial := append([]logLine(nil), src...)
	next, uid := p.logSeq, p.pod.UID
	s.mu.Unlock()

	pr, pw := io.Pipe()
	go func() {
		if err := writeLogLines(pw, initial); err != nil || !o.Follow || o.Previous {
			_ = pw.Close()
			return
		}
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = pw.CloseWithError(ctx.Err())
				return
			case <-t.C:
				lines, ok := s.linesSince(ns, name, uid, &next)
				if !ok {
					_ = pw.Close() // pod disparu : fin du flux, comme kubectl logs -f
					return
				}
				if err := writeLogLines(pw, lines); err != nil {
					return
				}
			}
		}
	}()
	return pr, nil
}

// linesSince renvoie les lignes ajoutées depuis next et avance next.
func (s *Sim) linesSince(ns, name, uid string, next *int) ([]logLine, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.findPod(ns, name)
	if p == nil || p.pod.UID != uid {
		return nil, false
	}
	n := min(p.logSeq-*next, len(p.logs))
	*next = p.logSeq
	return append([]logLine(nil), p.logs[len(p.logs)-n:]...), true
}

func writeLogLines(w io.Writer, lines []logLine) error {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.at.UTC().Format(inspect.LogTimeLayout))
		b.WriteByte(' ')
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

/* ---------- YAML ---------- */

func (s *Sim) YAML(_ context.Context, _ access.User, ref inspect.Ref) (inspect.Doc, error) {
	if _, err := inspect.Lookup(ref.Group, ref.Version, ref.Kind); err != nil {
		return inspect.Doc{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var obj map[string]any
	var argo bool
	switch ref.Kind {
	case "Pod":
		p := s.findPod(ref.Namespace, ref.Name)
		if p == nil {
			return inspect.Doc{}, notFound("pods", ref.Name)
		}
		obj, argo = s.podObject(p), p.wl.def.Argo
	case "ReplicaSet":
		for _, w := range s.workloads {
			if w.def.Kind == "Deployment" && w.def.NS == ref.Namespace && w.def.Name+"-"+w.hash == ref.Name {
				obj, argo = s.replicaSetObject(w), w.def.Argo
			}
		}
	default:
		for _, w := range s.workloads {
			if w.def.Kind == ref.Kind && w.def.NS == ref.Namespace && w.def.Name == ref.Name {
				obj, argo = s.workloadObject(w), w.def.Argo
			}
		}
	}
	if obj == nil {
		return inspect.Doc{}, notFound(strings.ToLower(ref.Kind)+"s", ref.Name)
	}
	b, err := yaml.Marshal(obj)
	if err != nil {
		return inspect.Doc{}, err
	}
	doc := inspect.Doc{Ref: ref, YAML: string(b)}
	if argo {
		doc.Argo = &model.ArgoInfo{Application: ref.Namespace + "-apps", SyncStatus: "Synced"}
	}
	return doc, nil
}

func quantity(cpuMilli, memBytes int64) map[string]any {
	q := map[string]any{}
	if cpuMilli > 0 {
		q["cpu"] = fmt.Sprintf("%dm", cpuMilli)
	}
	if memBytes > 0 {
		q["memory"] = fmt.Sprintf("%dMi", memBytes/mi)
	}
	return q
}

func (s *Sim) meta(name, ns string, d workloadDef, extra map[string]any) map[string]any {
	m := map[string]any{"name": name, "namespace": ns, "labels": map[string]any{"app.kubernetes.io/name": d.Name}}
	if d.Argo {
		m["labels"].(map[string]any)["app.kubernetes.io/managed-by"] = "argocd"
		m["annotations"] = map[string]any{"argocd.argoproj.io/tracking-id": fmt.Sprintf("%s-apps:apps/%s:%s/%s", ns, d.Kind, ns, d.Name)}
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func container(d workloadDef) map[string]any {
	c := map[string]any{
		"name": d.Name, "image": d.Image,
		"resources": map[string]any{"requests": quantity(d.CPU, d.Mem), "limits": quantity(0, d.Mem*3/2)},
	}
	if d.Name != "coredns" && d.Kind != "Job" {
		c["ports"] = []any{map[string]any{"name": "http", "containerPort": 8080}}
		c["readinessProbe"] = map[string]any{"httpGet": map[string]any{"path": "/healthz", "port": "http"}, "periodSeconds": 10}
	}
	return c
}

func podSpec(d workloadDef) map[string]any {
	spec := map[string]any{"serviceAccountName": d.Name, "containers": []any{container(d)}}
	if d.GPU {
		spec["nodeSelector"] = map[string]any{"cloud.google.com/gke-accelerator": "nvidia-l4"}
		spec["tolerations"] = []any{map[string]any{"key": "nvidia.com/gpu", "operator": "Exists", "effect": "NoSchedule"}}
	}
	if d.ToleraAll {
		spec["tolerations"] = []any{map[string]any{"operator": "Exists"}}
	}
	if d.Kind == "Job" {
		spec["restartPolicy"] = "Never"
	}
	return spec
}

func (s *Sim) workloadObject(w *simWorkload) map[string]any {
	d := w.def
	m := s.workloadModel(w)
	sel := map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": d.Name}}
	tpl := map[string]any{"metadata": map[string]any{"labels": map[string]any{"app.kubernetes.io/name": d.Name}}, "spec": podSpec(d)}
	group := "apps/v1"
	spec := map[string]any{"selector": sel, "template": tpl}
	status := map[string]any{"readyReplicas": m.ReadyReplicas}
	switch d.Kind {
	case "Deployment":
		spec["replicas"] = w.replicas
		spec["strategy"] = map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": "25%", "maxUnavailable": 0}}
		status["replicas"], status["availableReplicas"], status["updatedReplicas"] = m.Replicas, m.ReadyReplicas, m.Replicas
	case "StatefulSet":
		spec["replicas"], spec["serviceName"] = w.replicas, d.Name
		status["replicas"] = m.Replicas
	case "DaemonSet":
		status = map[string]any{"desiredNumberScheduled": m.Replicas, "numberReady": m.ReadyReplicas}
	case "Job":
		group = "batch/v1"
		spec = map[string]any{"backoffLimit": 2, "ttlSecondsAfterFinished": 30, "template": tpl}
		status = map[string]any{"ready": m.ReadyReplicas}
	}
	return map[string]any{"apiVersion": group, "kind": d.Kind, "metadata": s.meta(d.Name, d.NS, d, nil), "spec": spec, "status": status}
}

func (s *Sim) replicaSetObject(w *simWorkload) map[string]any {
	d := w.def
	m := s.workloadModel(w)
	owner := []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": d.Name, "controller": true, "blockOwnerDeletion": true}}
	return map[string]any{
		"apiVersion": "apps/v1", "kind": "ReplicaSet",
		"metadata": s.meta(d.Name+"-"+w.hash, d.NS, d, map[string]any{"ownerReferences": owner}),
		"spec":     map[string]any{"replicas": w.replicas, "template": map[string]any{"spec": podSpec(d)}},
		"status":   map[string]any{"replicas": m.Replicas, "readyReplicas": m.ReadyReplicas},
	}
}

func (s *Sim) podObject(p *simPod) map[string]any {
	d := p.wl.def
	ownerKind, ownerName := d.Kind, d.Name
	if d.Kind == "Deployment" {
		ownerKind, ownerName = "ReplicaSet", d.Name+"-"+p.wl.hash
	}
	owner := []any{map[string]any{"apiVersion": "apps/v1", "kind": ownerKind, "name": ownerName, "controller": true}}
	var statuses []any
	for _, c := range p.pod.Containers {
		cs := map[string]any{"name": c.Name, "image": c.Image, "ready": c.Ready, "restartCount": c.Restarts}
		state := map[string]any{}
		if c.Reason != "" {
			state["reason"] = c.Reason
		}
		cs["state"] = map[string]any{c.State: state}
		statuses = append(statuses, cs)
	}
	spec := podSpec(d)
	if p.pod.NodeName != "" {
		spec["nodeName"] = p.pod.NodeName
	}
	return map[string]any{
		"apiVersion": "v1", "kind": "Pod",
		"metadata": s.meta(p.pod.Name, p.pod.Namespace, d, map[string]any{
			"uid": p.pod.UID, "creationTimestamp": p.pod.CreatedAt.UTC().Format(time.RFC3339), "ownerReferences": owner}),
		"spec":   spec,
		"status": map[string]any{"phase": p.pod.Phase, "podIP": p.pod.PodIP, "qosClass": p.pod.QOSClass, "containerStatuses": statuses},
	}
}
