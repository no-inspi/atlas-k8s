// Package demo simule un cluster GKE pour `atlas --demo` : il produit le même
// modèle réduit que les informers et le pousse dans le hub de stream, si bien
// que le front suit exactement le même chemin de données qu'en production.
package demo

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/no-inspi/cluster-atlas/internal/model"
	"github.com/no-inspi/cluster-atlas/internal/stream"
)

// Sink reçoit l'état simulé ; *stream.Hub l'implémente.
type Sink interface {
	Upsert(kind stream.Kind, key string, obj any)
	Delete(kind stream.Kind, key string, obj any)
	SetMetrics(m model.Metrics)
}

type Options struct {
	Seed uint64
	// Now est l'heure de départ de la simulation (time.Now() si zéro).
	Now time.Time
}

const gpuTaintKey = "nvidia.com/gpu"

type simWorkload struct {
	def      workloadDef
	replicas int32
	hash     string // pod-template-hash des Deployments
	nextJob  time.Time
	// Un seul replica crashe / reste non ready ; le drapeau se libère quand ce
	// pod disparaît, pour que son remplaçant reprenne le rôle.
	crashTaken, notReadyTaken bool
	last                      model.Workload
	emitted                   bool
}

type simPod struct {
	pod          model.Pod
	wl           *simWorkload
	ordinal      int
	pendingSince time.Time
	deadline     time.Time // prochaine transition ; zéro = aucune
	crash        bool
	notReady     bool
	dirty        bool
}

type simNode struct {
	node    model.Node
	emitted model.Resources
	sent    bool
}

type Sim struct {
	mu          sync.Mutex
	sink        Sink
	rng         *rand.Rand
	now         time.Time
	nodes       []*simNode
	workloads   []*simWorkload
	pods        []*simPod
	nextChurn   time.Time
	nextMetrics time.Time
}

// New construit le cluster, place les pods initiaux et publie l'état complet.
func New(sink Sink, opts Options) *Sim {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	s := &Sim{sink: sink, rng: rand.New(rand.NewPCG(opts.Seed, opts.Seed^0x9e3779b97f4a7c15)), now: now}
	s.buildNodes()
	for _, d := range workloads {
		w := &simWorkload{def: d, replicas: d.Replicas, hash: s.rid(10)}
		if d.Kind == "Job" {
			w.nextJob = now.Add(secs(jobFirstSec))
		}
		s.workloads = append(s.workloads, w)
	}
	s.placeInitialPods()
	s.nextChurn = now.Add(secs(churnPeriodSec))
	s.flush()
	s.publishMetrics()
	return s
}

// Run fait avancer la simulation en temps réel jusqu'à l'annulation du contexte.
func (s *Sim) Run(ctx context.Context) {
	t := time.NewTicker(stepIntervalMillisec * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.Step(now)
		}
	}
}

// Step avance la machine à états jusqu'à now et publie les changements.
func (s *Sim) Step(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
	s.advancePods()
	s.reconcile()
	s.churn()
	s.schedule()
	s.flush()
	if !now.Before(s.nextMetrics) {
		s.publishMetrics()
	}
}

// Scale change le nombre de replicas d'un Deployment ou d'un StatefulSet.
func (s *Sim) Scale(ns, name string, replicas int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.workloads {
		if w.def.NS == ns && w.def.Name == name && (w.def.Kind == "Deployment" || w.def.Kind == "StatefulSet") {
			w.replicas = replicas
		}
	}
}

/* ---------- construction ---------- */

func (s *Sim) buildNodes() {
	created := s.now.Add(-time.Duration(2+s.rng.IntN(7)) * 24 * time.Hour)
	for _, p := range pools {
		group := s.hex(8)
		for i := 0; i < p.Count; i++ {
			n := model.Node{
				Name:         fmt.Sprintf("gke-prod-%s-%s-%s", p.Name, group, s.rid(4)),
				Pool:         p.Name,
				InstanceType: p.Machine,
				Zone:         zones[s.rng.IntN(len(zones))],
				Spot:         p.Spot,
				GPU:          p.GPU,
				Allocatable:  model.Resources{CPU: p.CPU, Memory: p.Mem, Pods: 110},
				Conditions: []model.Condition{
					{Type: "Ready", Status: "True", Reason: "KubeletReady", Message: "kubelet is posting ready status"},
					{Type: "MemoryPressure", Status: "False", Reason: "KubeletHasSufficientMemory"},
					{Type: "DiskPressure", Status: "False", Reason: "KubeletHasNoDiskPressure"},
					{Type: "PIDPressure", Status: "False", Reason: "KubeletHasSufficientPID"},
				},
				Taints:         []model.Taint{},
				KubeletVersion: kubeletVersion,
				CreatedAt:      created.Add(-time.Duration(s.rng.IntN(3600)) * time.Second),
			}
			if p.GPU > 0 {
				n.InstanceType = p.Machine + " + nvidia-l4"
				n.Taints = append(n.Taints, model.Taint{Key: gpuTaintKey, Value: "present", Effect: "NoSchedule"})
			}
			s.nodes = append(s.nodes, &simNode{node: n})
		}
	}
}

// placeInitialPods démarre le cluster déjà en régime : pods placés, âgés de
// 1 à 72 h. Les DaemonSets passent d'abord, comme sur un vrai node.
func (s *Sim) placeInitialPods() {
	order := append([]*simWorkload(nil), s.workloads...)
	sort.SliceStable(order, func(i, j int) bool {
		return order[i].def.Kind == "DaemonSet" && order[j].def.Kind != "DaemonSet"
	})
	for _, w := range order {
		switch w.def.Kind {
		case "DaemonSet":
			for _, n := range s.nodes {
				p := s.newPod(w, 0)
				s.bind(p, n)
				s.startInstantly(p)
			}
		case "Deployment", "StatefulSet":
			for i := 0; i < int(w.replicas); i++ {
				p := s.newPod(w, i)
				if n := s.chooseNode(p); n != nil {
					s.bind(p, n)
					s.startInstantly(p)
				}
			}
		}
	}
}

func (s *Sim) startInstantly(p *simPod) {
	p.pod.CreatedAt = s.now.Add(-time.Duration(1+s.rng.IntN(71))*time.Hour - time.Duration(s.rng.IntN(3600))*time.Second)
	switch {
	case p.wl.def.BadImage:
		s.setStatus(p, "ImagePullBackOff")
	case p.crash:
		p.pod.Restarts = int32(6 + s.rng.IntN(9))
		s.setStatus(p, "CrashLoopBackOff")
		p.deadline = s.now.Add(secs(s.between(0.5, backoffSec)))
	default:
		s.setStatus(p, "Running")
	}
}

/* ---------- pods ---------- */

func (s *Sim) newPod(w *simWorkload, ordinal int) *simPod {
	d := w.def
	var name string
	switch d.Kind {
	case "Deployment":
		name = fmt.Sprintf("%s-%s-%s", d.Name, w.hash, s.rid(5))
	case "StatefulSet":
		name = fmt.Sprintf("%s-%d", d.Name, ordinal)
	default:
		name = fmt.Sprintf("%s-%s", d.Name, s.rid(5))
	}
	p := &simPod{wl: w, ordinal: ordinal, pendingSince: s.now, dirty: true}
	limits := model.Resources{Memory: d.Mem * 3 / 2}
	p.pod = model.Pod{
		UID:       s.uid(),
		Name:      name,
		Namespace: d.NS,
		Owner:     model.OwnerRef{Kind: d.Kind, Name: d.Name},
		Requests:  model.Resources{CPU: d.CPU, Memory: d.Mem},
		Limits:    limits,
		CreatedAt: s.now,
		QOSClass:  "Burstable",
	}
	if d.Crashy && !w.crashTaken {
		p.crash, w.crashTaken = true, true
	}
	if d.NotReady && !w.notReadyTaken {
		p.notReady, w.notReadyTaken = true, true
	}
	s.setStatus(p, "Pending")
	s.pods = append(s.pods, p)
	return p
}

// setStatus aligne phase, readiness et état du container sur ce qu'afficherait
// `kubectl get pods`.
func (s *Sim) setStatus(p *simPod, status string) {
	d := p.wl.def
	c := model.ContainerStatus{Name: d.Name, Image: d.Image, Restarts: p.pod.Restarts}
	phase, ready := "Pending", false
	switch status {
	case "Pending":
		c.State, c.Reason = "waiting", ""
	case "ContainerCreating", "ImagePullBackOff":
		c.State, c.Reason = "waiting", status
	case "Running":
		phase, ready = "Running", !p.notReady
		c.State = "running"
	case "Error":
		phase, c.State, c.Reason = "Running", "terminated", "Error"
	case "CrashLoopBackOff":
		phase, c.State, c.Reason = "Running", "waiting", "CrashLoopBackOff"
	case "Completed":
		phase, c.State, c.Reason = "Succeeded", "terminated", "Completed"
	case "Terminating":
		phase = p.pod.Phase
		c = p.pod.Containers[0]
		c.Ready = false
	}
	if status != "Terminating" {
		c.Ready = ready
	}
	p.pod.Phase = phase
	p.pod.Ready = ready && status != "Terminating"
	p.pod.DisplayStatus = status
	p.pod.Containers = []model.ContainerStatus{c}
	p.dirty = true
}

func (s *Sim) bind(p *simPod, n *simNode) {
	p.pod.NodeName = n.node.Name
	p.pod.PodIP = fmt.Sprintf("10.52.%d.%d", s.nodeIndex(n), 2+s.rng.IntN(248))
	p.pod.StatusMessage = ""
	p.dirty = true
}

func (s *Sim) terminate(p *simPod) {
	if p.pod.DisplayStatus == "Terminating" {
		return
	}
	s.setStatus(p, "Terminating")
	p.deadline = s.now.Add(secs(terminatingSec))
}

func (s *Sim) remove(p *simPod) {
	for i, q := range s.pods {
		if q == p {
			s.pods = append(s.pods[:i], s.pods[i+1:]...)
			break
		}
	}
	if p.crash {
		p.wl.crashTaken = false
	}
	if p.notReady {
		p.wl.notReadyTaken = false
	}
	s.sink.Delete(stream.KindPod, p.pod.UID, p.pod)
}

func (s *Sim) due(p *simPod) bool { return !p.deadline.IsZero() && !s.now.Before(p.deadline) }

func (s *Sim) advancePods() {
	for _, p := range append([]*simPod(nil), s.pods...) {
		if !s.due(p) {
			continue
		}
		p.deadline = time.Time{}
		isJob := p.wl.def.Kind == "Job"
		switch p.pod.DisplayStatus {
		case "Terminating":
			s.remove(p)
		case "ContainerCreating":
			switch {
			case p.wl.def.BadImage:
				s.setStatus(p, "ImagePullBackOff")
			case isJob:
				s.setStatus(p, "Running")
				p.deadline = s.now.Add(secs(jobRunSec))
			default:
				s.setStatus(p, "Running")
				if p.crash {
					p.deadline = s.now.Add(secs(s.between(firstCrashMinSec, firstCrashMaxSec)))
				}
			}
		case "Running":
			switch {
			case isJob:
				s.setStatus(p, "Completed")
				p.deadline = s.now.Add(secs(jobTTLSec))
			case p.crash:
				p.pod.Restarts++
				s.setStatus(p, "Error")
				p.deadline = s.now.Add(secs(errorSec))
			}
		case "Error":
			s.setStatus(p, "CrashLoopBackOff")
			p.deadline = s.now.Add(secs(backoffSec))
		case "CrashLoopBackOff":
			s.setStatus(p, "Running")
			p.deadline = s.now.Add(secs(s.between(nextCrashMinSec, nextCrashMaxSec)))
		case "Completed":
			s.remove(p)
		}
	}
}

/* ---------- contrôleurs ---------- */

func (s *Sim) livePods(w *simWorkload) []*simPod {
	var out []*simPod
	for _, p := range s.pods {
		if p.wl == w && p.pod.DisplayStatus != "Terminating" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Sim) reconcile() {
	for _, w := range s.workloads {
		live := s.livePods(w)
		switch w.def.Kind {
		case "Deployment":
			for i := len(live); i < int(w.replicas); i++ {
				s.newPod(w, 0)
			}
			if extra := len(live) - int(w.replicas); extra > 0 {
				// Comme le contrôleur de ReplicaSet : les pods non placés puis les plus récents d'abord.
				sort.SliceStable(live, func(i, j int) bool {
					if (live[i].pod.NodeName == "") != (live[j].pod.NodeName == "") {
						return live[i].pod.NodeName == ""
					}
					return live[i].pod.CreatedAt.After(live[j].pod.CreatedAt)
				})
				for _, p := range live[:extra] {
					s.terminate(p)
				}
			}
		case "StatefulSet":
			have := map[int]bool{}
			for _, p := range live {
				have[p.ordinal] = true
			}
			for i := 0; i < int(w.replicas); i++ {
				if !have[i] {
					s.newPod(w, i)
				}
			}
			for _, p := range live {
				if p.ordinal >= int(w.replicas) {
					s.terminate(p)
				}
			}
		case "DaemonSet":
			onNode := map[string]bool{}
			for _, p := range live {
				onNode[p.pod.NodeName] = true
			}
			for _, n := range s.nodes {
				if !onNode[n.node.Name] {
					p := s.newPod(w, 0)
					s.bind(p, n)
					s.setStatus(p, "ContainerCreating")
					p.deadline = s.now.Add(secs(s.between(creatingMinSec, creatingMaxSec)))
				}
			}
		case "Job":
			if !s.now.Before(w.nextJob) {
				s.newPod(w, 0)
				w.nextJob = w.nextJob.Add(secs(jobPeriodSec))
			}
		}
	}
}

// churn reproduit les rollouts de staging du prototype.
func (s *Sim) churn() {
	if s.now.Before(s.nextChurn) {
		return
	}
	s.nextChurn = s.nextChurn.Add(secs(churnPeriodSec))
	if s.rng.Float64() >= churnProbability {
		return
	}
	var c []*simPod
	for _, p := range s.pods {
		if p.pod.Namespace == "staging" && p.pod.DisplayStatus == "Running" {
			c = append(c, p)
		}
	}
	if len(c) > 0 {
		s.terminate(c[s.rng.IntN(len(c))])
	}
}

/* ---------- scheduler ---------- */

func (s *Sim) schedule() {
	for _, p := range s.pods {
		if p.pod.DisplayStatus != "Pending" || p.pod.NodeName != "" ||
			s.now.Sub(p.pendingSince) < secs(pendingDelaySec) {
			continue
		}
		if n := s.chooseNode(p); n != nil {
			s.bind(p, n)
			s.setStatus(p, "ContainerCreating")
			p.deadline = s.now.Add(secs(s.between(creatingMinSec, creatingMaxSec)))
			continue
		}
		if msg := s.failReason(p); msg != p.pod.StatusMessage {
			p.pod.StatusMessage = msg
			p.dirty = true
		}
	}
}

type usage struct {
	cpu, mem int64
	gpu      int
}

func (s *Sim) usageOf(n *simNode) usage {
	var u usage
	for _, p := range s.pods {
		if p.pod.NodeName != n.node.Name || p.pod.DisplayStatus == "Completed" {
			continue
		}
		u.cpu += p.pod.Requests.CPU
		u.mem += p.pod.Requests.Memory
		if p.wl.def.GPU {
			u.gpu++
		}
	}
	return u
}

// fit renvoie "" si le pod peut aller sur le node, sinon la raison telle que
// l'écrit kube-scheduler.
func (s *Sim) fit(p *simPod, n *simNode) string {
	d := p.wl.def
	switch {
	case n.node.Unschedulable:
		return "node(s) were unschedulable"
	case d.GPU && n.node.GPU == 0:
		return "node(s) didn't match Pod's node affinity/selector"
	case n.node.GPU > 0 && !d.GPU && !d.ToleraAll:
		return "node(s) had untolerated taint {nvidia.com/gpu: present}"
	}
	u := s.usageOf(n)
	switch {
	case u.cpu+d.CPU > n.node.Allocatable.CPU:
		return "Insufficient cpu"
	case u.mem+d.Mem > n.node.Allocatable.Memory:
		return "Insufficient memory"
	case d.GPU && u.gpu >= n.node.GPU:
		return "Insufficient nvidia.com/gpu"
	}
	return ""
}

// chooseNode suit l'esprit de kube-scheduler : répartir les replicas d'un même
// workload, puis préférer le node le moins chargé.
func (s *Sim) chooseNode(p *simPod) *simNode {
	var best *simNode
	var bestSame int
	var bestLoad float64
	for _, n := range s.nodes {
		if s.fit(p, n) != "" {
			continue
		}
		same := 0
		for _, q := range s.pods {
			if q.wl == p.wl && q.pod.NodeName == n.node.Name && q.pod.DisplayStatus != "Terminating" {
				same++
			}
		}
		load := float64(s.usageOf(n).cpu) / float64(n.node.Allocatable.CPU)
		if best == nil || same < bestSame || (same == bestSame && load < bestLoad) {
			best, bestSame, bestLoad = n, same, load
		}
	}
	return best
}

func (s *Sim) failReason(p *simPod) string {
	counts := map[string]int{}
	for _, n := range s.nodes {
		counts[s.fit(p, n)]++
	}
	var parts []string
	for r, c := range counts {
		if r != "" {
			parts = append(parts, fmt.Sprintf("%d %s", c, r))
		}
	}
	sort.Strings(parts)
	total := len(s.nodes)
	return fmt.Sprintf("0/%d nodes are available: %s. preemption: 0/%d nodes are available: %d Preemption is not helpful for scheduling.",
		total, strings.Join(parts, ", "), total, total)
}

/* ---------- publication ---------- */

func (s *Sim) flush() {
	for _, n := range s.nodes {
		u := s.usageOf(n)
		n.node.Requested = model.Resources{CPU: u.cpu, Memory: u.mem, Pods: int64(s.podCount(n))}
		if !n.sent || n.node.Requested != n.emitted {
			n.sent, n.emitted = true, n.node.Requested
			s.sink.Upsert(stream.KindNode, n.node.Name, n.node)
		}
	}
	for _, w := range s.workloads {
		cur := s.workloadModel(w)
		if !w.emitted || cur.Replicas != w.last.Replicas || cur.ReadyReplicas != w.last.ReadyReplicas {
			w.emitted, w.last = true, cur
			s.sink.Upsert(stream.KindWorkload, model.WorkloadKey(cur), cur)
		}
	}
	for _, p := range s.pods {
		if p.dirty {
			p.dirty = false
			s.sink.Upsert(stream.KindPod, p.pod.UID, p.pod)
		}
	}
}

func (s *Sim) podCount(n *simNode) int {
	c := 0
	for _, p := range s.pods {
		if p.pod.NodeName == n.node.Name {
			c++
		}
	}
	return c
}

func (s *Sim) workloadModel(w *simWorkload) model.Workload {
	m := model.Workload{Kind: w.def.Kind, Name: w.def.Name, Namespace: w.def.NS, Replicas: w.replicas}
	if w.def.Kind == "DaemonSet" {
		m.Replicas = int32(len(s.nodes))
	}
	for _, p := range s.livePods(w) {
		if p.pod.Ready {
			m.ReadyReplicas++
		}
	}
	if w.def.Argo {
		m.Argo = &model.ArgoInfo{Application: w.def.NS + "-apps", SyncStatus: "Synced"}
	}
	return m
}

func (s *Sim) publishMetrics() {
	s.nextMetrics = s.now.Add(secs(metricsPeriodSec))
	m := model.Metrics{Pods: map[string]model.Usage{}, Nodes: map[string]model.Usage{}}
	for _, p := range s.pods {
		if p.pod.DisplayStatus != "Running" {
			continue
		}
		u := model.Usage{
			CPU:    int64(float64(p.pod.Requests.CPU) * s.between(0.25, 1.15)),
			Memory: int64(float64(p.pod.Requests.Memory) * s.between(0.45, 0.95)),
		}
		m.Pods[p.pod.UID] = u
		n := m.Nodes[p.pod.NodeName]
		n.CPU += u.CPU
		n.Memory += u.Memory
		m.Nodes[p.pod.NodeName] = n
	}
	s.sink.SetMetrics(m)
}

/* ---------- utilitaires ---------- */

func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

func (s *Sim) between(a, b float64) float64 { return a + s.rng.Float64()*(b-a) }

// rid produit un suffixe à la manière des noms générés par Kubernetes.
func (s *Sim) rid(n int) string {
	const alphabet = "bcdfghjklmnpqrstvwxz2456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[s.rng.IntN(len(alphabet))]
	}
	return string(b)
}

func (s *Sim) hex(n int) string {
	const alphabet = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[s.rng.IntN(len(alphabet))]
	}
	return string(b)
}

func (s *Sim) uid() string {
	h := s.hex(32)
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

func (s *Sim) nodeIndex(n *simNode) int {
	for i, m := range s.nodes {
		if m == n {
			return i
		}
	}
	return 0
}
