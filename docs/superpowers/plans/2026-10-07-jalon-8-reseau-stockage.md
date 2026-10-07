# Jalon 8 — Réseau et stockage dans la ville : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** ajouter à la ville les Services (relais sur des avenues), les entrées (portes : Ingress et IngressRoute Traefik) et les PVC (citernes dans un quartier Entrepôts), reliés aux pods par des lignes au sol, avec un inspecteur en lecture seule. Design : [`docs/superpowers/specs/2026-10-07-jalon-8-reseau-stockage-design.md`](../specs/2026-10-07-jalon-8-reseau-stockage-design.md).

**Architecture :** le backend résout tout. Trois nouveaux kinds dans `/api/stream` (`service`, `route`, `volume`), calculés par la boucle « objets sales » de `internal/kube` à partir de nouveaux informers (dont un informer dynamique pour les IngressRoute) et produits à l'identique par le simulateur. Le front ne fait que disposer (fonctions pures `layout.ts`, `netLayout.ts`, `links.ts`) et dessiner (`City`, `Network`, `Links`).

**Tech Stack :** Go 1.26, client-go v0.37 (informers typés et dynamiques, fakes), React 19, React Three Fiber, three.js (InstancedMesh, ShaderMaterial), Zustand, Vitest, Playwright, Helm.

**Commits :** messages en français au format des jalons précédents (`feat(kube): …`) ; chaque message se termine par la ligne d'attribution demandée par l'environnement.

---

## Décisions

1. **Clés** : Service `namespace/name`, route `Source/namespace/name` (`Ingress/production/storefront`, `IngressRoute/monitoring/grafana`), volume `namespace/name`, porte = son nom. Côté front, les objets reliés par un lien sont désignés par `type:clé` (`gate:nginx`, `service:production/api`, `pod:<uid>`, `volume:production/data-0`, `route:Ingress/production/storefront`).
2. **Santé d'un Service** : calculée sur toutes les adresses de ses EndpointSlices (pods ou non, pour que `default/kubernetes` soit `ok`) ; `endpoints` ne liste que les pods. `ready` absent vaut ready (sémantique de l'API).
3. **Types optionnels** : Services, EndpointSlices, Ingress, IngressClass, PVC et IngressRoute ne sont démarrés que si un `list` d'essai (`limit=1`) n'est pas refusé (403 ou 404) ; sinon warning et type désactivé. Sans le lister des Services, l'état d'un backend vaut `ok` (inconnu).
4. **IngressRoute** : informer dynamique par groupe servi (`traefik.io`, `traefik.containo.us`), découverts au démarrage. Si un même nom existe dans les deux groupes, `traefik.io` l'emporte.
5. **Liens profonds** : par chemin, comme l'existant : `/services/<ns>/<nom>`, `/routes/<ingress|ingressroute>/<ns>/<nom>`, `/volumes/<ns>/<nom>`, `/gates/<nom>` (corrige la spec, qui parlait de `?select=`).
6. **Événements** : la route `/api/namespaces/{ns}/{resource}/{name}/events` remplace celle des pods (même forme pour `pods`) ; `resource` ∈ `pods`, `services`, `persistentvolumeclaims`, `ingresses`, `ingressroutes`.
7. **Animation** : paquets et gouttes à 15 images/s (invalidation programmée), aucune frame si `prefers-reduced-motion`, onglet caché ou vue éloignée.
8. **Atténuation** : sélectionner une porte, une route, un Service ou un PVC estompe les pods hors du chemin ; sélectionner un pod allume seulement ses liens (comportement actuel inchangé pour les autres pods).
9. **Démo à l'échelle** : chaque équipe simulée (25 pods) a 4 Services (`api`, `web`, `gateway`, `db` headless), 2 PVC (`data-db-0`, `data-db-1`), une Ingress nginx, et une IngressRoute traefik une équipe sur trois. `--demo-scale 100x30` donne environ 450 Services et 230 PVC (la spec est corrigée en conséquence).

## Fichiers

**Backend**
- Créer `internal/model/network.go` : `Service`, `Route`, `Volume`, `ServiceHealth`, clés.
- Modifier `internal/stream/hub.go`, `internal/stream/ws.go` : kinds et snapshot.
- Créer `internal/kube/network.go` : conversions Service, Ingress, IngressRoute, PVC, `PodClaims`, `claimVolumes`.
- Créer `internal/kube/source_net.go` : informers optionnels, index, déclenchements, `build*`.
- Créer `internal/kube/traefik.go` : découverte et informers dynamiques des IngressRoute.
- Modifier `internal/kube/source.go` : appel de `startNetwork`, index des pods par PVC, transform, `ObjectEvents`.
- Modifier `internal/access/filter.go` : droits des trois kinds.
- Modifier `internal/inspect/inspect.go`, `internal/kube/inspector.go`, `internal/server/server.go`, `internal/logs/logs_test.go` : kinds YAML et événements génériques.
- Créer `internal/demo/network.go` ; modifier `internal/demo/{sim.go,scale.go,inspect.go}`.
- Modifier `cmd/atlas/main.go`, `deploy/helm/cluster-atlas/templates/clusterrole.yaml`.

**Front**
- Modifier `web/src/api/types.ts`, `web/src/store/cluster.ts`, `web/src/store/fixtures.ts` ; créer `web/src/store/net.ts`.
- Modifier `web/src/scene/{layout.ts,colors.ts,theme.ts,world.ts,City.tsx,Scene.tsx,Selection.tsx,Pods.tsx,pick.ts,Stacks.tsx}`, `web/src/styles.css`.
- Créer `web/src/scene/{netLayout.ts,links.ts,health.ts,Network.tsx,Links.tsx}`.
- Modifier `web/src/api/inspect.ts`, `web/src/inspector/{Inspector.tsx,EventsTab.tsx}` ; créer `web/src/inspector/{NetOverview.tsx,RefYamlTab.tsx}`.
- Modifier `web/src/ui/{tree.ts,ListView.tsx,searchRank.ts,Search.tsx,route.ts}`, `web/src/App.tsx` ; créer `web/src/ui/PathSummary.tsx`.
- Créer `web/e2e/network.spec.ts` ; modifier `web/e2e/helpers.ts`.

**Banc et docs**
- Créer `hack/scenarios/network.yaml`, `hack/scenarios-traefik/ingressroute.yaml` ; modifier `Makefile`, `hack/load/kwok-up.sh`, `internal/kube/live_test.go`.
- Modifier `README.md`, `docs/spec.md`, la spec du jalon.

---

## Phase A — Backend

### Task 1 : modèle réseau et stockage

**Files:**
- Create: `internal/model/network.go`
- Test: `internal/model/model_test.go`

- [ ] **Step 1 : test qui échoue**

Ajouter à `internal/model/model_test.go` :

```go
func TestServiceHealth(t *testing.T) {
	cases := []struct {
		typ          string
		total, ready int
		want         string
	}{
		{"ClusterIP", 3, 3, HealthOK},
		{"ClusterIP", 3, 1, HealthDegraded},
		{"ClusterIP", 3, 0, HealthDown},
		{"ClusterIP", 0, 0, HealthDown},
		{"ExternalName", 0, 0, HealthExternal},
	}
	for _, c := range cases {
		if got := ServiceHealth(c.typ, c.total, c.ready); got != c.want {
			t.Errorf("ServiceHealth(%s, %d, %d) = %s, attendu %s", c.typ, c.total, c.ready, got, c.want)
		}
	}
}

func TestNetworkKeys(t *testing.T) {
	if k := ServiceKey(Service{Namespace: "prod", Name: "api"}); k != "prod/api" {
		t.Errorf("ServiceKey = %s", k)
	}
	if k := RouteKey(Route{Source: "IngressRoute", Namespace: "mon", Name: "grafana"}); k != "IngressRoute/mon/grafana" {
		t.Errorf("RouteKey = %s", k)
	}
	if k := VolumeKey(Volume{Namespace: "prod", Name: "data-0"}); k != "prod/data-0" {
		t.Errorf("VolumeKey = %s", k)
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/model/ -run 'TestServiceHealth|TestNetworkKeys'`
Expected: FAIL, `undefined: ServiceHealth`.

- [ ] **Step 3 : implémentation**

Créer `internal/model/network.go` :

```go
package model

// Santé d'un Service : couleur du voyant de son relais.
const (
	HealthOK       = "ok"
	HealthDegraded = "degraded"
	HealthDown     = "down"
	HealthExternal = "external"
)

type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	TargetPort string `json:"targetPort,omitempty"`
	Protocol   string `json:"protocol"`
	NodePort   int32  `json:"nodePort,omitempty"`
}

// Endpoint : un pod derrière un Service, lu dans ses EndpointSlices.
type Endpoint struct {
	PodUID string `json:"podUID"`
	Ready  bool   `json:"ready"`
}

type Service struct {
	Namespace    string        `json:"namespace"`
	Name         string        `json:"name"`
	Type         string        `json:"type"` // ClusterIP | NodePort | LoadBalancer | ExternalName
	Headless     bool          `json:"headless,omitempty"`
	ClusterIP    string        `json:"clusterIP,omitempty"`
	Ports        []ServicePort `json:"ports"`
	LoadBalancer []string      `json:"loadBalancer,omitempty"`
	ExternalName string        `json:"externalName,omitempty"`
	Endpoints    []Endpoint    `json:"endpoints"`
	Health       string        `json:"health"`
}

// ServiceHealth : ok si toutes les adresses sont ready, degraded si une partie,
// down si aucune (ou aucune adresse) ; external pour un ExternalName.
func ServiceHealth(typ string, total, ready int) string {
	switch {
	case typ == "ExternalName":
		return HealthExternal
	case ready == 0:
		return HealthDown
	case ready < total:
		return HealthDegraded
	}
	return HealthOK
}

// État du backend d'une règle de route.
const (
	BackendOK       = "ok"
	BackendMissing  = "missing"  // Service introuvable
	BackendIndirect = "indirect" // TraefikService, non résolu
)

type Backend struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      string `json:"port,omitempty"`
	Kind      string `json:"kind"` // Service | TraefikService
	State     string `json:"state"`
}

type Rule struct {
	Host    string  `json:"host,omitempty"`
	Path    string  `json:"path,omitempty"`
	Match   string  `json:"match,omitempty"` // règle Traefik brute
	Backend Backend `json:"backend"`
}

// Route : une Ingress ou une IngressRoute Traefik, rattachée à sa porte
// (le contrôleur d'entrée qui la sert).
type Route struct {
	Source    string   `json:"source"` // Ingress | IngressRoute
	Group     string   `json:"group"`  // networking.k8s.io | traefik.io | traefik.containo.us
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Gate      string   `json:"gate"`
	Rules     []Rule   `json:"rules"`
	Addresses []string `json:"addresses,omitempty"`
}

// Volume : un PersistentVolumeClaim et les pods qui le montent. Tailles en octets.
type Volume struct {
	Namespace    string   `json:"namespace"`
	Name         string   `json:"name"`
	StorageClass string   `json:"storageClass"`
	Requested    int64    `json:"requested"`
	Capacity     int64    `json:"capacity"`
	AccessModes  []string `json:"accessModes"`
	Phase        string   `json:"phase"` // Pending | Bound | Lost
	VolumeName   string   `json:"volumeName,omitempty"`
	Pods         []string `json:"pods"`
}

func ServiceKey(s Service) string { return s.Namespace + "/" + s.Name }
func RouteKey(r Route) string     { return r.Source + "/" + r.Namespace + "/" + r.Name }
func VolumeKey(v Volume) string   { return v.Namespace + "/" + v.Name }
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/model/`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/model
git commit -m "feat(model): Services, routes et volumes du jalon 8"
```

### Task 2 : trois kinds dans le flux

**Files:**
- Modify: `internal/stream/hub.go` (constantes `Kind`, `Message`, `snapshotLocked`)
- Modify: `internal/stream/ws.go` (`view.snapshot`)
- Test: `internal/stream/hub_test.go`, `internal/stream/ws_test.go`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `internal/stream/hub_test.go` :

```go
func TestSnapshotCarriesNetworkAndStorage(t *testing.T) {
	h := NewHub(Options{})
	h.Upsert(KindService, "prod/b", model.Service{Namespace: "prod", Name: "b"})
	h.Upsert(KindService, "prod/a", model.Service{Namespace: "prod", Name: "a"})
	h.Upsert(KindRoute, "Ingress/prod/web", model.Route{Source: "Ingress", Namespace: "prod", Name: "web"})
	h.Upsert(KindVolume, "prod/data", model.Volume{Namespace: "prod", Name: "data"})
	init, sub := h.Subscribe(0)
	defer sub.Close()
	m := init[0]
	if m.Type != "snapshot" || len(m.Services) != 2 || m.Services[0].Name != "a" || len(m.Routes) != 1 || len(m.Volumes) != 1 {
		t.Fatalf("snapshot = %+v", m)
	}
}
```

Ajouter à `internal/stream/ws_test.go` :

```go
// nsOnly ne laisse passer les objets réseau et stockage que d'un namespace.
type nsOnly string

func (o nsOnly) Allow(_ context.Context, _ Kind, obj any) bool {
	switch x := obj.(type) {
	case model.Service:
		return x.Namespace == string(o)
	case model.Route:
		return x.Namespace == string(o)
	case model.Volume:
		return x.Namespace == string(o)
	}
	return true
}
func (nsOnly) Changed(context.Context) bool { return false }

func TestViewFiltersNetworkAndStorage(t *testing.T) {
	v := newView(nsOnly("prod"))
	out := v.apply(context.Background(), []Message{{Type: "snapshot",
		Services: []model.Service{{Namespace: "prod", Name: "a"}, {Namespace: "kube-system", Name: "kube-dns"}},
		Routes:   []model.Route{{Namespace: "kube-system", Name: "x"}},
		Volumes:  []model.Volume{{Namespace: "prod", Name: "data"}},
	}})
	s := out[0]
	if len(s.Services) != 1 || s.Services[0].Name != "a" || len(s.Routes) != 0 || len(s.Volumes) != 1 {
		t.Fatalf("snapshot filtré = %+v", s)
	}
	d := v.apply(context.Background(), []Message{{Type: "upsert", Kind: KindService, Obj: model.Service{Namespace: "kube-system", Name: "kube-dns"}}})
	if len(d) != 0 {
		t.Errorf("delta d'un namespace interdit transmis : %+v", d)
	}
}
```

Vérifier que `ws_test.go` importe `context` et `internal/model` (sinon les ajouter).

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/stream/`
Expected: FAIL, `undefined: KindService`.

- [ ] **Step 3 : implémentation**

Dans `internal/stream/hub.go`, compléter les constantes et `Message` :

```go
const (
	KindNode      Kind = "node"
	KindPod       Kind = "pod"
	KindWorkload  Kind = "workload"
	KindNamespace Kind = "namespace"
	KindService   Kind = "service"
	KindRoute     Kind = "route"
	KindVolume    Kind = "volume"
)
```

```go
	Namespaces []model.Namespace `json:"namespaces,omitempty"`
	Services   []model.Service   `json:"services,omitempty"`
	Routes     []model.Route     `json:"routes,omitempty"`
	Volumes    []model.Volume    `json:"volumes,omitempty"`
	Metrics    *model.Metrics    `json:"metrics,omitempty"`
```

Remplacer `snapshotLocked` :

```go
func (h *Hub) snapshotLocked() Message {
	m := Message{Type: "snapshot", Rev: h.rev,
		Nodes: []model.Node{}, Pods: []model.Pod{}, Workloads: []model.Workload{}, Namespaces: []model.Namespace{},
		Services: []model.Service{}, Routes: []model.Route{}, Volumes: []model.Volume{}}
	for _, obj := range h.state {
		switch o := obj.(type) {
		case model.Node:
			m.Nodes = append(m.Nodes, o)
		case model.Pod:
			m.Pods = append(m.Pods, o)
		case model.Workload:
			m.Workloads = append(m.Workloads, o)
		case model.Namespace:
			m.Namespaces = append(m.Namespaces, o)
		case model.Service:
			m.Services = append(m.Services, o)
		case model.Route:
			m.Routes = append(m.Routes, o)
		case model.Volume:
			m.Volumes = append(m.Volumes, o)
		}
	}
	sort.Slice(m.Nodes, func(i, j int) bool { return m.Nodes[i].Name < m.Nodes[j].Name })
	sort.Slice(m.Pods, func(i, j int) bool { return m.Pods[i].UID < m.Pods[j].UID })
	sort.Slice(m.Namespaces, func(i, j int) bool { return m.Namespaces[i].Name < m.Namespaces[j].Name })
	sort.Slice(m.Workloads, func(i, j int) bool {
		return model.WorkloadKey(m.Workloads[i]) < model.WorkloadKey(m.Workloads[j])
	})
	sort.Slice(m.Services, func(i, j int) bool { return model.ServiceKey(m.Services[i]) < model.ServiceKey(m.Services[j]) })
	sort.Slice(m.Routes, func(i, j int) bool { return model.RouteKey(m.Routes[i]) < model.RouteKey(m.Routes[j]) })
	sort.Slice(m.Volumes, func(i, j int) bool { return model.VolumeKey(m.Volumes[i]) < model.VolumeKey(m.Volumes[j]) })
	return m
}
```

Dans `internal/stream/ws.go`, `view.snapshot` : initialiser `Services: []model.Service{}, Routes: []model.Route{}, Volumes: []model.Volume{}` dans `s`, puis avant `return s` :

```go
	for _, o := range m.Services {
		if v.f.Allow(ctx, KindService, o) {
			s.Services = append(s.Services, o)
		}
	}
	for _, o := range m.Routes {
		if v.f.Allow(ctx, KindRoute, o) {
			s.Routes = append(s.Routes, o)
		}
	}
	for _, o := range m.Volumes {
		if v.f.Allow(ctx, KindVolume, o) {
			s.Volumes = append(s.Volumes, o)
		}
	}
```

Les deltas de ces kinds passent par la branche `known == nil` de `view.delta` (visibles selon le droit courant) : rien à changer.

- [ ] **Step 4 : vérifier**

Run: `go test -race ./internal/stream/`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/stream
git commit -m "feat(stream): kinds service, route et volume dans le snapshot et les deltas"
```

### Task 3 : conversion des Services

**Files:**
- Create: `internal/kube/network.go`
- Test: `internal/kube/network_test.go`

- [ ] **Step 1 : test qui échoue**

Créer `internal/kube/network_test.go` :

```go
package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

func bptr(b bool) *bool { return &b }

func slice(svc string, eps ...discoveryv1.Endpoint) *discoveryv1.EndpointSlice {
	return &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{Name: svc + "-x", Namespace: "prod", Labels: map[string]string{discoveryv1.LabelServiceName: svc}},
		Endpoints:  eps,
	}
}

func podEp(uid string, ready *bool) discoveryv1.Endpoint {
	return discoveryv1.Endpoint{Addresses: []string{"10.0.0." + uid}, Conditions: discoveryv1.EndpointConditions{Ready: ready},
		TargetRef: &corev1.ObjectReference{Kind: "Pod", UID: types.UID(uid)}}
}

func TestConvertService(t *testing.T) {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
		Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, ClusterIP: "10.96.0.10", Selector: map[string]string{"app": "api"},
			Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP, NodePort: 31000}}},
		Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "34.1.2.3"}}}}}
	// Double pile : le pod 1 apparaît dans deux slices ; ready absent vaut ready.
	m, ok := ConvertService(svc, []*discoveryv1.EndpointSlice{
		slice("api", podEp("1", bptr(true)), podEp("2", bptr(false))),
		slice("api", podEp("1", nil)),
	})
	if !ok {
		t.Fatal("Service avec selector non publié")
	}
	want := []model.Endpoint{{PodUID: "1", Ready: true}, {PodUID: "2", Ready: false}}
	if len(m.Endpoints) != 2 || m.Endpoints[0] != want[0] || m.Endpoints[1] != want[1] {
		t.Errorf("endpoints = %+v", m.Endpoints)
	}
	if m.Health != model.HealthDegraded || m.Type != "LoadBalancer" || m.LoadBalancer[0] != "34.1.2.3" {
		t.Errorf("service = %+v", m)
	}
	if p := m.Ports[0]; p.Port != 80 || p.TargetPort != "http" || p.NodePort != 31000 || p.Protocol != "TCP" {
		t.Errorf("port = %+v", p)
	}
}

func TestConvertServiceWithoutPods(t *testing.T) {
	// Service sans selector mais avec une slice (default/kubernetes) : adresse sans pod, ready.
	k8s := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"}, Spec: corev1.ServiceSpec{ClusterIP: "10.96.0.1"}}
	m, ok := ConvertService(k8s, []*discoveryv1.EndpointSlice{slice("kubernetes", discoveryv1.Endpoint{Addresses: []string{"172.18.0.2"}})})
	if !ok || m.Health != model.HealthOK || len(m.Endpoints) != 0 || m.Type != "ClusterIP" {
		t.Errorf("kubernetes = %v %+v", ok, m)
	}
	// Ni selector ni slice : pas publié.
	if _, ok := ConvertService(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "manual"}}, nil); ok {
		t.Error("Service sans selector ni slice publié")
	}
	// Selector sans endpoint : down. ExternalName : external. Headless : sans IP.
	down, _ := ConvertService(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "ghost"}, Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "x"}, ClusterIP: "None"}}, nil)
	if down.Health != model.HealthDown || !down.Headless || down.ClusterIP != "" {
		t.Errorf("ghost = %+v", down)
	}
	ext, ok := ConvertService(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "stripe"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "api.stripe.com"}}, nil)
	if !ok || ext.Health != model.HealthExternal || ext.ExternalName != "api.stripe.com" {
		t.Errorf("stripe = %v %+v", ok, ext)
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run TestConvertService`
Expected: FAIL, `undefined: ConvertService`.

- [ ] **Step 3 : implémentation**

Créer `internal/kube/network.go` :

```go
package kube

import (
	"sort"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// ConvertService réduit un Service et ses EndpointSlices. La santé compte
// toutes les adresses (un Service sans selector comme default/kubernetes reste
// sain) ; Endpoints ne garde que les pods, dédoublonnés (double pile). Un
// Service sans selector ni slice n'est pas publié (publish=false), sauf
// ExternalName.
func ConvertService(s *corev1.Service, slices []*discoveryv1.EndpointSlice) (m model.Service, publish bool) {
	m = model.Service{Namespace: s.Namespace, Name: s.Name, Type: string(s.Spec.Type), ClusterIP: s.Spec.ClusterIP,
		Headless: s.Spec.ClusterIP == corev1.ClusterIPNone, ExternalName: s.Spec.ExternalName,
		Ports: []model.ServicePort{}, Endpoints: []model.Endpoint{}}
	if m.Type == "" {
		m.Type = string(corev1.ServiceTypeClusterIP)
	}
	if m.Headless {
		m.ClusterIP = ""
	}
	for _, p := range s.Spec.Ports {
		m.Ports = append(m.Ports, model.ServicePort{Name: p.Name, Port: p.Port, TargetPort: p.TargetPort.String(),
			Protocol: string(p.Protocol), NodePort: p.NodePort})
	}
	for _, in := range s.Status.LoadBalancer.Ingress {
		if in.IP != "" {
			m.LoadBalancer = append(m.LoadBalancer, in.IP)
		} else if in.Hostname != "" {
			m.LoadBalancer = append(m.LoadBalancer, in.Hostname)
		}
	}

	// Une adresse est identifiée par son pod, sinon par sa première IP.
	readyByID := map[string]bool{}
	pods := map[string]int{}
	for _, sl := range slices {
		for _, e := range sl.Endpoints {
			ready := e.Conditions.Ready == nil || *e.Conditions.Ready
			id := ""
			if e.TargetRef != nil && e.TargetRef.Kind == "Pod" && e.TargetRef.UID != "" {
				id = "pod:" + string(e.TargetRef.UID)
				if i, ok := pods[id]; ok {
					m.Endpoints[i].Ready = m.Endpoints[i].Ready || ready
				} else {
					pods[id] = len(m.Endpoints)
					m.Endpoints = append(m.Endpoints, model.Endpoint{PodUID: string(e.TargetRef.UID), Ready: ready})
				}
			} else if len(e.Addresses) > 0 {
				id = "ip:" + e.Addresses[0]
			} else {
				continue
			}
			readyByID[id] = readyByID[id] || ready
		}
	}
	sort.Slice(m.Endpoints, func(i, j int) bool { return m.Endpoints[i].PodUID < m.Endpoints[j].PodUID })
	ready := 0
	for _, r := range readyByID {
		if r {
			ready++
		}
	}
	m.Health = model.ServiceHealth(m.Type, len(readyByID), ready)
	publish = m.Type == string(corev1.ServiceTypeExternalName) || len(s.Spec.Selector) > 0 || len(slices) > 0
	return m, publish
}
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/kube/ -run TestConvertService`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/kube/network.go internal/kube/network_test.go
git commit -m "feat(kube): conversion des Services et de leurs EndpointSlices"
```

### Task 4 : conversion des Ingress et des IngressRoute

**Files:**
- Modify: `internal/kube/network.go`
- Test: `internal/kube/network_test.go`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `internal/kube/network_test.go` (imports supplémentaires : `networkingv1 "k8s.io/api/networking/v1"`, `"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"`) :

```go
func sptr(s string) *string { return &s }

func TestIngressGate(t *testing.T) {
	def := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "nginx", Annotations: map[string]string{"ingressclass.kubernetes.io/is-default-class": "true"}}}
	other := &networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "traefik"}}
	classes := []*networkingv1.IngressClass{other, def}
	cases := []struct {
		name string
		ing  networkingv1.Ingress
		want string
	}{
		{"spec", networkingv1.Ingress{Spec: networkingv1.IngressSpec{IngressClassName: sptr("traefik")}}, "traefik"},
		{"annotation", networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"kubernetes.io/ingress.class": "haproxy"}}}, "haproxy"},
		{"défaut", networkingv1.Ingress{}, "nginx"},
	}
	for _, c := range cases {
		if got := IngressGate(&c.ing, classes); got != c.want {
			t.Errorf("%s : %s, attendu %s", c.name, got, c.want)
		}
	}
	if got := IngressGate(&networkingv1.Ingress{}, []*networkingv1.IngressClass{other}); got != "default" {
		t.Errorf("sans classe par défaut : %s", got)
	}
}

func TestConvertIngress(t *testing.T) {
	pathType := networkingv1.PathTypePrefix
	backend := func(svc string, port int32) networkingv1.IngressBackend {
		return networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: svc, Port: networkingv1.ServiceBackendPort{Number: port}}}
	}
	def := backend("frontend", 80)
	ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "storefront", Namespace: "prod"},
		Spec: networkingv1.IngressSpec{DefaultBackend: &def, Rules: []networkingv1.IngressRule{{Host: "shop.example.com",
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
				{Path: "/api", PathType: &pathType, Backend: backend("api", 8080)},
				{Path: "/old", PathType: &pathType, Backend: backend("ghost", 80)},
			}}}}}},
		Status: networkingv1.IngressStatus{LoadBalancer: networkingv1.IngressLoadBalancerStatus{Ingress: []networkingv1.IngressLoadBalancerIngress{{IP: "34.1.2.3"}}}}}
	exists := func(ns, name string) bool { return ns == "prod" && name != "ghost" }
	r := ConvertIngress(ing, "nginx", exists)
	if r.Source != "Ingress" || r.Group != "networking.k8s.io" || r.Gate != "nginx" || r.Addresses[0] != "34.1.2.3" || len(r.Rules) != 3 {
		t.Fatalf("route = %+v", r)
	}
	if b := r.Rules[0]; b.Host != "" || b.Backend.Service != "frontend" || b.Backend.Port != "80" || b.Backend.State != model.BackendOK {
		t.Errorf("defaultBackend = %+v", b)
	}
	if b := r.Rules[1]; b.Host != "shop.example.com" || b.Path != "/api" || b.Backend.Service != "api" || b.Backend.Namespace != "prod" {
		t.Errorf("règle /api = %+v", b)
	}
	if r.Rules[2].Backend.State != model.BackendMissing {
		t.Errorf("ghost = %+v", r.Rules[2])
	}
	if got := routeBackends(r); len(got) != 3 || got[0] != "prod/frontend" {
		t.Errorf("routeBackends = %v", got)
	}
}

func ingressRoute(group, ns, name string, routes []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": group + "/v1alpha1", "kind": "IngressRoute",
		"metadata": map[string]any{"name": name, "namespace": ns},
		"spec":     map[string]any{"entryPoints": []any{"websecure"}, "routes": routes},
	}}
}

func TestConvertIngressRoute(t *testing.T) {
	u := ingressRoute("traefik.io", "mon", "grafana", []any{
		map[string]any{"match": "Host(`grafana.example.com`) && PathPrefix(`/d`)", "kind": "Rule", "services": []any{
			map[string]any{"name": "grafana", "port": int64(3000)},
			map[string]any{"name": "auth", "namespace": "sso", "port": "http"},
			map[string]any{"name": "weighted", "kind": "TraefikService"},
		}},
	})
	r := ConvertIngressRoute(u, func(ns, name string) bool { return name != "auth" })
	if r.Source != "IngressRoute" || r.Group != "traefik.io" || r.Gate != "traefik" || len(r.Rules) != 3 {
		t.Fatalf("route = %+v", r)
	}
	g := r.Rules[0]
	if g.Host != "grafana.example.com" || g.Path != "/d" || g.Match == "" || g.Backend.Port != "3000" || g.Backend.Namespace != "mon" || g.Backend.State != model.BackendOK {
		t.Errorf("grafana = %+v", g)
	}
	if a := r.Rules[1].Backend; a.Namespace != "sso" || a.Port != "http" || a.State != model.BackendMissing {
		t.Errorf("auth = %+v", a)
	}
	if w := r.Rules[2].Backend; w.Kind != "TraefikService" || w.State != model.BackendIndirect {
		t.Errorf("weighted = %+v", w)
	}
	if got := routeBackends(r); len(got) != 2 {
		t.Errorf("un TraefikService n'est pas un Service : %v", got)
	}
	u.SetAnnotations(map[string]string{"kubernetes.io/ingress.class": "traefik-internal"})
	if g := ConvertIngressRoute(u, nil).Gate; g != "traefik-internal" {
		t.Errorf("porte annotée = %s", g)
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'TestIngressGate|TestConvertIngress'`
Expected: FAIL, `undefined: IngressGate`.

- [ ] **Step 3 : implémentation**

Ajouter à `internal/kube/network.go` (imports : `"fmt"`, `"regexp"`, `"strconv"`, `networkingv1 "k8s.io/api/networking/v1"`, `"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"`) :

```go
const (
	ingressClassAnnotation = "kubernetes.io/ingress.class"
	defaultClassAnnotation = "ingressclass.kubernetes.io/is-default-class"
	traefikGate            = "traefik"
)

// IngressGate : porte d'une Ingress : spec.ingressClassName, sinon l'annotation
// kubernetes.io/ingress.class, sinon l'IngressClass par défaut, sinon « default ».
func IngressGate(i *networkingv1.Ingress, classes []*networkingv1.IngressClass) string {
	if c := i.Spec.IngressClassName; c != nil && *c != "" {
		return *c
	}
	if a := i.Annotations[ingressClassAnnotation]; a != "" {
		return a
	}
	var defaults []string
	for _, c := range classes {
		if c.Annotations[defaultClassAnnotation] == "true" {
			defaults = append(defaults, c.Name)
		}
	}
	sort.Strings(defaults)
	if len(defaults) > 0 {
		return defaults[0]
	}
	return "default"
}

// ServiceExists : « ce Service existe-t-il ? ». nil quand on ne peut pas le
// savoir (Services non listables) : le backend est alors considéré présent.
type ServiceExists func(namespace, name string) bool

func backendState(exists ServiceExists, ns, name string) string {
	if exists == nil || exists(ns, name) {
		return model.BackendOK
	}
	return model.BackendMissing
}

func ConvertIngress(i *networkingv1.Ingress, gate string, exists ServiceExists) model.Route {
	r := model.Route{Source: "Ingress", Group: "networking.k8s.io", Namespace: i.Namespace, Name: i.Name, Gate: gate, Rules: []model.Rule{}}
	add := func(host, path string, b *networkingv1.IngressBackend) {
		if b == nil || b.Service == nil {
			return // backend « resource » : hors jalon
		}
		port := b.Service.Port.Name
		if port == "" && b.Service.Port.Number != 0 {
			port = strconv.Itoa(int(b.Service.Port.Number))
		}
		r.Rules = append(r.Rules, model.Rule{Host: host, Path: path, Backend: model.Backend{
			Namespace: i.Namespace, Service: b.Service.Name, Port: port, Kind: "Service", State: backendState(exists, i.Namespace, b.Service.Name)}})
	}
	add("", "", i.Spec.DefaultBackend)
	for _, rule := range i.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, p := range rule.HTTP.Paths {
			b := p.Backend
			add(rule.Host, p.Path, &b)
		}
	}
	for _, lb := range i.Status.LoadBalancer.Ingress {
		if lb.IP != "" {
			r.Addresses = append(r.Addresses, lb.IP)
		} else if lb.Hostname != "" {
			r.Addresses = append(r.Addresses, lb.Hostname)
		}
	}
	return r
}

var (
	hostRe = regexp.MustCompile("Host\\(`([^`]+)`")
	pathRe = regexp.MustCompile("(?:PathPrefix|Path)\\(`([^`]+)`")
)

// parseMatch extrait le premier Host() et le premier Path()/PathPrefix() d'une règle Traefik.
func parseMatch(match string) (host, path string) {
	if m := hostRe.FindStringSubmatch(match); m != nil {
		host = m[1]
	}
	if m := pathRe.FindStringSubmatch(match); m != nil {
		path = m[1]
	}
	return host, path
}

// ConvertIngressRoute lit une IngressRoute Traefik (traefik.io ou traefik.containo.us).
func ConvertIngressRoute(u *unstructured.Unstructured, exists ServiceExists) model.Route {
	gate := u.GetAnnotations()[ingressClassAnnotation]
	if gate == "" {
		gate = traefikGate
	}
	r := model.Route{Source: "IngressRoute", Group: u.GroupVersionKind().Group, Namespace: u.GetNamespace(), Name: u.GetName(), Gate: gate, Rules: []model.Rule{}}
	routes, _, _ := unstructured.NestedSlice(u.Object, "spec", "routes")
	for _, ro := range routes {
		rm, ok := ro.(map[string]any)
		if !ok {
			continue
		}
		match, _ := rm["match"].(string)
		host, path := parseMatch(match)
		services, _ := rm["services"].([]any)
		for _, so := range services {
			sm, ok := so.(map[string]any)
			if !ok {
				continue
			}
			name, _ := sm["name"].(string)
			ns, _ := sm["namespace"].(string)
			if ns == "" {
				ns = r.Namespace
			}
			kind, _ := sm["kind"].(string)
			if kind == "" {
				kind = "Service"
			}
			port := ""
			if p, ok := sm["port"]; ok && p != nil {
				port = fmt.Sprint(p)
			}
			state := model.BackendIndirect
			if kind == "Service" {
				state = backendState(exists, ns, name)
			}
			r.Rules = append(r.Rules, model.Rule{Host: host, Path: path, Match: match,
				Backend: model.Backend{Namespace: ns, Service: name, Port: port, Kind: kind, State: state}})
		}
	}
	return r
}

// routeBackends : Services visés (« ns/name »), pour l'index des routes par Service.
func routeBackends(r model.Route) []string {
	var out []string
	seen := map[string]bool{}
	for _, rule := range r.Rules {
		b := rule.Backend
		if k := b.Namespace + "/" + b.Service; b.Kind == "Service" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/kube/ -run 'TestIngressGate|TestConvertIngress'`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/kube/network.go internal/kube/network_test.go
git commit -m "feat(kube): Ingress et IngressRoute Traefik ramenées à des routes vers une porte"
```

### Task 5 : conversion des PVC et volumes montés par les pods

**Files:**
- Modify: `internal/kube/network.go`, `internal/kube/source.go` (`transform`)
- Test: `internal/kube/network_test.go`, `internal/kube/source_test.go`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `internal/kube/network_test.go` (import `"k8s.io/apimachinery/pkg/api/resource"`) :

```go
func TestConvertPVC(t *testing.T) {
	class := "standard-rwo"
	pvc := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "data-0", Namespace: "prod"},
		Spec: corev1.PersistentVolumeClaimSpec{StorageClassName: &class, VolumeName: "pvc-123",
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}}},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound, Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("20Gi")}}}
	v := ConvertPVC(pvc, []string{"u1"})
	if v.StorageClass != "standard-rwo" || v.Requested != 10<<30 || v.Capacity != 20<<30 || v.Phase != "Bound" ||
		v.AccessModes[0] != "ReadWriteOnce" || v.VolumeName != "pvc-123" || v.Pods[0] != "u1" {
		t.Errorf("volume = %+v", v)
	}
	empty := ConvertPVC(&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "x"}}, nil)
	if empty.Phase != "Pending" || empty.Pods == nil || empty.AccessModes == nil {
		t.Errorf("PVC neuf = %+v", empty)
	}
}

func TestPodClaims(t *testing.T) {
	p := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web-0"}, Spec: corev1.PodSpec{Volumes: []corev1.Volume{
		{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-web-0"}}},
		{Name: "scratch", VolumeSource: corev1.VolumeSource{Ephemeral: &corev1.EphemeralVolumeSource{}}},
		{Name: "cfg", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{}}},
	}}}
	got := PodClaims(p)
	if len(got) != 2 || got[0] != "data-web-0" || got[1] != "web-0-scratch" {
		t.Errorf("PodClaims = %v", got)
	}
}
```

Ajouter à `TestTransformStripsUnusedFields` dans `internal/kube/source_test.go`, avant le test du Deployment :

```go
	withClaims, _ := transform(&corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{
		{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "c", ReadOnly: true}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{Ephemeral: &corev1.EphemeralVolumeSource{VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{}}}},
		{Name: "secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "s"}}},
	}}})
	vs := withClaims.(*corev1.Pod).Spec.Volumes
	if len(vs) != 2 || vs[0].PersistentVolumeClaim.ClaimName != "c" || vs[0].PersistentVolumeClaim.ReadOnly || vs[1].Ephemeral.VolumeClaimTemplate != nil {
		t.Errorf("volumes gardés = %+v", vs)
	}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'TestConvertPVC|TestPodClaims|TestTransform'`
Expected: FAIL, `undefined: ConvertPVC`.

- [ ] **Step 3 : implémentation**

Ajouter à `internal/kube/network.go` :

```go
// ConvertPVC réduit un PersistentVolumeClaim ; pods : UID des pods qui le montent.
func ConvertPVC(p *corev1.PersistentVolumeClaim, pods []string) model.Volume {
	v := model.Volume{Namespace: p.Namespace, Name: p.Name, Phase: string(p.Status.Phase), VolumeName: p.Spec.VolumeName,
		AccessModes: []string{}, Pods: pods}
	if v.Phase == "" {
		v.Phase = string(corev1.ClaimPending)
	}
	if p.Spec.StorageClassName != nil {
		v.StorageClass = *p.Spec.StorageClassName
	}
	if q, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		v.Requested = q.Value()
	}
	if q, ok := p.Status.Capacity[corev1.ResourceStorage]; ok {
		v.Capacity = q.Value()
	}
	for _, m := range p.Spec.AccessModes {
		v.AccessModes = append(v.AccessModes, string(m))
	}
	if v.Pods == nil {
		v.Pods = []string{}
	}
	return v
}

// PodClaims : PVC montés par le pod. Un volume éphémère crée le PVC « <pod>-<volume> ».
func PodClaims(p *corev1.Pod) []string {
	var out []string
	for _, v := range p.Spec.Volumes {
		switch {
		case v.PersistentVolumeClaim != nil:
			out = append(out, v.PersistentVolumeClaim.ClaimName)
		case v.Ephemeral != nil:
			out = append(out, p.Name+"-"+v.Name)
		}
	}
	return out
}

// claimVolumes : seuls volumes gardés en cache, sans leurs détails.
func claimVolumes(vs []corev1.Volume) []corev1.Volume {
	var out []corev1.Volume
	for _, v := range vs {
		switch {
		case v.PersistentVolumeClaim != nil:
			out = append(out, corev1.Volume{Name: v.Name, VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: v.PersistentVolumeClaim.ClaimName}}})
		case v.Ephemeral != nil:
			out = append(out, corev1.Volume{Name: v.Name, VolumeSource: corev1.VolumeSource{Ephemeral: &corev1.EphemeralVolumeSource{}}})
		}
	}
	return out
}
```

Dans `internal/kube/source.go`, fonction `transform`, remplacer `o.Spec.Volumes = nil` par :

```go
		o.Spec.Volumes = claimVolumes(o.Spec.Volumes) // PVC montés (jalon 8), rien d'autre
```

et adapter le commentaire de `transform` : « … ni des volumes (sauf les PVC montés), variables d'environnement ou templates ».

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/kube/`
Expected: PASS (le pod de `TestTransformStripsUnusedFields` n'a qu'un volume sans source : `Volumes` reste nil).

- [ ] **Step 5 : commit**

```bash
git add internal/kube
git commit -m "feat(kube): PVC et volumes montés par les pods (gardés seuls en cache)"
```

### Task 6 : informers optionnels, index et déclenchements

**Files:**
- Create: `internal/kube/source_net.go`
- Modify: `internal/kube/source.go` (struct `Source`, `NewSource`, `Run`, `onPod`, `markAll`, `build`)
- Test: `internal/kube/source_net_test.go`, `internal/kube/source_test.go` (`startSource`)

- [ ] **Step 1 : rendre le démarrage des tests paramétrable**

Dans `internal/kube/source_test.go`, remplacer `startSource` par :

```go
func startSource(t *testing.T, objs ...runtime.Object) (*fake.Clientset, *sink) {
	t.Helper()
	return startSourceWith(t, fake.NewClientset(objs...), Options{})
}

// startSourceWith démarre une source sur un client préparé (réacteurs, découverte).
func startSourceWith(t *testing.T, client *fake.Clientset, opts Options) (*fake.Clientset, *sink) {
	t.Helper()
	sk := newSink()
	opts.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	opts.ReconcileInterval = 10 * time.Millisecond
	src := NewSource(client, sk, opts)
	lastSource = src
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = src.Run(ctx) }()
	eventually(t, "source prête", func() bool { sk.mu.Lock(); defer sk.mu.Unlock(); return sk.ready })
	return client, sk
}
```

Run: `go test ./internal/kube/`
Expected: PASS (refactoring sans changement de comportement).

- [ ] **Step 2 : tests qui échouent**

Créer `internal/kube/source_net_test.go` :

```go
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
```

Run: `go test ./internal/kube/ -run 'TestNetworkObjects|TestForbiddenType'`
Expected: FAIL (aucun service publié).

- [ ] **Step 3 : implémentation**

Dans `internal/kube/source.go` :

1. Imports supplémentaires : `"k8s.io/client-go/dynamic"`, `"k8s.io/client-go/dynamic/dynamicinformer"`, `networkinglisters "k8s.io/client-go/listers/networking/v1"`.
2. `Options` gagne :

```go
	// Dynamic lit les IngressRoute Traefik (CRD) ; nil : pas d'IngressRoute.
	Dynamic dynamic.Interface
```

3. `Source` gagne, après `events` :

```go
	client kubernetes.Interface

	// Jalon 8 : types optionnels, nil quand le ServiceAccount ne peut pas les lister.
	services   corelisters.ServiceLister
	slices     cache.Indexer
	pvcs       corelisters.PersistentVolumeClaimLister
	ingresses  networkinglisters.IngressLister
	ingressIdx cache.Indexer
	classes    networkinglisters.IngressClassLister
	traefik    []traefikInformer
	dynFactory dynamicinformer.DynamicSharedInformerFactory
```

4. Dans `NewSource`, construire `s` avec `client: client`, et ajouter à l'indexeur des pods :

```go
		indexByClaim: func(obj any) ([]string, error) {
			p := obj.(*corev1.Pod)
			var out []string
			for _, c := range PodClaims(p) {
				out = append(out, p.Namespace+"/"+c)
			}
			return out, nil
		},
```

5. `Run` : appeler `s.startNetwork(ctx)` avant `s.factory.Start(ctx.Done())`, puis démarrer et arrêter la fabrique dynamique :

```go
	s.startNetwork(ctx)
	s.factory.Start(ctx.Done())
	if s.dynFactory != nil {
		s.dynFactory.Start(ctx.Done())
	}
```

```go
		case <-ctx.Done():
			s.factory.Shutdown()
			if s.dynFactory != nil {
				s.dynFactory.Shutdown()
			}
			return nil
```

6. `onPod` : à la fin, marquer les PVC montés :

```go
	if s.pvcs != nil {
		for _, c := range PodClaims(p) {
			s.mark(ref{stream.KindVolume, p.Namespace + "/" + c})
		}
	}
```

7. `markAll` : appeler `s.markNetwork()` juste avant `return nil`.
8. `build` : ajouter les cas avant le `return` final :

```go
	case stream.KindService:
		return s.buildService(r.id)
	case stream.KindRoute:
		return s.buildRoute(r.id)
	case stream.KindVolume:
		return s.buildVolume(r.id)
```

Créer `internal/kube/source_net.go` :

```go
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
	if s.probe(ctx, "endpointslices", func(ctx context.Context) error { _, err := c.DiscoveryV1().EndpointSlices("").List(ctx, probeOpts); return err }) {
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
	if s.probe(ctx, "ingressclasses", func(ctx context.Context) error { _, err := c.NetworkingV1().IngressClasses().List(ctx, probeOpts); return err }) {
		inf := f.Networking().V1().IngressClasses()
		s.classes = inf.Lister()
		s.watch(inf.Informer(), s.onIngressClass)
	}
	if s.probe(ctx, "ingresses", func(ctx context.Context) error { _, err := c.NetworkingV1().Ingresses("").List(ctx, probeOpts); return err }) {
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
```

Pour que ce fichier compile avant la tâche 7, créer `internal/kube/traefik.go` minimal :

```go
package kube

import (
	"context"

	"k8s.io/client-go/tools/cache"
)

// traefikInformer : cache des IngressRoute d'un groupe Traefik.
type traefikInformer struct {
	group  string
	lister cache.GenericLister
	index  cache.Indexer
}

func (s *Source) startTraefik(context.Context) {}
```

- [ ] **Step 4 : vérifier**

Run: `go test -race ./internal/kube/`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/kube
git commit -m "feat(kube): Services, EndpointSlices, Ingress et PVC dans la boucle des objets sales"
```

### Task 7 : IngressRoute Traefik par informer dynamique

**Files:**
- Modify: `internal/kube/traefik.go`, `cmd/atlas/main.go` (`startClusterSource`)
- Test: `internal/kube/traefik_test.go`

- [ ] **Step 1 : test qui échoue**

Créer `internal/kube/traefik_test.go` :

```go
package kube

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

var irGVR = schema.GroupVersionResource{Group: "traefik.io", Version: "v1alpha1", Resource: "ingressroutes"}

func TestIngressRoutesFromTraefik(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	client.Resources = []*metav1.APIResourceList{{GroupVersion: "traefik.io/v1alpha1",
		APIResources: []metav1.APIResource{{Name: "ingressroutes", Kind: "IngressRoute", Namespaced: true}}}}
	ir := ingressRoute("traefik.io", "prod", "admin", []any{map[string]any{"match": "Host(`admin.example.com`)",
		"services": []any{map[string]any{"name": "api", "port": int64(80)}, map[string]any{"name": "ghost", "port": int64(80)}}}})
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{irGVR: "IngressRouteList"}, ir)

	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	o, ok := sk.get(stream.KindRoute, "IngressRoute/prod/admin")
	if !ok {
		t.Fatal("IngressRoute absente")
	}
	r := o.(model.Route)
	if r.Gate != "traefik" || r.Group != "traefik.io" || r.Rules[0].Host != "admin.example.com" ||
		r.Rules[0].Backend.State != model.BackendOK || r.Rules[1].Backend.State != model.BackendMissing {
		t.Fatalf("route = %+v", r)
	}

	if err := dyn.Resource(irGVR).Namespace("prod").Delete(context.Background(), "admin", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute supprimée", func() bool { _, ok := sk.get(stream.KindRoute, "IngressRoute/prod/admin"); return !ok })
}

func TestNoTraefikWithoutCRD(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{irGVR: "IngressRouteList"})
	_, sk := startSourceWith(t, fake.NewClientset(netFixtures()...), Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, "Ingress/prod/storefront"); !ok {
		t.Error("la source doit fonctionner sans CRD Traefik")
	}
}
```

Run: `go test ./internal/kube/ -run 'Traefik'`
Expected: FAIL (`IngressRoute absente`).

- [ ] **Step 2 : implémentation**

Remplacer `internal/kube/traefik.go` :

```go
package kube

import (
	"context"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Groupes des IngressRoute Traefik : v3 (traefik.io), puis l'ancien groupe v2.
var traefikGroups = []string{"traefik.io", "traefik.containo.us"}

// traefikInformer : cache des IngressRoute d'un groupe Traefik.
type traefikInformer struct {
	group  string
	lister cache.GenericLister
	index  cache.Indexer
}

// traefikGroupsServed : groupes dont l'API server sert les IngressRoute.
// La découverte a lieu au démarrage : une CRD installée ensuite est prise en
// compte au prochain redémarrage.
func traefikGroupsServed(d discovery.DiscoveryInterface) []string {
	var out []string
	for _, g := range traefikGroups {
		rl, err := d.ServerResourcesForGroupVersion(g + "/v1alpha1")
		if err != nil {
			continue
		}
		for _, r := range rl.APIResources {
			if r.Name == "ingressroutes" {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

func (s *Source) startTraefik(ctx context.Context) {
	dyn := s.opts.Dynamic
	if dyn == nil {
		return
	}
	for _, g := range traefikGroupsServed(s.client.Discovery()) {
		gvr := schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "ingressroutes"}
		if !s.probe(ctx, "ingressroutes."+g, func(ctx context.Context) error {
			_, err := dyn.Resource(gvr).List(ctx, probeOpts)
			return err
		}) {
			continue
		}
		if s.dynFactory == nil {
			s.dynFactory = dynamicinformer.NewDynamicSharedInformerFactory(dyn, 0)
		}
		inf := s.dynFactory.ForResource(gvr)
		_ = inf.Informer().SetTransform(transform)
		_ = inf.Informer().AddIndexers(cache.Indexers{indexByBackend: func(o any) ([]string, error) {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return nil, nil
			}
			return routeBackends(ConvertIngressRoute(u, nil)), nil
		}})
		s.traefik = append(s.traefik, traefikInformer{group: g, lister: inf.Lister(), index: inf.Informer().GetIndexer()})
		s.watch(inf.Informer(), s.onIngressRoute)
	}
}

```

Dans `cmd/atlas/main.go`, `startClusterSource` :

```go
	dyn, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	src := kube.NewSource(client, hub, kube.Options{Node: kube.NodeOptions{PoolLabel: f.poolLabel}, Log: log, Dynamic: dyn})
```

- [ ] **Step 3 : vérifier**

Run: `gofmt -l . && go vet ./... && go test -race ./internal/kube/ ./cmd/...`
Expected: aucune sortie de `gofmt`, PASS.

- [ ] **Step 4 : commit**

```bash
git add internal/kube cmd/atlas
git commit -m "feat(kube): IngressRoute Traefik (traefik.io et traefik.containo.us) par informer dynamique"
```

### Task 8 : droits du flux pour les trois kinds

**Files:**
- Modify: `internal/access/filter.go` (`attributes`)
- Test: `internal/access/access_test.go`

- [ ] **Step 1 : tests qui échouent**

Dans `TestStreamFilter`, ajouter aux `cases` :

```go
		{stream.KindService, model.Service{Namespace: "production"}, true},
		{stream.KindService, model.Service{Namespace: "kube-system"}, false},
		{stream.KindRoute, model.Route{Source: "Ingress", Namespace: "production"}, true},
		{stream.KindRoute, model.Route{Source: "IngressRoute", Group: "traefik.io", Namespace: "kube-system"}, false},
		{stream.KindVolume, model.Volume{Namespace: "production"}, true},
		{stream.KindVolume, model.Volume{Namespace: "kube-system"}, false},
```

Et ajouter :

```go
func TestNetworkAttributes(t *testing.T) {
	cases := []struct {
		obj  any
		want Attributes
	}{
		{model.Service{Namespace: "a"}, Attributes{Verb: "list", Resource: "services", Namespace: "a"}},
		{model.Route{Source: "Ingress", Group: "networking.k8s.io", Namespace: "a"}, Attributes{Verb: "list", Group: "networking.k8s.io", Resource: "ingresses", Namespace: "a"}},
		{model.Route{Source: "IngressRoute", Group: "traefik.containo.us", Namespace: "a"}, Attributes{Verb: "list", Group: "traefik.containo.us", Resource: "ingressroutes", Namespace: "a"}},
		{model.Volume{Namespace: "a"}, Attributes{Verb: "list", Resource: "persistentvolumeclaims", Namespace: "a"}},
	}
	for _, c := range cases {
		if got, ok := attributes("", c.obj); !ok || got != c.want {
			t.Errorf("%+v : %+v, attendu %+v", c.obj, got, c.want)
		}
	}
}
```

Run: `go test ./internal/access/`
Expected: FAIL.

- [ ] **Step 2 : implémentation**

Dans `attributes` (`internal/access/filter.go`), ajouter avant la fermeture du `switch` :

```go
	case model.Service:
		return Attributes{Verb: "list", Resource: "services", Namespace: o.Namespace}, true
	case model.Route:
		if o.Source == "Ingress" {
			return Attributes{Verb: "list", Group: "networking.k8s.io", Resource: "ingresses", Namespace: o.Namespace}, true
		}
		return Attributes{Verb: "list", Group: o.Group, Resource: "ingressroutes", Namespace: o.Namespace}, true
	case model.Volume:
		return Attributes{Verb: "list", Resource: "persistentvolumeclaims", Namespace: o.Namespace}, true
```

Mettre à jour le commentaire de `attributes` : « … Services, routes et PVC suivent le droit de les lister dans leur namespace ».

- [ ] **Step 3 : vérifier**

Run: `go test -race ./internal/access/`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add internal/access
git commit -m "feat(access): Services, routes et PVC filtrés par le droit de les lister"
```

### Task 9 : YAML et événements des nouveaux objets dans l'inspecteur

**Files:**
- Modify: `internal/inspect/inspect.go`, `internal/kube/inspector.go`, `internal/kube/source.go` (`PodEvents` → `ObjectEvents`), `internal/server/server.go`, `internal/demo/inspect.go` (signature), `internal/logs/logs_test.go`
- Test: `internal/kube/inspector_test.go`, `internal/server/inspect_test.go`, `internal/demo/inspect_test.go`

- [ ] **Step 1 : adapter les tests à la nouvelle signature**

- `internal/kube/inspector_test.go` : `func (s staticEvents) ObjectEvents(string, string, string) []model.Event { return s }` ; les appels deviennent `.Events(context.Background(), bob, "Pod", "prod", "api")` ; dans `TestSourceIndexesPodEvents`, `src.ObjectEvents("prod", "Pod", "api")`.
- `internal/logs/logs_test.go` : `func (f *fakeBackend) Events(context.Context, access.User, string, string, string) ([]model.Event, error)`.
- `internal/demo/inspect_test.go` : `s.Events(context.Background(), anyone, "Pod", "production", crashy.Name)` (et de même pour `pending.Name`).

Ajouter à `internal/kube/inspector_test.go` :

```go
func TestSourceIndexesServiceEvents(t *testing.T) {
	ev := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Name: "e1", Namespace: "prod"}, Reason: "SyncLoadBalancerFailed", Type: "Warning",
		InvolvedObject: corev1.ObjectReference{Kind: "Service", Namespace: "prod", Name: "api"}}
	startSource(t, append(fixtures(), ev)...)
	eventually(t, "événement du Service indexé", func() bool { return len(lastSource.ObjectEvents("prod", "Service", "api")) == 1 })
}
```

Ajouter à la fin de `TestInspectorRoutes` (`internal/server/inspect_test.go`) :

```go
	if rec := get(h, "/api/namespaces/production/secrets/x/events"); rec.Code != 400 {
		t.Errorf("événements d'un Secret = %d, attendu 400", rec.Code)
	}
	if rec := get(h, "/api/namespaces/production/services/api-gateway/events"); rec.Code != 200 {
		t.Errorf("événements d'un Service = %d", rec.Code)
	}
```

Ajouter à `internal/inspect/inspect_test.go` (créer le fichier s'il n'existe pas, `package inspect`, import `testing`) :

```go
func TestNetworkKinds(t *testing.T) {
	for _, c := range []struct{ group, version, kind string }{
		{"", "v1", "Service"}, {"", "v1", "PersistentVolumeClaim"}, {"networking.k8s.io", "v1", "Ingress"},
		{"traefik.io", "v1alpha1", "IngressRoute"}, {"traefik.containo.us", "v1alpha1", "IngressRoute"},
	} {
		if _, err := Lookup(c.group, c.version, c.kind); err != nil {
			t.Errorf("%+v : %v", c, err)
		}
	}
	if k, ok := KindForResource("persistentvolumeclaims"); !ok || k != "PersistentVolumeClaim" {
		t.Errorf("KindForResource = %s %v", k, ok)
	}
	if _, ok := KindForResource("secrets"); ok {
		t.Error("secrets ne doit pas être inspectable")
	}
}
```

Run: `go test ./internal/...`
Expected: FAIL de compilation (`ObjectEvents`, `KindForResource` indéfinis).

- [ ] **Step 2 : implémentation**

`internal/inspect/inspect.go` :

```go
	// Events renvoie les événements d'un objet (kind : Pod, Service…), du plus récent au plus ancien.
	Events(ctx context.Context, u access.User, kind, namespace, name string) ([]model.Event, error)
```

```go
var Kinds = []Kind{
	{"", "v1", "Pod", "pods"},
	{"apps", "v1", "Deployment", "deployments"},
	{"apps", "v1", "ReplicaSet", "replicasets"},
	{"apps", "v1", "StatefulSet", "statefulsets"},
	{"apps", "v1", "DaemonSet", "daemonsets"},
	{"batch", "v1", "Job", "jobs"},
	{"", "v1", "Service", "services"},
	{"", "v1", "PersistentVolumeClaim", "persistentvolumeclaims"},
	{"networking.k8s.io", "v1", "Ingress", "ingresses"},
	{"traefik.io", "v1alpha1", "IngressRoute", "ingressroutes"},
	{"traefik.containo.us", "v1alpha1", "IngressRoute", "ingressroutes"},
}
```

```go
// KindForResource : kind désigné par la ressource d'une URL (« services » → Service).
func KindForResource(resource string) (string, bool) {
	for _, k := range Kinds {
		if k.Resource == resource {
			return k.Kind, true
		}
	}
	return "", false
}
```

`internal/kube/source.go` : renommer `PodEvents` en

```go
// ObjectEvents renvoie les événements d'un objet depuis le cache partagé.
func (s *Source) ObjectEvents(namespace, kind, name string) []model.Event {
	objs, _ := s.events.ByIndex(indexByInvolved, namespace+"/"+kind+"/"+name)
	out := make([]model.Event, 0, len(objs))
	for _, o := range objs {
		out = append(out, ConvertEvent(o.(*corev1.Event)))
	}
	return out
}
```

`internal/kube/inspector.go` :

```go
// EventSource fournit les événements d'un objet depuis le cache partagé.
type EventSource interface {
	ObjectEvents(namespace, kind, name string) []model.Event
}
```

```go
func (in *Inspector) Events(ctx context.Context, u access.User, kind, ns, name string) ([]model.Event, error) {
	if in.authorize != nil && !in.authorize(ctx, u, access.Attributes{Verb: "list", Resource: "events", Namespace: ns}) {
		return nil, apierrors.NewForbidden(schema.GroupResource{Resource: "events"}, "",
			fmt.Errorf("User %q cannot list resource \"events\" in API group \"\" in the namespace %q", u.Name, ns))
	}
	evs := in.events.ObjectEvents(ns, kind, name)
	inspect.SortEvents(evs)
	return evs, nil
}
```

`internal/server/server.go` : remplacer la route des événements des pods par

```go
			r.Get("/namespaces/{ns}/{resource}/{name}/events", s.events)
```

et le handler :

```go
// events : /api/namespaces/{ns}/{resource}/{name}/events (pods, services, persistentvolumeclaims, ingresses, ingressroutes…).
func (s *server) events(w http.ResponseWriter, r *http.Request) {
	kind, ok := inspect.KindForResource(chi.URLParam(r, "resource"))
	if !ok {
		apiError(w, inspect.ErrUnsupportedKind)
		return
	}
	evs, err := s.cfg.Inspect.Events(r.Context(), s.user(r), kind, chi.URLParam(r, "ns"), chi.URLParam(r, "name"))
	if err != nil {
		apiError(w, err)
		return
	}
	writeJSON(w, map[string]any{"events": evs})
}
```

`internal/demo/inspect.go` : nouvelle signature, comportement des pods inchangé (les autres kinds arrivent à la tâche 10) :

```go
func (s *Sim) Events(_ context.Context, _ access.User, kind, ns, name string) ([]model.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind != "Pod" {
		return s.netEvents(kind, ns, name), nil
	}
	p := s.findPod(ns, name)
	if p == nil {
		return []model.Event{}, nil
	}
	evs := append([]model.Event(nil), p.events...)
	inspect.SortEvents(evs)
	return evs, nil
}

// netEvents : complété à la tâche 10 (PVC en attente).
func (s *Sim) netEvents(kind, ns, name string) []model.Event { return []model.Event{} }
```

- [ ] **Step 3 : vérifier**

Run: `go vet ./... && go test -race ./internal/...`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add internal
git commit -m "feat(inspect): YAML et événements des Services, Ingress, IngressRoute et PVC"
```

### Task 10 : réseau et stockage simulés

**Files:**
- Create: `internal/demo/network.go`
- Modify: `internal/demo/sim.go` (`Sim`, `New`, `Step`), `internal/demo/scale.go` (`catalog`, `catalogFor`), `internal/demo/inspect.go` (`YAML`, `netEvents`)
- Test: `internal/demo/sim_test.go`, `internal/demo/inspect_test.go`, `internal/server/inspect_test.go`

- [ ] **Step 1 : tests qui échouent**

Dans `internal/demo/sim_test.go`, le `fakeSink` garde aussi le réseau et le stockage :

```go
	net        map[string]any // « kind|clé » : Services, routes, volumes
```

`newSink` initialise `net: map[string]any{}` ; `Upsert` gagne

```go
	case model.Service, model.Route, model.Volume:
		f.net[string(kind)+"|"+key] = o
```

et `Delete`, en tête :

```go
	if kind == stream.KindService || kind == stream.KindRoute || kind == stream.KindVolume {
		delete(f.net, string(kind)+"|"+key)
		return
	}
```

Ajouter :

```go
func TestDemoNetworkAndStorage(t *testing.T) {
	s, sink := start(5)
	api := sink.net["service|production/api-gateway"].(model.Service)
	if api.Health != model.HealthOK || len(api.Endpoints) != 3 || api.Type != "LoadBalancer" || len(api.LoadBalancer) != 1 {
		t.Errorf("api-gateway = %+v", api)
	}
	if h := sink.net["service|staging/checkout-preview"].(model.Service).Health; h != model.HealthDown {
		t.Errorf("checkout-preview (image introuvable) = %s, attendu down", h)
	}
	if h := sink.net["service|production/stripe-api"].(model.Service).Health; h != model.HealthExternal {
		t.Errorf("stripe-api = %s", h)
	}
	if !sink.net["service|production/postgres-payments"].(model.Service).Headless {
		t.Error("postgres-payments doit être headless")
	}
	shop := sink.net["route|Ingress/production/storefront"].(model.Route)
	if shop.Gate != "nginx" || shop.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("storefront = %+v", shop)
	}
	admin := sink.net["route|IngressRoute/production/admin"].(model.Route)
	if admin.Gate != "traefik" || admin.Group != "traefik.io" || admin.Rules[0].Backend.State != model.BackendMissing {
		t.Errorf("admin = %+v", admin)
	}
	pg := sink.net["volume|production/data-postgres-payments-0"].(model.Volume)
	if pg.Phase != "Bound" || len(pg.Pods) != 1 || pg.StorageClass != "standard-rwo" {
		t.Errorf("data-postgres-payments-0 = %+v", pg)
	}
	if v := sink.net["volume|staging/uploads-preview"].(model.Volume); v.Phase != "Pending" || len(v.Pods) != 0 {
		t.Errorf("uploads-preview = %+v", v)
	}

	// Les endpoints suivent les pods.
	_ = s.Scale(context.Background(), anyone, "production", "Deployment", "api-gateway", 1)
	advance(s, t0, 5*time.Second)
	if n := len(sink.net["service|production/api-gateway"].(model.Service).Endpoints); n != 1 {
		t.Errorf("après scale à 1 : %d endpoints", n)
	}
}

func TestScaledCatalogHasNetwork(t *testing.T) {
	c := catalogFor(Scale{Nodes: 10, PodsPerNode: 30}) // 10 équipes
	if len(c.services) != len(services)+40 || len(c.volumes) != len(volumes)+20 {
		t.Errorf("services = %d, volumes = %d", len(c.services), len(c.volumes))
	}
	traefik := 0
	for _, r := range c.routes {
		if r.Source == "IngressRoute" {
			traefik++
		}
	}
	if traefik != 3+3 { // grafana, admin, argocd, puis les équipes 3, 6 et 9
		t.Errorf("IngressRoute = %d", traefik)
	}
}
```

(`anyone` est défini dans `inspect_test.go`, même package.)

Ajouter à `internal/demo/inspect_test.go` :

```go
func TestDemoNetworkInspector(t *testing.T) {
	s, _ := start(5)
	ctx := context.Background()
	yamlOf := func(r inspect.Ref) string {
		t.Helper()
		doc, err := s.YAML(ctx, anyone, r)
		if err != nil {
			t.Fatalf("%+v : %v", r, err)
		}
		return doc.YAML
	}
	if y := yamlOf(inspect.Ref{Version: "v1", Kind: "Service", Namespace: "production", Name: "api-gateway"}); !strings.Contains(y, "kind: Service") || !strings.Contains(y, "type: LoadBalancer") {
		t.Errorf("Service :\n%s", y)
	}
	if y := yamlOf(inspect.Ref{Group: "networking.k8s.io", Version: "v1", Kind: "Ingress", Namespace: "production", Name: "storefront"}); !strings.Contains(y, "ingressClassName: nginx") {
		t.Errorf("Ingress :\n%s", y)
	}
	if y := yamlOf(inspect.Ref{Group: "traefik.io", Version: "v1alpha1", Kind: "IngressRoute", Namespace: "monitoring", Name: "grafana"}); !strings.Contains(y, "Host(`grafana.example.com`)") {
		t.Errorf("IngressRoute :\n%s", y)
	}
	if y := yamlOf(inspect.Ref{Version: "v1", Kind: "PersistentVolumeClaim", Namespace: "staging", Name: "uploads-preview"}); !strings.Contains(y, "phase: Pending") {
		t.Errorf("PVC :\n%s", y)
	}
	if _, err := s.YAML(ctx, anyone, inspect.Ref{Version: "v1", Kind: "Service", Namespace: "production", Name: "absent"}); err == nil {
		t.Error("Service absent : erreur attendue")
	}
	evs, _ := s.Events(ctx, anyone, "PersistentVolumeClaim", "staging", "uploads-preview")
	if len(evs) != 1 || evs[0].Reason != "WaitForFirstConsumer" {
		t.Errorf("événements du PVC en attente = %+v", evs)
	}
}
```

Ajouter à la fin de `TestInspectorRoutes` (`internal/server/inspect_test.go`) :

```go
	if rec := get(h, "/api/yaml/traefik.io/v1alpha1/IngressRoute/monitoring/grafana"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "IngressRoute") {
		t.Errorf("yaml d'une IngressRoute = %d", rec.Code)
	}
```

Run: `go test ./internal/demo/ ./internal/server/`
Expected: FAIL (`sink.net[...]` nil, `undefined: services`).

- [ ] **Step 2 : catalogue**

Créer `internal/demo/network.go` :

```go
package demo

import (
	"fmt"
	"hash/fnv"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/no-inspi/atlas-k8s/internal/inspect"
	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// Réseau et stockage simulés : Services devant les workloads du catalogue,
// routes (Ingress nginx et IngressRoute traefik) et PVC. Recalculés à chaque
// pas depuis les pods vivants ; seuls les changements sont publiés.

type serviceDef struct {
	NS, Name string
	Workload string // workload ciblé, dans le même namespace ; vide pour un ExternalName
	Type     string // ClusterIP si vide
	Port     int32
	Headless bool
	External string
}

type volumeDef struct {
	NS, Name, Class string
	Size            int64
	Workload        string
	Ordinal         int // replica qui le monte ; -1 : tous
	Pending         bool
}

var services = []serviceDef{
	{NS: "production", Name: "api-gateway", Workload: "api-gateway", Type: "LoadBalancer", Port: 80},
	{NS: "production", Name: "orders-service", Workload: "orders-service", Port: 8080},
	{NS: "production", Name: "payment-worker", Workload: "payment-worker", Port: 8080},
	{NS: "production", Name: "frontend", Workload: "frontend", Port: 80},
	{NS: "production", Name: "ml-inference", Workload: "ml-inference", Port: 8080},
	{NS: "production", Name: "postgres-payments", Workload: "postgres-payments", Port: 5432, Headless: true},
	{NS: "production", Name: "stripe-api", Type: "ExternalName", External: "api.stripe.com"},
	{NS: "staging", Name: "api-gateway", Workload: "api-gateway", Port: 80},
	{NS: "staging", Name: "orders-service", Workload: "orders-service", Port: 8080},
	{NS: "staging", Name: "checkout-preview", Workload: "checkout-preview", Port: 80},
	{NS: "monitoring", Name: "prometheus", Workload: "prometheus", Port: 9090},
	{NS: "monitoring", Name: "grafana", Workload: "grafana", Port: 3000},
	{NS: "argocd", Name: "argocd-server", Workload: "argocd-server", Port: 443},
	{NS: "kube-system", Name: "kube-dns", Workload: "coredns", Port: 53},
}

func rule(host, path, ns, svc, port string) model.Rule {
	return model.Rule{Host: host, Path: path, Backend: model.Backend{Namespace: ns, Service: svc, Port: port, Kind: "Service"}}
}

func traefikRule(host, path, ns, svc, port string) model.Rule {
	r := rule(host, path, ns, svc, port)
	r.Match = "Host(`" + host + "`)"
	if path != "" {
		r.Match += " && PathPrefix(`" + path + "`)"
	}
	return r
}

// Routes de base ; l'état des backends est calculé à la publication.
var baseRoutes = []model.Route{
	{Source: "Ingress", Group: "networking.k8s.io", Namespace: "production", Name: "storefront", Gate: "nginx", Addresses: []string{"34.77.12.8"},
		Rules: []model.Rule{rule("shop.example.com", "/", "production", "frontend", "80"), rule("shop.example.com", "/api", "production", "api-gateway", "80")}},
	{Source: "Ingress", Group: "networking.k8s.io", Namespace: "staging", Name: "storefront", Gate: "nginx", Addresses: []string{"34.77.12.8"},
		Rules: []model.Rule{rule("staging.shop.example.com", "/", "staging", "checkout-preview", "80"), rule("staging.shop.example.com", "/api", "staging", "api-gateway", "80")}},
	{Source: "IngressRoute", Group: "traefik.io", Namespace: "monitoring", Name: "grafana", Gate: "traefik",
		Rules: []model.Rule{traefikRule("grafana.example.com", "", "monitoring", "grafana", "3000")}},
	{Source: "IngressRoute", Group: "traefik.io", Namespace: "production", Name: "admin", Gate: "traefik",
		Rules: []model.Rule{traefikRule("admin.example.com", "/", "production", "admin-panel", "8080")}},
	{Source: "IngressRoute", Group: "traefik.io", Namespace: "argocd", Name: "argocd", Gate: "traefik",
		Rules: []model.Rule{traefikRule("argocd.example.com", "", "argocd", "argocd-server", "443")}},
}

var volumes = []volumeDef{
	{NS: "production", Name: "data-postgres-payments-0", Class: "standard-rwo", Size: 20 * gi, Workload: "postgres-payments", Ordinal: 0},
	{NS: "production", Name: "data-postgres-payments-1", Class: "standard-rwo", Size: 20 * gi, Workload: "postgres-payments", Ordinal: 1},
	{NS: "monitoring", Name: "prometheus-data", Class: "premium-rwo", Size: 50 * gi, Workload: "prometheus", Ordinal: -1},
	{NS: "staging", Name: "uploads-preview", Class: "standard-rwo", Size: 5 * gi, Pending: true},
}

// teamNetwork : Services, routes et PVC d'une équipe simulée (banc de performance).
func teamNetwork(i int, ns string) ([]serviceDef, []model.Route, []volumeDef) {
	svcs := []serviceDef{
		{NS: ns, Name: "api", Workload: "api", Port: 8080},
		{NS: ns, Name: "web", Workload: "web", Port: 80},
		{NS: ns, Name: "gateway", Workload: "gateway", Port: 80},
		{NS: ns, Name: "db", Workload: "db", Port: 5432, Headless: true},
	}
	host := ns + ".example.com"
	routes := []model.Route{{Source: "Ingress", Group: "networking.k8s.io", Namespace: ns, Name: "gateway", Gate: "nginx",
		Rules: []model.Rule{rule(host, "/", ns, "gateway", "80")}}}
	if i%3 == 0 {
		routes = append(routes, model.Route{Source: "IngressRoute", Group: "traefik.io", Namespace: ns, Name: "web", Gate: "traefik",
			Rules: []model.Rule{traefikRule("web."+host, "", ns, "web", "80")}})
	}
	vols := []volumeDef{
		{NS: ns, Name: "data-db-0", Class: "standard-rwo", Size: 10 * gi, Workload: "db", Ordinal: 0},
		{NS: ns, Name: "data-db-1", Class: "standard-rwo", Size: 10 * gi, Workload: "db", Ordinal: 1},
	}
	return svcs, routes, vols
}

func hash32(s string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return h.Sum32()
}
```

Dans `internal/demo/scale.go` :

```go
type catalog struct {
	pools     []poolDef
	workloads []workloadDef
	services  []serviceDef
	routes    []model.Route
	volumes   []volumeDef
}
```

(import `"github.com/no-inspi/atlas-k8s/internal/model"`), puis dans `catalogFor` :

```go
	if sc.Nodes == 0 {
		return catalog{pools: pools, workloads: workloads, services: services, routes: baseRoutes, volumes: volumes}
	}
```

après la construction des pools :

```go
	c.workloads = append(c.workloads, workloads...)
	c.services = append(c.services, services...)
	c.routes = append(c.routes, baseRoutes...)
	c.volumes = append(c.volumes, volumes...)
```

et dans la boucle des équipes, après celle des workloads :

```go
		svcs, routes, vols := teamNetwork(i, ns)
		c.services = append(c.services, svcs...)
		c.routes = append(c.routes, routes...)
		c.volumes = append(c.volumes, vols...)
```

- [ ] **Step 3 : publication**

Ajouter à `internal/demo/network.go` :

```go
// flushNetwork recalcule Services, routes et volumes depuis les pods vivants et
// publie ce qui a changé.
func (s *Sim) flushNetwork() {
	seen := map[string]bool{}
	publish := func(kind stream.Kind, key string, obj any) {
		k := string(kind) + "|" + key
		seen[k] = true
		if prev, ok := s.netLast[k]; ok && reflect.DeepEqual(prev, obj) {
			return
		}
		s.netLast[k] = obj
		s.sink.Upsert(kind, key, obj)
	}
	pods := map[string][]*simPod{} // « ns/workload » → pods placés, hors Terminating
	for _, p := range s.pods {
		if p.pod.NodeName == "" || p.pod.DisplayStatus == "Terminating" {
			continue
		}
		k := p.wl.def.NS + "/" + p.wl.def.Name
		pods[k] = append(pods[k], p)
	}
	exists := map[string]bool{}
	for _, d := range s.catalog.services {
		m := serviceModel(d, pods[d.NS+"/"+d.Workload])
		exists[model.ServiceKey(m)] = true
		publish(stream.KindService, model.ServiceKey(m), m)
	}
	for _, r := range s.catalog.routes {
		m := withBackendStates(r, exists)
		publish(stream.KindRoute, model.RouteKey(m), m)
	}
	for _, d := range s.catalog.volumes {
		m := volumeModel(d, pods[d.NS+"/"+d.Workload])
		publish(stream.KindVolume, model.VolumeKey(m), m)
	}
	for k, prev := range s.netLast {
		if !seen[k] {
			kind, key, _ := strings.Cut(k, "|")
			delete(s.netLast, k)
			s.sink.Delete(stream.Kind(kind), key, prev)
		}
	}
}

func serviceModel(d serviceDef, pods []*simPod) model.Service {
	typ := d.Type
	if typ == "" {
		typ = "ClusterIP"
	}
	m := model.Service{Namespace: d.NS, Name: d.Name, Type: typ, Headless: d.Headless, ExternalName: d.External,
		Ports: []model.ServicePort{}, Endpoints: []model.Endpoint{}}
	if typ != "ExternalName" {
		m.Ports = []model.ServicePort{{Name: "http", Port: d.Port, TargetPort: "http", Protocol: "TCP"}}
		if !d.Headless {
			h := hash32(d.NS + "/" + d.Name)
			m.ClusterIP = fmt.Sprintf("10.96.%d.%d", h>>8&255, h&255)
		}
		if typ == "LoadBalancer" {
			m.LoadBalancer = []string{"34.77.12.8"}
		}
	}
	ready := 0
	for _, p := range pods {
		m.Endpoints = append(m.Endpoints, model.Endpoint{PodUID: p.pod.UID, Ready: p.pod.Ready})
		if p.pod.Ready {
			ready++
		}
	}
	sort.Slice(m.Endpoints, func(i, j int) bool { return m.Endpoints[i].PodUID < m.Endpoints[j].PodUID })
	m.Health = model.ServiceHealth(typ, len(m.Endpoints), ready)
	return m
}

func withBackendStates(r model.Route, exists map[string]bool) model.Route {
	out := r
	out.Rules = make([]model.Rule, len(r.Rules))
	for i, rule := range r.Rules {
		b := rule.Backend
		switch {
		case b.Kind != "Service":
			b.State = model.BackendIndirect
		case exists[b.Namespace+"/"+b.Service]:
			b.State = model.BackendOK
		default:
			b.State = model.BackendMissing
		}
		rule.Backend = b
		out.Rules[i] = rule
	}
	return out
}

func volumeModel(d volumeDef, pods []*simPod) model.Volume {
	v := model.Volume{Namespace: d.NS, Name: d.Name, StorageClass: d.Class, Requested: d.Size,
		AccessModes: []string{"ReadWriteOnce"}, Phase: "Pending", Pods: []string{}}
	if !d.Pending {
		v.Phase, v.Capacity = "Bound", d.Size
		v.VolumeName = fmt.Sprintf("pvc-%08x", hash32(d.NS+"/"+d.Name))
		for _, p := range pods {
			if d.Ordinal < 0 || p.ordinal == d.Ordinal {
				v.Pods = append(v.Pods, p.pod.UID)
			}
		}
		sort.Strings(v.Pods)
	}
	return v
}
```

Dans `internal/demo/sim.go` : le champ `netLast map[string]any // dernier modèle publié par « kind|clé » (réseau et stockage)` dans `Sim` ; `New` initialise `netLast: map[string]any{}` dans le littéral de `s` et appelle `s.flushNetwork()` juste après `s.flush()` ; `Step` appelle `s.flushNetwork()` juste après `s.flush()`.

- [ ] **Step 4 : YAML et événements simulés**

Ajouter à `internal/demo/network.go` :

```go
// netObject : objet Kubernetes (pour l'onglet YAML) d'un Service, d'une route ou d'un PVC simulé.
func (s *Sim) netObject(ref inspect.Ref) map[string]any {
	meta := map[string]any{"name": ref.Name, "namespace": ref.Namespace}
	switch ref.Kind {
	case "Service":
		m, ok := s.netLast["service|"+ref.Namespace+"/"+ref.Name].(model.Service)
		if !ok {
			return nil
		}
		spec := map[string]any{"type": m.Type}
		if m.Type == "ExternalName" {
			spec["externalName"] = m.ExternalName
		} else {
			spec["selector"] = map[string]any{"app.kubernetes.io/name": m.Name}
			spec["clusterIP"] = m.ClusterIP
			if m.Headless {
				spec["clusterIP"] = "None"
			}
			ports := []any{}
			for _, p := range m.Ports {
				ports = append(ports, map[string]any{"name": p.Name, "port": p.Port, "targetPort": p.TargetPort, "protocol": p.Protocol})
			}
			spec["ports"] = ports
		}
		return map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": meta, "spec": spec}
	case "PersistentVolumeClaim":
		v, ok := s.netLast["volume|"+ref.Namespace+"/"+ref.Name].(model.Volume)
		if !ok {
			return nil
		}
		size := fmt.Sprintf("%dGi", v.Requested/gi)
		status := map[string]any{"phase": v.Phase}
		spec := map[string]any{"accessModes": v.AccessModes, "storageClassName": v.StorageClass,
			"resources": map[string]any{"requests": map[string]any{"storage": size}}}
		if v.Phase == "Bound" {
			spec["volumeName"] = v.VolumeName
			status["capacity"] = map[string]any{"storage": size}
		}
		return map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": meta, "spec": spec, "status": status}
	case "Ingress", "IngressRoute":
		r, ok := s.netLast["route|"+ref.Kind+"/"+ref.Namespace+"/"+ref.Name].(model.Route)
		if !ok {
			return nil
		}
		if ref.Kind == "Ingress" {
			byHost := map[string][]any{}
			var hosts []string
			for _, rule := range r.Rules {
				if _, ok := byHost[rule.Host]; !ok {
					hosts = append(hosts, rule.Host)
				}
				byHost[rule.Host] = append(byHost[rule.Host], map[string]any{"path": rule.Path, "pathType": "Prefix",
					"backend": map[string]any{"service": map[string]any{"name": rule.Backend.Service, "port": map[string]any{"number": rule.Backend.Port}}}})
			}
			rules := []any{}
			for _, h := range hosts {
				rules = append(rules, map[string]any{"host": h, "http": map[string]any{"paths": byHost[h]}})
			}
			return map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": meta,
				"spec": map[string]any{"ingressClassName": r.Gate, "rules": rules}}
		}
		routes := []any{}
		for _, rule := range r.Rules {
			routes = append(routes, map[string]any{"match": rule.Match, "kind": "Rule",
				"services": []any{map[string]any{"name": rule.Backend.Service, "port": rule.Backend.Port}}})
		}
		return map[string]any{"apiVersion": r.Group + "/v1alpha1", "kind": "IngressRoute", "metadata": meta,
			"spec": map[string]any{"entryPoints": []any{"websecure"}, "routes": routes}}
	}
	return nil
}
```

Dans `internal/demo/inspect.go`, fonction `YAML`, ajouter avant `default:` :

```go
	case "Service", "PersistentVolumeClaim", "Ingress", "IngressRoute":
		obj = s.netObject(ref)
```

et remplacer `netEvents` :

```go
// netEvents : un PVC en attente attend son premier consommateur (StorageClass
// en WaitForFirstConsumer), comme sur GKE ou kind.
func (s *Sim) netEvents(kind, ns, name string) []model.Event {
	if kind == "PersistentVolumeClaim" {
		if v, ok := s.netLast["volume|"+ns+"/"+name].(model.Volume); ok && v.Phase == "Pending" {
			return []model.Event{{Type: "Normal", Reason: "WaitForFirstConsumer", Count: 12, Source: "persistentvolume-controller",
				Message: "waiting for first consumer to be created before binding", FirstSeen: s.now.Add(-time.Hour), LastSeen: s.now}}
		}
	}
	return []model.Event{}
}
```

(Déplacer `netEvents` dans `network.go` ; `inspect.go` ne garde que l'appel.)

- [ ] **Step 5 : vérifier**

Run: `go vet ./... && go test -race ./internal/demo/ ./internal/server/`
Expected: PASS.

Run: `go run ./cmd/atlas --demo --demo-scale 100x30 --addr :18080 &` puis, après 3 s, `curl -s localhost:18080/healthz; kill %1`
Expected: `ok` (pas de panique au démarrage à l'échelle).

- [ ] **Step 6 : commit**

```bash
git add internal/demo internal/server
git commit -m "feat(demo): Services, routes nginx et traefik, PVC simulés (et à l'échelle)"
```

### Task 11 : droits du ServiceAccount dans le chart

**Files:**
- Modify: `deploy/helm/cluster-atlas/templates/clusterrole.yaml`

- [ ] **Step 1 : règles**

Après la règle `[nodes, pods, namespaces, events]`, ajouter :

```yaml
  # Jalon 8 : Services, entrées et PVC (lecture seule ; un type refusé est désactivé).
  # get : onglet YAML en auth.mode=none, où le client du ServiceAccount sert.
  - apiGroups: [""]
    resources: [services, persistentvolumeclaims]
    verbs: [get, list, watch]
  - apiGroups: [discovery.k8s.io]
    resources: [endpointslices]
    verbs: [list, watch]
  - apiGroups: [networking.k8s.io]
    resources: [ingresses]
    verbs: [get, list, watch]
  - apiGroups: [networking.k8s.io]
    resources: [ingressclasses]
    verbs: [list, watch]
  - apiGroups: [traefik.io, traefik.containo.us]
    resources: [ingressroutes]
    verbs: [get, list, watch]
```

- [ ] **Step 2 : vérifier**

Run: `helm lint deploy/helm/cluster-atlas --set auth.oidc.clientID=ci --set publicURL=https://atlas.example.com && helm template deploy/helm/cluster-atlas --set auth.mode=none | grep -B2 -A2 ingressroutes`
Expected: `1 chart(s) linted, 0 chart(s) failed`, puis la règle `ingressroutes` affichée.

- [ ] **Step 3 : commit**

```bash
git add deploy/helm
git commit -m "feat(chart): lecture des Services, EndpointSlices, Ingress, IngressRoute et PVC"
```

---

## Phase B — Données du front

Toutes les commandes de cette phase se lancent depuis `web/`.

### Task 12 : types, store et dérivés réseau

**Files:**
- Modify: `web/src/api/types.ts`, `web/src/store/cluster.ts`, `web/src/store/fixtures.ts`, `web/src/ui/tree.ts` (type de `select`)
- Create: `web/src/store/net.ts`
- Test: `web/src/store/cluster.test.ts`, `web/src/store/net.test.ts`

- [ ] **Step 1 : types**

Ajouter à `web/src/api/types.ts`, avant `export type Kind` :

```ts
export type Health = 'ok' | 'degraded' | 'down' | 'external'

export interface ServicePort {
  name?: string
  port: number
  targetPort?: string
  protocol: string
  nodePort?: number
}

export interface Endpoint {
  podUID: string
  ready: boolean
}

export interface Service {
  namespace: string
  name: string
  type: string // ClusterIP | NodePort | LoadBalancer | ExternalName
  headless?: boolean
  clusterIP?: string
  ports: ServicePort[]
  loadBalancer?: string[]
  externalName?: string
  endpoints: Endpoint[]
  health: Health
}

export interface Backend {
  namespace: string
  service: string
  port?: string
  kind: string // Service | TraefikService
  state: 'ok' | 'missing' | 'indirect'
}

export interface Rule {
  host?: string
  path?: string
  match?: string
  backend: Backend
}

export interface Route {
  source: 'Ingress' | 'IngressRoute'
  group: string
  namespace: string
  name: string
  gate: string
  rules: Rule[]
  addresses?: string[]
}

export interface Volume {
  namespace: string
  name: string
  storageClass: string
  requested: number
  capacity: number
  accessModes: string[]
  phase: 'Pending' | 'Bound' | 'Lost'
  volumeName?: string
  pods: string[]
}
```

Remplacer `Kind` et `Message` :

```ts
export type Kind = 'node' | 'pod' | 'workload' | 'namespace' | 'service' | 'route' | 'volume'

export type Message =
  | {
      type: 'snapshot'; rev: number; nodes?: Node[]; pods?: Pod[]; workloads?: Workload[]; namespaces?: Namespace[]
      services?: Service[]; routes?: Route[]; volumes?: Volume[]
    }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'node'; obj: Node }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'pod'; obj: Pod }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'workload'; obj: Workload }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'namespace'; obj: Namespace }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'service'; obj: Service }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'route'; obj: Route }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'volume'; obj: Volume }
  | { type: 'metrics'; metrics: Metrics }
```

Et à la fin du fichier :

```ts
export const serviceKey = (s: Pick<Service, 'namespace' | 'name'>) => `${s.namespace}/${s.name}`
export const routeKey = (r: Pick<Route, 'source' | 'namespace' | 'name'>) => `${r.source}/${r.namespace}/${r.name}`
export const volumeKey = (v: Pick<Volume, 'namespace' | 'name'>) => `${v.namespace}/${v.name}`
```

- [ ] **Step 2 : fixtures**

Ajouter à `web/src/store/fixtures.ts` (import des nouveaux types) :

```ts
export function service(over: Partial<Service> = {}): Service {
  return {
    namespace: 'production', name: 'api', type: 'ClusterIP', clusterIP: '10.96.0.10',
    ports: [{ name: 'http', port: 80, targetPort: 'http', protocol: 'TCP' }],
    endpoints: [{ podUID: 'u1', ready: true }], health: 'ok',
    ...over,
  }
}

export function route(over: Partial<Route> = {}): Route {
  return {
    source: 'Ingress', group: 'networking.k8s.io', namespace: 'production', name: 'storefront', gate: 'nginx',
    rules: [{ host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'api', port: '80', kind: 'Service', state: 'ok' } }],
    ...over,
  }
}

export function volume(over: Partial<Volume> = {}): Volume {
  return {
    namespace: 'production', name: 'data-0', storageClass: 'standard-rwo', requested: 10 * 2 ** 30, capacity: 10 * 2 ** 30,
    accessModes: ['ReadWriteOnce'], phase: 'Bound', volumeName: 'pvc-1', pods: ['u1'],
    ...over,
  }
}
```

- [ ] **Step 3 : tests qui échouent**

Ajouter à `web/src/store/cluster.test.ts` (importer `route`, `service`, `volume` depuis `./fixtures`) :

```ts
describe('réseau et stockage', () => {
  it('suit Services, routes et volumes', () => {
    s().applyMessages([{ type: 'snapshot', rev: 1, services: [service()], routes: [route()], volumes: [volume()] }])
    expect([...s().services.keys()]).toEqual(['production/api'])
    expect([...s().routes.keys()]).toEqual(['Ingress/production/storefront'])
    expect([...s().volumes.keys()]).toEqual(['production/data-0'])
    s().applyMessages([
      { type: 'upsert', kind: 'service', rev: 2, obj: service({ name: 'web' }) },
      { type: 'delete', kind: 'route', rev: 3, obj: route() },
      { type: 'delete', kind: 'volume', rev: 4, obj: volume() },
    ])
    expect(s().services.size).toBe(2)
    expect(s().routes.size).toBe(0)
    expect(s().volumes.size).toBe(0)
  })

  it('sélectionne un Service, une route, un volume ou une porte', () => {
    s().applyMessages([{ type: 'snapshot', rev: 1, services: [service()], routes: [route()], volumes: [volume()] }])
    s().select({ type: 'service', key: 'production/api' })
    expect(s().selection).toEqual({ type: 'service', key: 'production/api', name: 'api' })
    s().select({ type: 'route', key: 'Ingress/production/storefront' })
    expect(s().selection?.name).toBe('storefront')
    s().select({ type: 'gate', key: 'nginx' })
    expect(s().selection?.name).toBe('nginx')
  })

  it('retient l’objet réseau survolé', () => {
    s().setHoverNet('service:production/api')
    expect(s().hoverNet).toBe('service:production/api')
  })
})
```

Créer `web/src/store/net.test.ts` :

```ts
import { describe, expect, it } from 'vitest'
import { route, service, volume } from './fixtures'
import { gatesOf, readyCount, routeBroken, routesTo, servicesOfPod, volumesOfPod } from './net'

const missing = route({ name: 'admin', source: 'IngressRoute', group: 'traefik.io', gate: 'traefik',
  rules: [{ match: 'Host(`admin`)', backend: { namespace: 'production', service: 'ghost', kind: 'Service', state: 'missing' } }] })

describe('portes', () => {
  it('regroupe les routes par porte, triées, et compte les routes cassées', () => {
    const gates = gatesOf([missing, route(), route({ name: 'b', gate: 'nginx' })])
    expect(gates.map((g) => [g.name, g.routes.length, g.broken])).toEqual([['nginx', 2, 0], ['traefik', 1, 1]])
    expect(gates[0].routes.map((r) => r.name)).toEqual(['b', 'storefront'])
    expect(routeBroken(missing)).toBe(true)
  })
})

describe('relations', () => {
  it('relie Services, routes, pods et volumes', () => {
    expect(routesTo([route(), missing], service())).toHaveLength(1)
    expect(servicesOfPod([service(), service({ name: 'x', endpoints: [] })], 'u1').map((s) => s.name)).toEqual(['api'])
    expect(volumesOfPod([volume(), volume({ name: 'd1', pods: [] })], 'u1').map((v) => v.name)).toEqual(['data-0'])
    expect(readyCount(service({ endpoints: [{ podUID: 'a', ready: true }, { podUID: 'b', ready: false }] }))).toBe(1)
  })
})
```

Run: `npx vitest run src/store`
Expected: FAIL.

- [ ] **Step 4 : store et dérivés**

Créer `web/src/store/net.ts` :

```ts
import type { Route, Service, Volume } from '../api/types'

// Relations dérivées du flux : portes (déduites des routes), routes d'un
// Service, Services et volumes d'un pod.

export interface Gate {
  name: string
  routes: Route[]
  /** Routes dont un backend est introuvable. */
  broken: number
}

export const routeBroken = (r: Route) => r.rules.some((x) => x.backend.state === 'missing')

const byNsName = (a: { namespace: string; name: string }, b: { namespace: string; name: string }) =>
  a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name)

/** Portes triées par nom, routes de chaque porte triées par namespace puis nom. */
export function gatesOf(routes: Iterable<Route>): Gate[] {
  const by = new Map<string, Route[]>()
  for (const r of routes) by.set(r.gate, [...(by.get(r.gate) ?? []), r])
  return [...by]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([name, rs]) => {
      const sorted = [...rs].sort(byNsName)
      return { name, routes: sorted, broken: sorted.filter(routeBroken).length }
    })
}

export const routesTo = (routes: Iterable<Route>, svc: Pick<Service, 'namespace' | 'name'>) =>
  [...routes].filter((r) => r.rules.some((x) => x.backend.kind === 'Service' && x.backend.namespace === svc.namespace && x.backend.service === svc.name))

export const servicesOfPod = (services: Iterable<Service>, uid: string) =>
  [...services].filter((s) => s.endpoints.some((e) => e.podUID === uid))

export const volumesOfPod = (volumes: Iterable<Volume>, uid: string) => [...volumes].filter((v) => v.pods.includes(uid))

export const readyCount = (s: Service) => s.endpoints.filter((e) => e.ready).length
```

Dans `web/src/store/cluster.ts` :

1. Imports : `routeKey, serviceKey, volumeKey, type Route, type Service, type Volume`.
2. Types :

```ts
export type SelectionType = 'pod' | 'node' | 'service' | 'route' | 'volume' | 'gate'
export type Selection = { type: SelectionType; key: string; name: string } | null
```

3. `ClusterState` gagne, après `namespaces` :

```ts
  services: Map<string, Service>
  routes: Map<string, Route>
  volumes: Map<string, Volume>
```

après `hover` :

```ts
  /** Porte, relais ou citerne sous le pointeur (« type:clé ») : ses fibres s'affichent. */
  hoverNet: string | null
```

et parmi les actions : `select(sel: { type: SelectionType; key: string } | null): void` et `setHoverNet(key: string | null): void`.

4. `initial()` : `services: new Map<string, Service>(), routes: new Map<string, Route>(), volumes: new Map<string, Volume>(), hoverNet: null as string | null`.
5. `applyMessages` : déstructurer aussi `services, routes, volumes` ; dans `snapshot` :

```ts
            services = new Map((m.services ?? []).map((x) => [serviceKey(x), x]))
            routes = new Map((m.routes ?? []).map((x) => [routeKey(x), x]))
            volumes = new Map((m.volumes ?? []).map((x) => [volumeKey(x), x]))
```

dans les deltas, avant le `else` final (workloads) :

```ts
            } else if (m.kind === 'service') {
              if (del) services.delete(serviceKey(m.obj))
              else services.set(serviceKey(m.obj), m.obj)
            } else if (m.kind === 'route') {
              if (del) routes.delete(routeKey(m.obj))
              else routes.set(routeKey(m.obj), m.obj)
            } else if (m.kind === 'volume') {
              if (del) volumes.delete(volumeKey(m.obj))
              else volumes.set(volumeKey(m.obj), m.obj)
```

et `set({ … services, routes, volumes, … })`.

6. `select` :

```ts
    select(sel) {
      if (!sel) return set({ selection: null })
      const st = get()
      const name = {
        pod: () => st.pods.get(sel.key)?.name,
        node: () => st.nodes.get(sel.key)?.name,
        service: () => st.services.get(sel.key)?.name,
        route: () => st.routes.get(sel.key)?.name,
        volume: () => st.volumes.get(sel.key)?.name,
        gate: () => sel.key,
      }[sel.type]()
      // Le YAML choisi via la chaîne de propriétaires ne survit pas au changement de pod.
      set({ selection: { ...sel, name: name ?? sel.key }, yamlTarget: null })
    },
```

7. `setHoverNet: (hoverNet) => { if (get().hoverNet !== hoverNet) set({ hoverNet }) },`

Dans `web/src/ui/tree.ts`, `TreeNode.select` devient `select?: { type: SelectionType; key: string }` (import `type SelectionType` depuis `../store/cluster`).

- [ ] **Step 5 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 6 : commit**

```bash
git add web/src
git commit -m "feat(web): Services, routes et volumes dans le store, sélection et survol"
```

### Task 13 : couleurs, santé et tokens de thème

**Files:**
- Modify: `web/src/scene/colors.ts`, `web/src/scene/theme.ts`, `web/src/styles.css`
- Create: `web/src/scene/health.ts`
- Test: `web/src/scene/colors.test.ts`, `web/src/scene/health.test.ts`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `web/src/scene/colors.test.ts` (imports `clusterColors` et `service`, `volume`, `pod` depuis `../store/fixtures`) :

```ts
describe('clusterColors', () => {
  it('colore aussi les namespaces qui n’ont que des Services ou des volumes', () => {
    const colors = clusterColors({
      pods: new Map([['u1', pod()]]), namespaces: new Map(),
      services: new Map([['edge/lb', service({ namespace: 'edge', name: 'lb' })]]),
      volumes: new Map([['data/d', volume({ namespace: 'data', name: 'd' })]]),
    })
    expect([...colors.keys()].sort()).toEqual(['data', 'edge', 'production'])
  })
})
```

Créer `web/src/scene/health.test.ts` :

```ts
import { describe, expect, it } from 'vitest'
import { gateSignal, healthSignal, volumeSignal, worst } from './health'

describe('signaux', () => {
  it('traduit santé, phase et routes cassées en couleur de voyant', () => {
    expect(['ok', 'degraded', 'down', 'external'].map((h) => healthSignal(h as never))).toEqual(['ok', 'warn', 'err', 'mute'])
    expect(['Bound', 'Pending', 'Lost'].map((p) => volumeSignal({ phase: p as never }))).toEqual(['ok', 'warn', 'err'])
    expect(gateSignal({ broken: 0 })).toBe('ok')
    expect(gateSignal({ broken: 2 })).toBe('warn')
  })

  it('garde le pire signal d’un groupe', () => {
    expect(worst(['ok', 'warn', 'ok'])).toBe('warn')
    expect(worst(['mute', 'err', 'warn'])).toBe('err')
    expect(worst([])).toBe('mute')
  })
})
```

Run: `npx vitest run src/scene/colors.test.ts src/scene/health.test.ts`
Expected: FAIL.

- [ ] **Step 2 : implémentation**

`web/src/scene/colors.ts`, remplacer `clusterColors` :

```ts
/** Couleurs des namespaces présents (pods, Services, volumes), annotations comprises. */
export function clusterColors(st: {
  pods: ReadonlyMap<string, { namespace: string }>
  namespaces: ReadonlyMap<string, { color?: string }>
  services?: ReadonlyMap<string, { namespace: string }>
  volumes?: ReadonlyMap<string, { namespace: string }>
}) {
  const overrides = new Map<string, string>()
  for (const [name, ns] of st.namespaces) if (ns.color) overrides.set(name, ns.color)
  const names = [...st.pods.values(), ...(st.services?.values() ?? []), ...(st.volumes?.values() ?? [])].map((o) => o.namespace)
  return nsColors(names, overrides)
}
```

Créer `web/src/scene/health.ts` :

```ts
import type { Health, Volume } from '../api/types'
import type { Gate } from '../store/net'

// Couleur des voyants (relais, portes, citernes), dans le vocabulaire des badges.

export type Signal = 'ok' | 'warn' | 'err' | 'mute'

const RANK: Record<Signal, number> = { mute: 0, ok: 1, warn: 2, err: 3 }

export const healthSignal = (h: Health): Signal => (h === 'ok' ? 'ok' : h === 'degraded' ? 'warn' : h === 'down' ? 'err' : 'mute')
export const volumeSignal = (v: Pick<Volume, 'phase'>): Signal => (v.phase === 'Bound' ? 'ok' : v.phase === 'Pending' ? 'warn' : 'err')
export const gateSignal = (g: Pick<Gate, 'broken'>): Signal => (g.broken ? 'warn' : 'ok')
export const worst = (xs: Signal[]): Signal => xs.reduce<Signal>((w, x) => (RANK[x] > RANK[w] ? x : w), 'mute')

export const HEALTH_LABEL: Record<Health, string> = {
  ok: 'Sain', degraded: 'Dégradé', down: 'Aucun endpoint ready', external: 'ExternalName',
}
```

`web/src/scene/theme.ts`, compléter `TOKENS` :

```ts
  err: '--err', tree: '--tree', house: '--house', roof: '--roof', font: '--font',
  avenue: '--avenue', warehouse: '--warehouse', fibre: '--fibre', data: '--data',
```

`web/src/styles.css` : dans `:root` (ligne des tokens `--code-bg…`), ajouter `--avenue:#C9D2CF; --warehouse:#E2DDD0; --fibre:#2F5D8C; --data:#1F9E94;` ; dans les deux blocs sombres (`@media (prefers-color-scheme: dark)` et `:root[data-theme="dark"]`), ajouter `--avenue:#27343A; --warehouse:#2F2B23; --fibre:#6C9BD0; --data:#3FB8AD;`.

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run src/scene`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add web/src
git commit -m "feat(web): couleurs des namespaces réseau, voyants et tokens avenue, entrepôts, fibre, data"
```

### Task 14 : avenues dans la disposition de la ville

**Files:**
- Modify: `web/src/scene/layout.ts`
- Test: `web/src/scene/layout.test.ts`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `web/src/scene/layout.test.ts` (importer `AVENUE`, `GATE_ZONE`) :

```ts
describe('avenues', () => {
  const geo = plotGeometry(12)

  it('une seule rangée : une avenue devant, avant la file d’attente', () => {
    const c = layoutCity([n('a-1', 'p'), n('a-2', 'p')], geo)
    expect(c.avenues).toHaveLength(1)
    const a = c.avenues[0]
    const maxZ = Math.max(...[...c.plots.values()].map((p) => p.z))
    expect(a.z - AVENUE / 2).toBeGreaterThan(maxZ)
    expect(c.queue.z - c.queue.depth / 2).toBeGreaterThan(a.z + AVENUE / 2)
  })

  it('une avenue entre chaque paire de rangées, sans toucher les parcelles', () => {
    const many = Array.from({ length: 40 }, (_, i) => n(`node-${String(i).padStart(2, '0')}`, `pool-${i % 4}`))
    const c = layoutCity(many, geo)
    const rows = new Set(c.districts.map((d) => (d.z - d.depth / 2).toFixed(3))).size
    expect(rows).toBeGreaterThan(1)
    expect(c.avenues).toHaveLength(rows - 1)
    const plots = [...c.plots.values()]
    for (const a of c.avenues) {
      expect(plots.some((p) => p.z < a.z)).toBe(true)
      expect(plots.some((p) => p.z > a.z)).toBe(true)
      for (const p of plots) expect(Math.abs(p.z - a.z)).toBeGreaterThan(AVENUE / 2 + geo.depth / 2 - 1e-6)
    }
  })

  it('commence à l’ouest de la ville, où se tiennent les portes', () => {
    const c = layoutCity(nodes, geo)
    const left = Math.min(...c.districts.map((d) => d.x - d.width / 2))
    for (const a of c.avenues) expect(a.x - a.width / 2).toBeCloseTo(left - GATE_ZONE)
    expect(c.bounds.x - c.bounds.width / 2).toBeLessThanOrEqual(left - GATE_ZONE + 1e-6)
  })
})
```

Run: `npx vitest run src/scene/layout.test.ts`
Expected: FAIL (`c.avenues` indéfini).

- [ ] **Step 2 : implémentation**

Dans `web/src/scene/layout.ts` :

```ts
export interface Avenue {
  x: number // centre
  z: number
  width: number
  depth: number
}
```

`CityLayout` gagne `avenues: Avenue[]`. Exporter `ALLEY` (`export const ALLEY = 1.9`) et ajouter :

```ts
/** Largeur d'une avenue : une rangée de relais, trois voies de liens et les noms des tronçons. */
export const AVENUE = 3.6
/** Entrée ouest de la première avenue, où se tiennent les portes. */
export const GATE_ZONE = 4.5
```

Dans `layoutCity`, remplacer l'algorithme d'étagères et la suite par :

```ts
  // Étagères : on remplit une rangée jusqu'à maxRow, puis on passe à la suivante,
  // de l'autre côté d'une avenue.
  type Placed = (typeof boxes)[number] & { x0: number; z0: number }
  const placed: Placed[] = []
  const avenueTops: number[] = [] // bord nord de chaque avenue, avant centrage
  let x = 0, z = 0, rowDepth = 0, width = 0
  for (const b of boxes) {
    if (x > 0 && x + b.width > maxRow) {
      avenueTops.push(z + rowDepth)
      z += rowDepth + AVENUE
      x = 0
      rowDepth = 0
    }
    placed.push({ ...b, x0: x, z0: z })
    x += b.width + DISTRICT_GAP
    width = Math.max(width, x - DISTRICT_GAP)
    rowDepth = Math.max(rowDepth, b.depth)
  }
  let depth = z + rowDepth
  // Une seule rangée : l'avenue passe devant, entre la ville et la file d'attente.
  if (!avenueTops.length) {
    avenueTops.push(depth)
    depth += AVENUE
  }
  const ox = -width / 2, oz = -depth / 2
```

(la boucle `districts` / `plots` reste identique), puis :

```ts
  const avenues: Avenue[] = avenueTops.map((top) => ({
    x: (1 - GATE_ZONE) / 2, z: oz + top + AVENUE / 2, width: width + GATE_ZONE + 1, depth: AVENUE,
  }))
  const queue: Rect = { x: 0, z: depth / 2 + 3.4, width: Math.max(14, width * 0.8), depth: 2.6 }
  return {
    districts, plots, queue, avenues, geometry: geo,
    bounds: { x: -GATE_ZONE / 2, z: 1.7, width: width + GATE_ZONE, depth: depth + 3.4 * 2 },
  }
```

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run src/scene`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add web/src/scene
git commit -m "feat(web): avenues entre les rangées de quartiers, entrée ouest pour les portes"
```

### Task 15 : disposition du réseau et des entrepôts

**Files:**
- Create: `web/src/scene/netLayout.ts`
- Test: `web/src/scene/netLayout.test.ts`

- [ ] **Step 1 : tests qui échouent**

Créer `web/src/scene/netLayout.test.ts` :

```ts
import { describe, expect, it } from 'vitest'
import { node } from '../store/fixtures'
import { layoutCity, plotGeometry } from './layout'
import { layoutNetwork, RELAY_PITCH, tankRadius, type RelayInput, type TankInput } from './netLayout'

const city = layoutCity([node({ name: 'a-1', pool: 'p' }), node({ name: 'a-2', pool: 'p' }), node({ name: 'a-3', pool: 'p' })], plotGeometry(12))
const relay = (ns: string, name: string): RelayInput => ({ key: `${ns}/${name}`, namespace: ns, name })
const tank = (ns: string, name: string, storageClass: string, gib = 10): TankInput => ({ key: `${ns}/${name}`, namespace: ns, name, storageClass, requested: gib * 2 ** 30 })
const GiB = 2 ** 30

describe('relais', () => {
  const relays = [relay('staging', 'api'), relay('production', 'web'), relay('production', 'api')]

  it('range les namespaces par nom, les relais par nom, avec un espace entre deux namespaces', () => {
    const net = layoutNetwork(city, relays, [], [])
    const x = (k: string) => net.relays.get(k)!.x
    expect(x('production/api')).toBeLessThan(x('production/web'))
    expect(x('production/web') - x('production/api')).toBeCloseTo(RELAY_PITCH)
    expect(x('staging/api') - x('production/web')).toBeCloseTo(2 * RELAY_PITCH)
    expect(net.segments.map((s) => s.ns)).toEqual(['production', 'staging'])
    expect(net.segments[0].x1 - net.segments[0].x0).toBeCloseTo(2 * RELAY_PITCH)
  })

  it('ne dépend pas de l’ordre d’arrivée', () => {
    const a = layoutNetwork(city, relays, ['nginx', 'traefik'], [])
    const b = layoutNetwork(city, [...relays].reverse(), ['traefik', 'nginx'], [])
    expect([...a.relays.entries()].sort()).toEqual([...b.relays.entries()].sort())
    expect([...a.gates.entries()]).toEqual([...b.gates.entries()])
  })

  it('pose les relais sur l’avenue, au-delà des portes', () => {
    const net = layoutNetwork(city, relays, ['nginx'], [])
    const a = city.avenues[0]
    for (const r of net.relays.values()) {
      expect(Math.abs(r.z - a.z)).toBeLessThan(a.depth / 2)
      expect(r.x).toBeGreaterThan(net.gates.get('nginx')!.x)
      expect(r.x).toBeGreaterThan(net.westX)
    }
  })

  it('regroupe les plus gros namespaces quand l’avenue est pleine', () => {
    const many = [
      ...Array.from({ length: 60 }, (_, i) => relay('big', `s-${String(i).padStart(2, '0')}`)),
      relay('small', 'a'), relay('small', 'b'),
    ]
    const net = layoutNetwork(city, many, [], [])
    expect(net.groups.map((g) => g.ns)).toEqual(['big'])
    expect(net.groups[0].members).toHaveLength(60)
    expect(net.relays.get('big/s-00')!.group).toBe('ns:big')
    expect(net.relays.get('small/a')!.group).toBeUndefined()
    expect(net.relays.size).toBe(62)
  })
})

describe('portes', () => {
  it('se tiennent à l’entrée ouest de la première avenue, triées par nom', () => {
    const net = layoutNetwork(city, [], ['traefik', 'nginx'], [])
    const [g1, g2] = [net.gates.get('nginx')!, net.gates.get('traefik')!]
    expect(g1.x).toBe(g2.x)
    expect(g1.z).toBeLessThan(g2.z)
    expect(g1.x).toBeLessThan(net.westX)
  })
})

describe('entrepôts', () => {
  const tanks = [tank('production', 'b', 'standard-rwo'), tank('production', 'a', 'standard-rwo', 100), tank('staging', 'x', ''), tank('monitoring', 'm', 'premium-rwo', 1)]

  it('un îlot par StorageClass, à l’est de la ville', () => {
    const net = layoutNetwork(city, [], [], tanks)
    expect(net.islands.map((i) => i.storageClass)).toEqual(['(aucune)', 'premium-rwo', 'standard-rwo'])
    const right = Math.max(...city.districts.map((d) => d.x + d.width / 2))
    for (const t of net.tanks.values()) expect(t.x).toBeGreaterThan(right)
    expect(net.eastX).toBeGreaterThan(right)
    expect(net.eastX).toBeLessThan(Math.min(...[...net.tanks.values()].map((t) => t.x)))
    expect(net.tanks.get('production/a')!.x).toBeLessThan(net.tanks.get('production/b')!.x)
  })

  it('agrandit la ville pour les contenir', () => {
    const net = layoutNetwork(city, [], [], tanks)
    const b = net.bounds, w = net.warehouse!
    expect(b.x + b.width / 2).toBeGreaterThanOrEqual(w.x + w.width / 2)
    expect(layoutNetwork(city, [], [], []).warehouse).toBeNull()
  })

  it('dimensionne les citernes selon la capacité, entre deux bornes', () => {
    expect(tankRadius(1 * GiB)).toBeCloseTo(0.35)
    expect(tankRadius(8 * GiB)).toBeGreaterThan(tankRadius(1 * GiB))
    expect(tankRadius(10_000 * GiB)).toBe(0.8)
  })
})
```

Run: `npx vitest run src/scene/netLayout.test.ts`
Expected: FAIL (module absent).

- [ ] **Step 2 : implémentation**

Créer `web/src/scene/netLayout.ts` :

```ts
import { GATE_ZONE, type Avenue, type CityLayout, type Rect } from './layout'

// Disposition du réseau et du stockage : relais (Services) rangés par
// namespace sur les avenues, portes à l'entrée ouest de la première avenue,
// citernes (PVC) dans un quartier Entrepôts à l'est. Fonction pure : ne dépend
// que de la ville et des clés reçues.

export const RELAY_PITCH = 1.3
const RELAY_ROW = 0.85 // centre des relais depuis le bord nord de l'avenue
const GATE_PITCH = 2.2
const TANK_PITCH = 1.9
const TANK_COLS = 3
const ISLAND_LABEL = 1.0
const ISLAND_GAP = 0.8
const WAREHOUSE_GAP = 3.2 // entre la ville et les entrepôts, rue est comprise
const GiB = 2 ** 30
const UNCLASSED = '(aucune)'

export interface RelayInput { key: string; namespace: string; name: string }
export interface TankInput { key: string; namespace: string; name: string; storageClass: string; requested: number }

export interface RelaySlot {
  key: string
  ns: string
  x: number
  z: number
  avenue: number
  /** Bloc qui représente ce relais quand son namespace est regroupé. */
  group?: string
}
export interface RelayGroup { key: string; ns: string; x: number; z: number; avenue: number; members: string[] }
export interface Segment { ns: string; avenue: number; x0: number; x1: number }
export interface GateSlot { name: string; x: number; z: number }
export interface Island { storageClass: string; x: number; z: number; width: number; depth: number }
export interface TankSlot { key: string; x: number; z: number; r: number }
/** Repères d'une avenue (z) : bord nord, voies des lignes principales, des fibres et des conduites, noms des tronçons. */
export interface Lanes { north: number; main: number; fibre: number; data: number; label: number }

export interface NetLayout {
  segments: Segment[]
  relays: Map<string, RelaySlot>
  groups: RelayGroup[]
  gates: Map<string, GateSlot>
  islands: Island[]
  tanks: Map<string, TankSlot>
  warehouse: Rect | null
  lanes: Lanes[]
  /** Rue ouest (portes ↔ avenues) et rue est (entrepôts ↔ avenues). */
  westX: number
  eastX: number
  bounds: Rect
}

export function lanesOf(a: Avenue): Lanes {
  return { north: a.z - a.depth / 2, main: a.z - 0.05, fibre: a.z + 0.45, data: a.z + 0.95, label: a.z + 1.45 }
}

/** Rayon d'une citerne : logarithme de la capacité demandée, entre 0,35 et 0,8. */
export const tankRadius = (bytes: number) => Math.min(0.8, Math.max(0.35, 0.35 + 0.1 * Math.log2(Math.max(1, bytes / GiB))))

function union(a: Rect, b: Rect | null): Rect {
  if (!b) return a
  const x0 = Math.min(a.x - a.width / 2, b.x - b.width / 2), x1 = Math.max(a.x + a.width / 2, b.x + b.width / 2)
  const z0 = Math.min(a.z - a.depth / 2, b.z - b.depth / 2), z1 = Math.max(a.z + a.depth / 2, b.z + b.depth / 2)
  return { x: (x0 + x1) / 2, z: (z0 + z1) / 2, width: x1 - x0, depth: z1 - z0 }
}

export function layoutNetwork(city: CityLayout, relays: RelayInput[], gates: string[], tanks: TankInput[]): NetLayout {
  const avenues = city.avenues
  const lanes = avenues.map(lanesOf)
  const left = Math.min(...city.districts.map((d) => d.x - d.width / 2))
  const right = Math.max(...city.districts.map((d) => d.x + d.width / 2))
  const top = Math.min(...city.districts.map((d) => d.z - d.depth / 2))
  const a0 = avenues[0]
  const westX = left - 1.9
  const eastX = right + 1.4

  // Places des relais, de l'ouest vers l'est, avenue après avenue.
  const slots: { avenue: number; x: number }[] = []
  avenues.forEach((a, i) => {
    const x0 = a.x - a.width / 2 + GATE_ZONE + 0.6, x1 = a.x + a.width / 2 - 0.6
    for (let x = x0 + RELAY_PITCH / 2; x <= x1 - RELAY_PITCH / 2 + 1e-9; x += RELAY_PITCH) slots.push({ avenue: i, x })
  })
  if (!slots.length) slots.push({ avenue: 0, x: a0.x })

  const byNs = new Map<string, RelayInput[]>()
  for (const r of relays) byNs.set(r.namespace, [...(byNs.get(r.namespace) ?? []), r])
  const nss = [...byNs.keys()].sort()
  for (const ns of nss) byNs.get(ns)!.sort((a, b) => a.name.localeCompare(b.name))

  // Trop de relais : on regroupe les plus gros namespaces en un seul bloc jusqu'à tenir.
  const grouped = new Set<string>()
  const need = () => nss.reduce((s, ns) => s + (grouped.has(ns) ? 1 : byNs.get(ns)!.length), 0) + Math.max(0, nss.length - 1)
  const bySize = [...nss].sort((a, b) => byNs.get(b)!.length - byNs.get(a)!.length || a.localeCompare(b))
  for (const ns of bySize) {
    if (need() <= slots.length) break
    if (byNs.get(ns)!.length > 1) grouped.add(ns)
  }

  const out = new Map<string, RelaySlot>()
  const groups: RelayGroup[] = []
  const segments: Segment[] = []
  let next = 0
  const take = () => slots[Math.min(next++, slots.length - 1)] // au-delà : empilés sur la dernière place
  for (const ns of nss) {
    const items = byNs.get(ns)!
    const used: { avenue: number; x: number }[] = []
    if (grouped.has(ns)) {
      const s = take()
      const key = `ns:${ns}`
      const z = lanes[s.avenue].north + RELAY_ROW
      groups.push({ key, ns, x: s.x, z, avenue: s.avenue, members: items.map((r) => r.key) })
      for (const r of items) out.set(r.key, { key: r.key, ns, x: s.x, z, avenue: s.avenue, group: key })
      used.push(s)
    } else {
      for (const r of items) {
        const s = take()
        out.set(r.key, { key: r.key, ns, x: s.x, z: lanes[s.avenue].north + RELAY_ROW, avenue: s.avenue })
        used.push(s)
      }
    }
    // Un tronçon par avenue traversée.
    for (const s of used) {
      const last = segments[segments.length - 1]
      if (last && last.ns === ns && last.avenue === s.avenue) last.x1 = s.x + RELAY_PITCH / 2
      else segments.push({ ns, avenue: s.avenue, x0: s.x - RELAY_PITCH / 2, x1: s.x + RELAY_PITCH / 2 })
    }
    next++ // une place libre entre deux namespaces
  }

  // Portes : en colonne à l'entrée de la première avenue.
  const names = [...new Set(gates)].sort()
  const gx = a0.x - a0.width / 2 + 1.2
  const gateSlots = new Map(names.map((name, k) => [name, { name, x: gx, z: a0.z + (k - (names.length - 1) / 2) * GATE_PITCH }]))

  // Entrepôts : un îlot par StorageClass, empilés du nord au sud.
  const byClass = new Map<string, TankInput[]>()
  for (const t of tanks) {
    const c = t.storageClass || UNCLASSED
    byClass.set(c, [...(byClass.get(c) ?? []), t])
  }
  const wx = right + WAREHOUSE_GAP
  const width = TANK_COLS * TANK_PITCH + 0.8
  const islands: Island[] = []
  const tankSlots = new Map<string, TankSlot>()
  let z = top
  for (const c of [...byClass.keys()].sort()) {
    const ts = byClass.get(c)!.sort((a, b) => a.namespace.localeCompare(b.namespace) || a.name.localeCompare(b.name))
    const rows = Math.ceil(ts.length / TANK_COLS)
    const depth = ISLAND_LABEL + rows * TANK_PITCH + 0.4
    islands.push({ storageClass: c, x: wx + width / 2, z: z + depth / 2, width, depth })
    ts.forEach((t, k) => tankSlots.set(t.key, {
      key: t.key,
      x: wx + 0.4 + TANK_PITCH * ((k % TANK_COLS) + 0.5),
      z: z + ISLAND_LABEL + TANK_PITCH * (Math.floor(k / TANK_COLS) + 0.5),
      r: tankRadius(t.requested),
    }))
    z += depth + ISLAND_GAP
  }
  const zEnd = z - ISLAND_GAP
  const warehouse = islands.length ? { x: wx + width / 2, z: (top + zEnd) / 2, width: width + 1, depth: zEnd - top + 1 } : null

  return {
    segments, relays: out, groups, gates: gateSlots, islands, tanks: tankSlots, warehouse, lanes, westX, eastX,
    bounds: union(city.bounds, warehouse && { ...warehouse, width: warehouse.width + 2, depth: warehouse.depth + 2 }),
  }
}
```

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run src/scene/netLayout.test.ts`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add web/src/scene/netLayout.ts web/src/scene/netLayout.test.ts
git commit -m "feat(web): disposition des relais, des portes et des entrepôts"
```

### Task 16 : tracé des liens et chemin d'un objet

**Files:**
- Create: `web/src/scene/links.ts`
- Test: `web/src/scene/links.test.ts`

- [ ] **Step 1 : tests qui échouent**

Créer `web/src/scene/links.test.ts` :

```ts
import { describe, expect, it } from 'vitest'
import { routeKey, serviceKey, volumeKey, type Pod } from '../api/types'
import { node, pod, route, service, volume } from '../store/fixtures'
import { layoutCity, plotGeometry } from './layout'
import { buildLinks, isLit, pathOf, type Link } from './links'
import { layoutNetwork } from './netLayout'

const nodes = [node({ name: 'n1', pool: 'p' }), node({ name: 'n2', pool: 'p' })]
const city = layoutCity(nodes, plotGeometry(12))
const pods: Pod[] = [
  pod({ uid: 'a', nodeName: 'n1' }), pod({ uid: 'b', nodeName: 'n2' }),
  pod({ uid: 'pending', nodeName: '', displayStatus: 'Pending' }),
  pod({ uid: 'db', name: 'db-0', nodeName: 'n2', displayStatus: 'Running' }),
]
const targets = new Map(pods.map((p) => {
  const plot = city.plots.get(p.nodeName)
  return [p.uid, plot ? { x: plot.x, z: plot.z + 1, onNode: true } : { x: 0, z: 40, onNode: false }]
}))
const api = service({ endpoints: [{ podUID: 'a', ready: true }, { podUID: 'b', ready: false }, { podUID: 'pending', ready: false }] })
const db = service({ name: 'db', headless: true, endpoints: [{ podUID: 'db', ready: true }] })
const shop = route()
const shop2 = route({ name: 'storefront-v2' })
const broken = route({ name: 'admin', source: 'IngressRoute', group: 'traefik.io', gate: 'traefik',
  rules: [{ match: 'Host(`admin`)', backend: { namespace: 'production', service: 'ghost', kind: 'Service', state: 'missing' } }] })
const data = volume({ name: 'data-db-0', pods: ['db'] })

const net = layoutNetwork(city, [api, db].map((s) => ({ key: serviceKey(s), namespace: s.namespace, name: s.name })), ['nginx', 'traefik'],
  [{ key: volumeKey(data), namespace: data.namespace, name: data.name, storageClass: data.storageClass, requested: data.requested }])
const links = buildLinks({ city, net, services: [api, db], routes: [shop, shop2, broken], volumes: [data],
  pods: new Map(pods.map((p) => [p.uid, p])), targets })
const fam = (f: Link['family']) => links.filter((l) => l.family === f)

describe('buildLinks', () => {
  it('une ligne principale par couple porte-Service, quelle que soit la route', () => {
    expect(fam('main')).toHaveLength(1)
    const main = fam('main')[0]
    expect(main.keys).toEqual(['gate:nginx', 'service:production/api', `route:${routeKey(shop)}`, `route:${routeKey(shop2)}`])
    const relay = net.relays.get('production/api')!
    expect(main.points[main.points.length - 1]).toEqual([relay.x, relay.z])
  })

  it('trace une route cassée jusqu’au tronçon de son namespace, avec un panneau', () => {
    const [b] = fam('broken')
    expect(b.keys).toEqual(['gate:traefik', `route:${routeKey(broken)}`])
    expect(b.sign![0]).toBeCloseTo(net.segments.find((s) => s.ns === 'production')!.x0)
  })

  it('relie chaque endpoint placé, pas les pods en attente', () => {
    const fibres = fam('fibre').filter((l) => l.keys[0] === 'service:production/api')
    expect(fibres.map((l) => [l.keys[1], l.live])).toEqual([['pod:a', true], ['pod:b', false]])
    const last = fibres[0].points[fibres[0].points.length - 1]
    expect(last).toEqual([targets.get('a')!.x, targets.get('a')!.z])
  })

  it('relie une citerne aux pods qui la montent, par la rue est', () => {
    const [d] = fam('data')
    expect(d.keys).toEqual(['volume:production/data-db-0', 'pod:db'])
    expect(d.live).toBe(true)
    expect(d.points[1][0]).toBe(net.eastX)
  })

  it('ne trace que des segments horizontaux ou verticaux', () => {
    for (const l of links)
      for (let i = 1; i < l.points.length; i++) {
        const [x0, z0] = l.points[i - 1], [x1, z1] = l.points[i]
        expect(Math.abs(x1 - x0) < 1e-9 || Math.abs(z1 - z0) < 1e-9).toBe(true)
      }
  })
})

describe('pathOf', () => {
  it('d’un Service : ses portes, ses pods et leurs volumes', () => {
    const p = pathOf('service:production/db', links)
    expect([...p].sort()).toEqual(['pod:db', 'service:production/db', 'volume:production/data-db-0'])
    expect([...pathOf('service:production/api', links)]).toEqual(expect.arrayContaining(['gate:nginx', 'pod:a', 'pod:b']))
  })

  it('d’une porte : ses Services puis leurs pods', () => {
    expect([...pathOf('gate:nginx', links)]).toEqual(expect.arrayContaining(['service:production/api', 'pod:a', `route:${routeKey(shop)}`]))
  })

  it('d’un pod : ses Services, leurs portes et ses volumes', () => {
    expect([...pathOf('pod:db', links)].sort()).toEqual(['pod:db', 'service:production/db', 'volume:production/data-db-0'])
    expect([...pathOf('pod:a', links)]).toEqual(expect.arrayContaining(['service:production/api', 'gate:nginx']))
  })

  it('un lien est allumé quand tous ses objets (hors routes) sont sur le chemin', () => {
    const p = pathOf('pod:a', links)
    expect(isLit(fam('main')[0], p)).toBe(true)
    expect(isLit(fam('data')[0], p)).toBe(false)
  })
})
```

Run: `npx vitest run src/scene/links.test.ts`
Expected: FAIL.

- [ ] **Step 2 : implémentation**

Créer `web/src/scene/links.ts` :

```ts
import { routeKey, serviceKey, volumeKey, type Pod, type Route, type Service, type Volume } from '../api/types'
import { ALLEY, type CityLayout } from './layout'
import type { NetLayout } from './netLayout'

// Liens au sol, tracés en angles droits par les avenues, la rue ouest (portes),
// la rue est (entrepôts) et l'allée à l'est de chaque parcelle : jamais à
// travers un bâtiment.

export type Family = 'main' | 'broken' | 'data' | 'fibre'
export type Pt = [number, number]

export interface Link {
  family: Family
  points: Pt[]
  /** Objets reliés, « type:clé » : gate:, route:, service:, pod:, volume:. */
  keys: string[]
  /** Paquets ou gouttes qui circulent (endpoint ready, pod Running). */
  live: boolean
  /** Route cassée : position du panneau « ? ». */
  sign?: Pt
}

export interface LinkInput {
  city: CityLayout
  net: NetLayout
  services: Service[]
  routes: Route[]
  volumes: Volume[]
  pods: ReadonlyMap<string, Pod>
  targets: ReadonlyMap<string, { x: number; z: number; onNode: boolean }>
}

const dedupe = (pts: Pt[]): Pt[] =>
  pts.filter((p, i) => i === 0 || Math.abs(p[0] - pts[i - 1][0]) + Math.abs(p[1] - pts[i - 1][1]) > 1e-6)

function nearestAvenue(city: CityLayout, z: number): number {
  let best = 0
  city.avenues.forEach((a, i) => { if (Math.abs(a.z - z) < Math.abs(city.avenues[best].z - z)) best = i })
  return best
}

/** De la voie laneZ à l'allée à l'est de la parcelle, puis au pod. */
function toPod(city: CityLayout, laneZ: number, pod: { x: number; z: number }, plot: { x: number }, offset: number): Pt[] {
  const alley = plot.x + city.geometry.width / 2 + ALLEY / 2 + offset
  return [[alley, laneZ], [alley, pod.z], [pod.x, pod.z]]
}

export function buildLinks(i: LinkInput): Link[] {
  const { city, net } = i
  const out: Link[] = []
  const anchor = (uid: string) => {
    const t = i.targets.get(uid), p = i.pods.get(uid)
    if (!t || !t.onNode || !p) return null
    const plot = city.plots.get(p.nodeName)
    return plot ? { t, p, plot, avenue: nearestAvenue(city, plot.z) } : null
  }

  // Lignes principales : une par couple (porte, Service), toutes routes confondues.
  const mains = new Map<string, Link>()
  for (const r of i.routes) {
    const gate = net.gates.get(r.gate)
    if (!gate) continue
    const rk = `route:${routeKey(r)}`
    for (const rule of r.rules) {
      const b = rule.backend
      if (b.kind !== 'Service') continue
      const sk = `${b.namespace}/${b.service}`
      if (b.state === 'missing') {
        const seg = net.segments.find((s) => s.ns === b.namespace)
        const lane = net.lanes[seg?.avenue ?? 0]
        const x = seg ? seg.x0 : net.westX + 1.2
        out.push({ family: 'broken', keys: [`gate:${r.gate}`, rk], live: false, sign: [x, lane.main],
          points: dedupe([[gate.x + 0.3, gate.z], [net.westX, gate.z], [net.westX, lane.main], [x, lane.main]]) })
        continue
      }
      const relay = net.relays.get(sk)
      if (!relay) continue
      const id = `${r.gate}|${sk}`
      const prev = mains.get(id)
      if (prev) {
        if (!prev.keys.includes(rk)) prev.keys.push(rk)
        continue
      }
      const lane = net.lanes[relay.avenue]
      mains.set(id, { family: 'main', live: true, keys: [`gate:${r.gate}`, `service:${sk}`, rk],
        points: dedupe([[gate.x + 0.3, gate.z], [net.westX, gate.z], [net.westX, lane.main], [relay.x, lane.main], [relay.x, relay.z]]) })
    }
  }
  out.push(...mains.values())

  // Fibres : du relais à chaque pod endpoint placé sur un node.
  for (const s of i.services) {
    const relay = net.relays.get(serviceKey(s))
    if (!relay) continue
    for (const e of s.endpoints) {
      const a = anchor(e.podUID)
      if (!a) continue
      const lr = net.lanes[relay.avenue], la = net.lanes[a.avenue]
      const head: Pt[] = relay.avenue === a.avenue
        ? [[relay.x, relay.z], [relay.x, lr.fibre]]
        : [[relay.x, relay.z], [relay.x, lr.fibre], [net.westX, lr.fibre], [net.westX, la.fibre]]
      out.push({ family: 'fibre', live: e.ready, keys: [`service:${serviceKey(s)}`, `pod:${e.podUID}`],
        points: dedupe([...head, ...toPod(city, la.fibre, a.t, a.plot, -0.2)]) })
    }
  }

  // Conduites : de la citerne aux pods qui montent le PVC, par la rue est.
  for (const v of i.volumes) {
    const tank = net.tanks.get(volumeKey(v))
    if (!tank) continue
    for (const uid of v.pods) {
      const a = anchor(uid)
      if (!a) continue
      const la = net.lanes[a.avenue]
      out.push({ family: 'data', live: a.p.displayStatus === 'Running', keys: [`volume:${volumeKey(v)}`, `pod:${uid}`],
        points: dedupe([[tank.x, tank.z], [net.eastX, tank.z], [net.eastX, la.data], ...toPod(city, la.data, a.t, a.plot, 0.2)]) })
    }
  }
  return out
}

const typeOf = (k: string) => k.slice(0, k.indexOf(':'))

/**
 * Chemin d'un objet (« type:clé ») : ce qui s'allume quand on le sélectionne.
 * Porte ou route → Services → pods → volumes ; Service → portes, pods →
 * volumes ; pod → Services → portes, et volumes ; volume → pods → Services.
 */
export function pathOf(sel: string, links: Link[]): Set<string> {
  const out = new Set([sel])
  const via = (from: Set<string>, fams: Family[]) => {
    const added = new Set<string>()
    for (const l of links)
      if (fams.includes(l.family) && l.keys.some((k) => from.has(k)))
        for (const k of l.keys) if (!out.has(k)) { out.add(k); added.add(k) }
    return added
  }
  const only = (s: Set<string>, type: string) => new Set([...s].filter((k) => typeOf(k) === type))
  const self = new Set([sel])
  switch (typeOf(sel)) {
    case 'gate':
    case 'route': {
      const svcs = only(via(self, ['main', 'broken']), 'service')
      via(only(via(svcs, ['fibre']), 'pod'), ['data'])
      break
    }
    case 'service':
      via(self, ['main'])
      via(only(via(self, ['fibre']), 'pod'), ['data'])
      break
    case 'pod':
      via(only(via(self, ['fibre']), 'service'), ['main'])
      via(self, ['data'])
      break
    case 'volume':
      via(only(via(self, ['data']), 'pod'), ['fibre'])
      break
  }
  return out
}

/** Lien allumé : tous ses objets, routes exceptées, sont sur le chemin. */
export const isLit = (l: Link, path: ReadonlySet<string>) => l.keys.every((k) => k.startsWith('route:') || path.has(k))
```

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run src/scene/links.test.ts`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add web/src/scene/links.ts web/src/scene/links.test.ts
git commit -m "feat(web): liens au sol (lignes principales, fibres, conduites) et chemin d'un objet"
```

### Task 17 : le monde dérivé connaît le réseau

**Files:**
- Modify: `web/src/scene/world.ts`, `web/src/scene/podView.ts` (`isNamespaceVisible`)
- Test: `web/src/scene/world.test.ts`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `web/src/scene/world.test.ts` (imports `route`, `service`, `volume` depuis `../store/fixtures`, `DEFAULT_VIEW` depuis `./podView`) :

```ts
describe('réseau et stockage', () => {
  const st = (version: number, extra: Partial<{ hideSystem: boolean }> = {}) => ({
    ...state(version, [node()], [pod({ uid: 'u1' })]),
    services: new Map([['production/api', service()], ['kube-system/kube-dns', service({ namespace: 'kube-system', name: 'kube-dns', endpoints: [] })]]),
    routes: new Map([['Ingress/production/storefront', route()]]),
    volumes: new Map([['production/data-0', volume()]]),
    podView: { ...DEFAULT_VIEW, hideSystem: extra.hideSystem ?? true },
  })

  it('dispose relais, portes et citernes, et trace les liens', () => {
    const w = new World()
    w.update(st(1))
    expect([...w.net!.relays.keys()]).toEqual(['production/api']) // kube-system masqué
    expect([...w.net!.gates.keys()]).toEqual(['nginx'])
    expect(w.net!.tanks.has('production/data-0')).toBe(true)
    expect(w.links.map((l) => l.family).sort()).toEqual(['data', 'fibre', 'main'])
    expect(w.layout!.bounds.width).toBeGreaterThan(0)
    const w2 = new World()
    w2.update(st(1, { hideSystem: false }))
    expect(w2.net!.relays.has('kube-system/kube-dns')).toBe(true)
  })

  it('estompe hors du chemin d’une sélection réseau, pas d’une sélection de pod', () => {
    const w = new World()
    w.update(st(1))
    const f = w.focusFor({ type: 'service', key: 'production/api', name: 'api' }, null, null)
    expect(f.dim).toBe(true)
    expect(f.path!.has('pod:u1')).toBe(true)
    expect(w.focusFor({ type: 'pod', key: 'u1', name: 'x' }, null, null).dim).toBe(false)
    expect(w.focusFor(null, 'gate:nginx', null).hover!.has('service:production/api')).toBe(true)
    expect(w.focusFor(null, null, null)).toEqual({ path: null, hover: null, dim: false })
  })

  it('situe un objet réseau', () => {
    const w = new World()
    w.update(st(1))
    expect(w.positionOf('service', 'production/api')).toEqual(expect.objectContaining({ x: expect.any(Number) }))
    expect(w.positionOf('route', 'Ingress/production/storefront')).toEqual(w.positionOf('gate', 'nginx'))
    expect(w.positionOf('volume', 'absent')).toBeNull()
  })
})
```

Run: `npx vitest run src/scene/world.test.ts`
Expected: FAIL.

- [ ] **Step 2 : implémentation**

`web/src/scene/podView.ts` : extraire

```ts
/** Namespace affiché : les namespaces système sont masqués, sauf s'ils sont choisis dans la légende. */
export const isNamespaceVisible = (ns: string, view: PodView, nsFilter: string | null = null) =>
  !(view.hideSystem && SYSTEM_NAMESPACES.has(ns) && ns !== nsFilter)
```

et dans `isVisible`, remplacer la ligne `hideSystem` par `if (!isNamespaceVisible(p.namespace, view, nsFilter)) return false`.

`web/src/scene/world.ts` :

1. Imports : `routeKey, serviceKey, volumeKey, type Route, type Service, type Volume` depuis `../api/types` ; `type Selection, type SelectionType` depuis `../store/cluster` ; `gatesOf, type Gate` depuis `../store/net` ; `buildLinks, pathOf, type Link` depuis `./links` ; `layoutNetwork, type NetLayout` depuis `./netLayout` ; `isNamespaceVisible` depuis `./podView` ; `type CityLayout` est déjà importé.
2. Types :

```ts
type WorldInput = Pick<ClusterState, 'version' | 'nodes' | 'pods' | 'namespaces'>
  & Partial<Pick<ClusterState, 'services' | 'routes' | 'volumes'>>
  & { podView?: PodView; nsFilter?: string | null }

export interface Focus {
  /** Chemin de la sélection (« type:clé »), null sans sélection autre qu'un node. */
  path: Set<string> | null
  /** Chemin de l'objet survolé : ses fibres s'affichent, sans rien estomper. */
  hover: Set<string> | null
  /** Estomper ce qui n'est pas sur le chemin : sélection d'une porte, d'une route, d'un Service ou d'un PVC. */
  dim: boolean
}

const NO_FOCUS: Focus = { path: null, hover: null, dim: false }
const NET_SELECTIONS: ReadonlySet<SelectionType> = new Set(['service', 'route', 'volume', 'gate'])
```

3. Champs de `World` :

```ts
  services: Service[] = []
  routes: Route[] = []
  volumes: Volume[] = []
  gates: Gate[] = []
  net: NetLayout | null = null
  links: Link[] = []
  private city: CityLayout | null = null
  private netKey = ''
  private focusKey = ''
  private focusVal: Focus = NO_FOCUS
```

4. Dans `update`, le `viewKey` couvre aussi le filtre de namespace pour le réseau : `const viewKey = \`${view.sort}|${view.hideSystem}|${view.hiddenKinds.join(',')}|${nsFilter}\``. Remplacer le calcul de la ville par :

```ts
    if (this.capacity && key !== this.layoutKey) {
      this.layoutKey = key
      this.city = layoutCity(this.nodes, plotGeometry(this.capacity))
      this.layout = this.city
      this.netKey = '' // la ville a changé : le réseau est à redisposer
    }
    this.colors = clusterColors(st)
    this.placePods(view, st.nodes)
    this.placeNetwork(st, view, nsFilter)
    return true
```

5. Méthodes :

```ts
  private placeNetwork(st: WorldInput, view: PodView, nsFilter: string | null) {
    const shown = (ns: string) => isNamespaceVisible(ns, view, nsFilter)
    this.services = [...(st.services?.values() ?? [])].filter((s) => shown(s.namespace))
    this.routes = [...(st.routes?.values() ?? [])].filter((r) => shown(r.namespace))
    this.volumes = [...(st.volumes?.values() ?? [])].filter((v) => shown(v.namespace))
    this.gates = gatesOf(this.routes)
    const city = this.city
    if (!city) {
      this.net = null
      this.links = []
      return
    }
    const key = [
      this.services.map(serviceKey).sort().join(','),
      this.gates.map((g) => g.name).join(','),
      this.volumes.map((v) => `${volumeKey(v)}@${v.storageClass}@${v.requested}`).sort().join(','),
    ].join('|')
    if (key !== this.netKey) {
      this.netKey = key
      this.net = layoutNetwork(city,
        this.services.map((s) => ({ key: serviceKey(s), namespace: s.namespace, name: s.name })),
        this.gates.map((g) => g.name),
        this.volumes.map((v) => ({ key: volumeKey(v), namespace: v.namespace, name: v.name, storageClass: v.storageClass, requested: v.requested })))
      // La ville s'agrandit des entrepôts (cadrage de la caméra, sol, arbres).
      this.layout = { ...city, bounds: this.net.bounds }
    }
    this.links = buildLinks({ city, net: this.net!, services: this.services, routes: this.routes, volumes: this.volumes, pods: st.pods, targets: this.targets })
  }

  /** Chemin à allumer pour la sélection et le survol courants (mis en cache). */
  focusFor(sel: Selection, hoverNet: string | null, hoverPod: string | null): Focus {
    const selKey = sel && sel.type !== 'node' ? `${sel.type}:${sel.key}` : null
    const hoverKey = hoverNet ?? (hoverPod ? `pod:${hoverPod}` : null)
    const key = `${this.version}|${this.viewKey}|${selKey}|${hoverKey}`
    if (key === this.focusKey) return this.focusVal
    this.focusKey = key
    this.focusVal = !selKey && !hoverKey ? NO_FOCUS : {
      path: selKey ? pathOf(selKey, this.links) : null,
      hover: hoverKey ? pathOf(hoverKey, this.links) : null,
      dim: !!sel && NET_SELECTIONS.has(sel.type),
    }
    return this.focusVal
  }

  /** Point de la ville où se trouve un objet (recherche, marqueur de sélection). */
  positionOf(type: SelectionType, key: string): { x: number; z: number } | null {
    const net = this.net
    const at = (p: { x: number; z: number } | undefined) => (p ? { x: p.x, z: p.z } : null)
    switch (type) {
      case 'service': return at(net?.relays.get(key))
      case 'gate': return at(net?.gates.get(key))
      case 'volume': return at(net?.tanks.get(key))
      case 'route': {
        const r = this.routes.find((x) => routeKey(x) === key)
        return r ? at(net?.gates.get(r.gate)) : null
      }
      case 'node': return at(this.layout?.plots.get(key))
      case 'pod': return at(podPositions.get(key) ?? this.targets.get(key))
    }
  }
```

Note : `focusFor` lit `this.viewKey`, champ privé de la classe : aucun changement de visibilité nécessaire.

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add web/src/scene
git commit -m "feat(web): le monde dispose le réseau, trace les liens et calcule le chemin sélectionné"
```

---

## Phase C — Scène

Les composants de scène ne sont pas testés unitairement (WebGL) : leur logique vit dans `netLayout.ts`, `links.ts` et `world.ts`, déjà couverts. Chaque tâche se vérifie par `npx tsc --noEmit`, `npx vitest run` et un coup d'œil en mode démo (`make dev-demo` depuis la racine, http://localhost:5173).

### Task 18 : avenues, tronçons et entrepôts au sol

**Files:**
- Modify: `web/src/scene/City.tsx`

- [ ] **Step 1 : implémentation**

Dans `City` :

1. S'abonner aussi au flux et aux réglages (le réseau change avec les Services) :

```tsx
  useCluster((s) => s.version)
  useCluster((s) => s.podView)
  useCluster((s) => s.nsFilter)
```

juste après la ligne `nodeKey`, puis `const net = world.net` après `const layout = world.layout`.

2. Après le `map` des quartiers, ajouter :

```tsx
      {layout.avenues.map((a, i) => (
        <Plane key={`avenue-${i}`} w={a.width} d={a.depth} x={a.x} z={a.z} y={0.012} color={theme.avenue} />
      ))}
      {net?.segments.map((s, i) => {
        const lanes = net.lanes[s.avenue]
        const w = s.x1 - s.x0
        return (
          <group key={`segment-${i}`}>
            <Plane w={w - 0.1} d={0.22} x={(s.x0 + s.x1) / 2} z={lanes.north + 0.13} y={0.016} color={world.colors.get(s.ns) ?? theme.muted} />
            <GroundLabel text={s.ns} w={Math.max(1.2, w - 0.1)} h={0.6} x={(s.x0 + s.x1) / 2} z={lanes.label} align="center" size={30} theme={theme} />
          </group>
        )
      })}
      {net?.warehouse && (
        <Plane w={net.warehouse.width} d={net.warehouse.depth} x={net.warehouse.x} z={net.warehouse.z} y={0.01} color={theme.warehouse} />
      )}
      {net?.islands.map((is) => (
        <group key={`island-${is.storageClass}`}>
          <Plane w={is.width} d={is.depth} x={is.x} z={is.z} y={0.014} color={theme.platform} />
          <GroundLabel text={is.storageClass} w={is.width - 0.4} h={0.8} x={is.x} z={is.z - is.depth / 2 + 0.5} align="center" size={34} theme={theme} />
        </group>
      ))}
```

- [ ] **Step 2 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS. En démo : une avenue grise sous chaque rangée de quartiers (devant la ville s'il n'y en a qu'une), des bandes colorées et des noms de namespace dessus, un quartier Entrepôts beige à l'est avec deux îlots (`premium-rwo`, `standard-rwo`).

- [ ] **Step 3 : commit**

```bash
git add web/src/scene/City.tsx
git commit -m "feat(web): avenues, tronçons de namespace et quartier Entrepôts au sol"
```

### Task 19 : relais, portes et citernes instanciés

**Files:**
- Create: `web/src/scene/Network.tsx`, `web/src/scene/tick.ts`
- Modify: `web/src/scene/Stacks.tsx` (texture de pastille réutilisable), `web/src/scene/pick.ts`, `web/src/scene/Scene.tsx`

- [ ] **Step 1 : utilitaires**

Créer `web/src/scene/tick.ts` :

```ts
// Animations lentes (paquets, gouttes, voyants) : 15 images/s au lieu de 60,
// pour que la ville au repos ne garde pas le GPU occupé.
let pending = false

export function tick(invalidate: () => void) {
  if (pending) return
  pending = true
  setTimeout(() => {
    pending = false
    invalidate()
  }, 66)
}
```

Dans `web/src/scene/Stacks.tsx`, remplacer `counterTexture` par une pastille de largeur variable, exportée :

```ts
/** Pastille de texte (compteur « ×12 », nom de porte) ; aspect = largeur / hauteur. */
export function pillTexture(text: string, theme: Theme): { tex: THREE.CanvasTexture; aspect: number } {
  const key = `${text}|${theme.ink}|${theme.bg}`
  let hit = cache.get(key)
  if (hit) return hit
  const c = document.createElement('canvas')
  c.height = 64
  c.width = Math.max(128, 40 + text.length * 19)
  const g = c.getContext('2d')!
  g.fillStyle = theme.ink
  g.beginPath()
  g.roundRect(4, 8, c.width - 8, 48, 24)
  g.fill()
  g.fillStyle = theme.bg
  g.font = `700 32px ${theme.font}`
  g.textAlign = 'center'
  g.textBaseline = 'middle'
  g.fillText(text, c.width / 2, 33)
  const tex = new THREE.CanvasTexture(c)
  tex.colorSpace = THREE.SRGBColorSpace
  hit = { tex, aspect: c.width / c.height }
  if (cache.size > 200) cache.clear()
  cache.set(key, hit)
  return hit
}
```

(`cache` devient `new Map<string, { tex: THREE.CanvasTexture; aspect: number }>()` ; dans `Stacks`, `tex: pillTexture(\`×${st.count}\`, theme).tex`.)

Dans `web/src/scene/pick.ts` :

```ts
/** Correspondance instance → objet Kubernetes, renseignée par Pods, Buildings et Network. */
export const pickables = {
  pods: { meshes: [] as THREE.InstancedMesh[], uids: [] as string[] },
  nodes: { meshes: [] as THREE.InstancedMesh[], names: [] as string[] },
  /** Relais, portes et citernes : une liste de clés « type:clé » par mesh. */
  net: [] as { mesh: THREE.InstancedMesh; keys: string[] }[],
}
```

- [ ] **Step 2 : composant**

Créer `web/src/scene/Network.tsx` :

```tsx
import { useFrame } from '@react-three/fiber'
import { useEffect, useMemo, useRef, useState } from 'react'
import * as THREE from 'three'
import { serviceKey, volumeKey } from '../api/types'
import { useCluster } from '../store/cluster'
import { gateSignal, healthSignal, volumeSignal, worst, type Signal } from './health'
import { Part, opacityBasicMaterial, opacityMaterial, roundCapacity } from './instanced'
import { pickables } from './pick'
import { LOD_PX } from './Pods'
import { pillTexture } from './Stacks'
import type { Theme } from './theme'
import { tick } from './tick'
import { world } from './world'

// Infrastructure de la ville : relais (Services) sur les avenues, portes
// (contrôleurs d'entrée) à l'ouest, citernes (PVC) dans les entrepôts. Une
// InstancedMesh par pièce : quelques draw calls quel que soit le nombre d'objets.
// Vu de loin, un relais par tronçon (pire voyant du groupe) avec un compteur.

const up = (g: THREE.BufferGeometry, h: number) => g.translate(0, h / 2, 0)
const GEOMETRY = {
  relayRing: up(new THREE.CylinderGeometry(0.5, 0.5, 0.1, 24), 0.1),
  relayBase: up(new THREE.CylinderGeometry(0.42, 0.42, 0.2, 24), 0.2),
  beacon: up(new THREE.BoxGeometry(0.2, 0.28, 0.2), 0.28),
  signPost: up(new THREE.BoxGeometry(0.08, 0.9, 0.08), 0.9),
  signPanel: up(new THREE.BoxGeometry(0.7, 0.32, 0.06), 0.32),
  gatePost: up(new THREE.BoxGeometry(0.35, 2.1, 0.35), 2.1),
  gateLintel: up(new THREE.BoxGeometry(0.42, 0.32, 1), 0.32),
  tankBody: up(new THREE.CylinderGeometry(1, 1, 1, 28), 1),
  tankCap: up(new THREE.CylinderGeometry(1.02, 1.02, 0.08, 28), 0.08),
}
type PartName = keyof typeof GEOMETRY
const NAMES = Object.keys(GEOMETRY) as PartName[]
/** Pièces lumineuses (non éclairées) : voyants. */
const GLOW: ReadonlySet<PartName> = new Set(['beacon', 'tankCap'])
const PICKABLE: PartName[] = ['relayRing', 'relayBase', 'signPanel', 'gatePost', 'gateLintel', 'tankBody']
const TANK_H = 1.4
const GATE_SPAN = 1.9 // écart entre les piliers d'une porte, le long de z
const WHITE = new THREE.Color('#ffffff')

interface RelayItem { key: string; ns: string; x: number; z: number; sig: Signal; external: boolean }

export function Network({ theme, reducedMotion }: { theme: Theme; reducedMotion: boolean }) {
  const group = useRef<THREE.Group>(null)
  const materials = useMemo(() => ({
    solid: opacityMaterial({ color: '#ffffff', roughness: 0.55, metalness: 0.1 }),
    glow: opacityBasicMaterial({ color: '#ffffff' }),
  }), [])
  const parts = useRef(new Map<PartName, Part>())
  const capacity = useRef(0)
  const [far, setFar] = useState(false)
  useCluster((s) => s.version)
  world.update(useCluster.getState())

  const colors = useMemo(() => ({
    platform: new THREE.Color(theme.platform),
    muted: new THREE.Color(theme.muted),
    accent: new THREE.Color(theme.accent),
    tank: new THREE.Color(theme.dark ? '#5E6E75' : '#B9C6CC'),
    signal: {
      ok: new THREE.Color(theme.ok), warn: new THREE.Color(theme.warn), err: new THREE.Color(theme.err), mute: new THREE.Color(theme.muted),
    } as Record<Signal, THREE.Color>,
  }), [theme])
  const tmp = useMemo(() => ({ m: new THREE.Matrix4(), q: new THREE.Quaternion(), p: new THREE.Vector3(), s: new THREE.Vector3(), c: new THREE.Color() }), [])

  useEffect(() => () => {
    parts.current.forEach((p) => p.dispose())
    materials.solid.dispose()
    materials.glow.dispose()
  }, [materials])

  const ensure = (n: number) => {
    if (n <= capacity.current) return
    parts.current.forEach((p) => { group.current?.remove(p.mesh); p.dispose() })
    capacity.current = roundCapacity(n, 64)
    parts.current = new Map(NAMES.map((name) => [name, new Part(GEOMETRY[name], GLOW.has(name) ? materials.glow : materials.solid,
      capacity.current, { opacity: true, name, castShadow: !GLOW.has(name) })]))
    parts.current.forEach((p) => group.current?.add(p.mesh))
  }

  useFrame(({ camera, clock, invalidate }) => {
    const st = useCluster.getState()
    world.update(st)
    const net = world.net
    const isFar = camera.zoom < LOD_PX
    if (isFar !== far) setFar(isFar)
    ensure(Math.max(1, (net?.relays.size ?? 0) + 2 * (net?.gates.size ?? 0) + (net?.tanks.size ?? 0)))
    const P = parts.current
    const counts = new Map<PartName, number>()
    const keys = new Map<PartName, string[]>(PICKABLE.map((n) => [n, []]))
    const { m, q, p, s, c } = tmp
    const put = (name: PartName, x: number, y: number, z: number, sx: number, sy: number, sz: number, color: THREE.Color, opacity: number, key = '') => {
      const part = P.get(name)!
      const i = counts.get(name) ?? 0
      p.set(x, y, z)
      s.set(sx, sy, sz)
      part.setMatrix(i, m.compose(p, q, s))
      part.setColor(i, color)
      part.setOpacity(i, opacity)
      counts.set(name, i + 1)
      keys.get(name)?.push(key)
    }
    const focus = world.focusFor(st.selection, st.hoverNet, st.hover?.uid ?? null)
    const selKey = st.selection ? `${st.selection.type}:${st.selection.key}` : ''
    const alpha = (key: string, ns?: string) =>
      st.nsFilter && ns && ns !== st.nsFilter ? 0.1 : focus.dim && focus.path && key && !focus.path.has(key) ? 0.25 : 1
    const still = reducedMotion || document.hidden
    const t = clock.elapsedTime
    let blinking = false

    if (net) {
      // Relais.
      const byKey = new Map(world.services.map((sv) => [serviceKey(sv), sv]))
      const items: RelayItem[] = []
      if (isFar) {
        const agg = new Map<string, { ns: string; x0: number; x1: number; z: number; sigs: Signal[] }>()
        for (const [k, r] of net.relays) {
          const sv = byKey.get(k)
          if (!sv) continue
          const id = `${r.ns}|${r.avenue}`
          const a = agg.get(id) ?? { ns: r.ns, x0: r.x, x1: r.x, z: r.z, sigs: [] }
          a.x0 = Math.min(a.x0, r.x)
          a.x1 = Math.max(a.x1, r.x)
          a.sigs.push(healthSignal(sv.health))
          agg.set(id, a)
        }
        for (const a of agg.values()) items.push({ key: '', ns: a.ns, x: (a.x0 + a.x1) / 2, z: a.z, sig: worst(a.sigs), external: false })
      } else {
        const groupSigs = new Map<string, Signal[]>()
        for (const [k, r] of net.relays) {
          const sv = byKey.get(k)
          if (!sv) continue
          if (r.group) { groupSigs.set(r.group, [...(groupSigs.get(r.group) ?? []), healthSignal(sv.health)]); continue }
          items.push({ key: `service:${k}`, ns: r.ns, x: r.x, z: r.z, sig: healthSignal(sv.health), external: sv.type === 'ExternalName' })
        }
        for (const g of net.groups) items.push({ key: '', ns: g.ns, x: g.x, z: g.z, sig: worst(groupSigs.get(g.key) ?? []), external: false })
      }
      for (const it of items) {
        const a = alpha(it.key, it.ns)
        c.set(world.colors.get(it.ns) ?? '#7D8A94')
        if (it.sig === 'err' && !still) blinking = true
        const beacon = it.sig === 'err' && !still && Math.sin(t * 6) < 0 ? colors.muted : colors.signal[it.sig]
        if (it.external) {
          put('signPost', it.x, 0, it.z, 1, 1, 1, colors.muted, a)
          put('signPanel', it.x, 0.9, it.z, 1, 1, 1, c, a, it.key)
          continue
        }
        put('relayRing', it.x, 0, it.z, 1, 1, 1, c, a, it.key)
        put('relayBase', it.x, 0.1, it.z, 1, 1, 1, it.key && it.key === selKey ? colors.accent : colors.platform, a, it.key)
        put('beacon', it.x, 0.3, it.z, 1, 1, 1, beacon, a)
      }

      // Portes : deux piliers et un linteau ; orange si une de leurs routes est cassée.
      for (const g of world.gates) {
        const slot = net.gates.get(g.name)
        if (!slot) continue
        const key = `gate:${g.name}`
        const a = alpha(key)
        c.copy(gateSignal(g) === 'warn' ? colors.signal.warn : colors.accent)
        if (key === selKey) c.lerp(WHITE, 0.35)
        put('gatePost', slot.x, 0, slot.z - GATE_SPAN / 2, 1, 1, 1, c, a, key)
        put('gatePost', slot.x, 0, slot.z + GATE_SPAN / 2, 1, 1, 1, c, a, key)
        put('gateLintel', slot.x, 2.1, slot.z, 1, 1, GATE_SPAN + 0.35, c, a, key)
      }

      // Citernes : grises si Bound, translucides orange si Pending, rouges si Lost.
      for (const v of world.volumes) {
        const slot = net.tanks.get(volumeKey(v))
        if (!slot) continue
        const key = `volume:${volumeKey(v)}`
        const sig = volumeSignal(v)
        const a = alpha(key, v.namespace)
        const body = key === selKey ? colors.accent : sig === 'ok' ? colors.tank : colors.signal[sig]
        put('tankBody', slot.x, 0, slot.z, slot.r, TANK_H, slot.r, body, sig === 'warn' ? a * 0.45 : a, key)
        put('tankCap', slot.x, TANK_H, slot.z, slot.r, 1, slot.r, colors.signal[sig], a)
      }
    }

    P.forEach((part, name) => part.commit(counts.get(name) ?? 0))
    pickables.net = PICKABLE.map((name) => ({ mesh: P.get(name)!.mesh, keys: keys.get(name)! }))
    if (blinking) tick(invalidate)
  })

  // Libellés des portes, compteurs des blocs regroupés (de près) ou des tronçons (de loin).
  const net = world.net
  const counters: { id: string; x: number; z: number; n: number }[] = []
  if (net && !far) for (const g of net.groups) counters.push({ id: g.key, x: g.x, z: g.z, n: g.members.length })
  if (net && far) {
    const agg = new Map<string, { x0: number; x1: number; z: number; n: number }>()
    for (const r of net.relays.values()) {
      const id = `${r.ns}|${r.avenue}`
      const a = agg.get(id) ?? { x0: r.x, x1: r.x, z: r.z, n: 0 }
      a.x0 = Math.min(a.x0, r.x)
      a.x1 = Math.max(a.x1, r.x)
      a.n++
      agg.set(id, a)
    }
    for (const [id, a] of agg) if (a.n > 1) counters.push({ id, x: (a.x0 + a.x1) / 2, z: a.z, n: a.n })
  }

  return (
    <>
      <group ref={group} />
      {net && [...net.gates.values()].map((g) => {
        const pill = pillTexture(g.name, theme)
        return (
          <sprite key={`gate-${g.name}`} position={[g.x, 3.0, g.z]} scale={[0.35 * pill.aspect, 0.35, 1]} raycast={() => null} renderOrder={10}>
            <spriteMaterial map={pill.tex} depthTest={false} transparent />
          </sprite>
        )
      })}
      {counters.map((k) => (
        <sprite key={`count-${k.id}`} position={[k.x, 1.2, k.z]} scale={[0.7, 0.35, 1]} raycast={() => null} renderOrder={10}>
          <spriteMaterial map={pillTexture(`×${k.n}`, theme).tex} depthTest={false} transparent />
        </sprite>
      ))}
    </>
  )
}
```

Dans `web/src/scene/Scene.tsx`, importer `Network` et l'ajouter après `<Buildings theme={theme} />` :

```tsx
      <Network theme={theme} reducedMotion={reducedMotion} />
```

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS. En démo : deux portes bleues `nginx` et `traefik` à l'ouest de l'avenue, un relais par Service bordé de la couleur de son namespace (voyant rouge clignotant pour `checkout-preview`, orange pour un Service dont une partie des pods n'est pas ready), un panneau pour `stripe-api`, quatre citernes dont `uploads-preview` translucide orange. En dézoomant au maximum, un relais par tronçon avec un compteur.

- [ ] **Step 4 : commit**

```bash
git add web/src/scene
git commit -m "feat(web): relais, portes et citernes instanciés, regroupés vus de loin"
```

### Task 20 : liens au sol animés

**Files:**
- Create: `web/src/scene/Links.tsx`
- Modify: `web/src/scene/Pods.tsx` (export de `questionTexture`), `web/src/scene/Scene.tsx`
- Test: `web/src/scene/ribbon.test.ts`

- [ ] **Step 1 : test du ruban (qui échoue)**

Créer `web/src/scene/ribbon.test.ts` :

```ts
import { describe, expect, it } from 'vitest'
import { ribbon } from './Links'

describe('ribbon', () => {
  it('quatre sommets et deux triangles par segment, abscisse curviligne continue', () => {
    const r = ribbon([{ points: [[0, 0], [2, 0], [2, 3]], alpha: 0.5, live: true }], 0.2, 0.03)
    expect(r.position.length).toBe(2 * 4 * 3)
    expect(r.index.length).toBe(2 * 6)
    expect([...r.dist]).toEqual([0, 0, 2, 2, 2, 2, 5, 5])
    expect(r.alpha[0]).toBe(0.5)
    expect(r.live[7]).toBe(1)
    expect(r.position[1]).toBeCloseTo(0.03)
  })

  it('accepte des liens sans segment', () => {
    expect(ribbon([{ points: [[1, 1]], alpha: 1, live: false }], 0.1, 0).index.length).toBe(0)
  })
})
```

Run: `npx vitest run src/scene/ribbon.test.ts`
Expected: FAIL (module absent).

- [ ] **Step 2 : implémentation**

Dans `web/src/scene/Pods.tsx`, exporter `questionTexture` (`export function questionTexture()`).

Créer `web/src/scene/Links.tsx` :

```tsx
import { useFrame } from '@react-three/fiber'
import { useEffect, useMemo, useRef } from 'react'
import * as THREE from 'three'
import { useCluster } from '../store/cluster'
import { isLit, type Family } from './links'
import { LOD_PX, questionTexture } from './Pods'
import type { Theme } from './theme'
import { tick } from './tick'
import { world } from './world'

// Liens au sol : rubans plats (deux triangles par segment) sur lesquels un
// shader fait défiler des paquets (réseau) ou des gouttes (data). Une
// géométrie par famille, reconstruite seulement quand les liens, la sélection,
// le survol ou le niveau de détail changent.

const WIDTH: Record<Family, number> = { main: 0.12, broken: 0.1, fibre: 0.08, data: 0.16 }
const Y: Record<Family, number> = { main: 0.03, broken: 0.031, fibre: 0.032, data: 0.033 }
/** Motif : période (unités monde), part allumée, intensité entre deux paquets, vitesse. */
const DASH: Record<Family, { period: number; duty: number; base: number; speed: number }> = {
  main: { period: 1.2, duty: 0.3, base: 0.45, speed: 1.6 },
  fibre: { period: 0.9, duty: 0.35, base: 0.5, speed: 1.4 },
  data: { period: 0.7, duty: 0.25, base: 0.2, speed: 0.8 },
  broken: { period: 0.5, duty: 0.55, base: 0, speed: 0 },
}
const FAMILIES: Family[] = ['main', 'broken', 'fibre', 'data']

export interface Ribbon {
  position: Float32Array
  dist: Float32Array
  alpha: Float32Array
  live: Float32Array
  index: Uint32Array
}

/** Rubans de largeur w le long des polylignes ; dist : abscisse curviligne (motif continu aux coudes). */
export function ribbon(items: { points: [number, number][]; alpha: number; live: boolean }[], w: number, y: number): Ribbon {
  let segs = 0
  for (const it of items) segs += Math.max(0, it.points.length - 1)
  const position = new Float32Array(segs * 12), dist = new Float32Array(segs * 4)
  const alpha = new Float32Array(segs * 4), live = new Float32Array(segs * 4), index = new Uint32Array(segs * 6)
  let v = 0, k = 0
  for (const it of items) {
    let d = 0
    for (let i = 0; i + 1 < it.points.length; i++) {
      const [x0, z0] = it.points[i], [x1, z1] = it.points[i + 1]
      const len = Math.hypot(x1 - x0, z1 - z0) || 1e-6
      const ux = (x1 - x0) / len, uz = (z1 - z0) / len
      const nx = -uz * w / 2, nz = ux * w / 2
      // Chaque segment déborde d'une demi-largeur : les coudes restent fermés.
      const ex = ux * w / 2, ez = uz * w / 2
      const corners: [number, number, number][] = [
        [x0 - ex + nx, z0 - ez + nz, d], [x0 - ex - nx, z0 - ez - nz, d],
        [x1 + ex + nx, z1 + ez + nz, d + len], [x1 + ex - nx, z1 + ez - nz, d + len],
      ]
      for (const [x, z, dd] of corners) {
        position.set([x, y, z], v * 3)
        dist[v] = dd
        alpha[v] = it.alpha
        live[v] = it.live ? 1 : 0
        v++
      }
      const b = v - 4
      index.set([b, b + 1, b + 2, b + 1, b + 3, b + 2], k)
      k += 6
      d += len
    }
  }
  return { position, dist, alpha, live, index }
}

const vertexShader = /* glsl */ `
attribute float aDist;
attribute float aAlpha;
attribute float aLive;
varying float vDist;
varying float vAlpha;
varying float vLive;
void main() {
  vDist = aDist;
  vAlpha = aAlpha;
  vLive = aLive;
  gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
}`

const fragmentShader = /* glsl */ `
uniform vec3 uColor;
uniform float uTime;
uniform float uPeriod;
uniform float uDuty;
uniform float uBase;
varying float vDist;
varying float vAlpha;
varying float vLive;
void main() {
  float phase = fract((vDist - uTime) / uPeriod);
  float on = phase < uDuty ? 1.0 : uBase;
  // Lien inactif (endpoint non ready, pod arrêté) : trait plein et pâle.
  gl_FragColor = vec4(uColor, vAlpha * mix(0.35, on, vLive));
}`

function geometryOf(r: Ribbon): THREE.BufferGeometry {
  const g = new THREE.BufferGeometry()
  g.setAttribute('position', new THREE.BufferAttribute(r.position, 3))
  g.setAttribute('aDist', new THREE.BufferAttribute(r.dist, 1))
  g.setAttribute('aAlpha', new THREE.BufferAttribute(r.alpha, 1))
  g.setAttribute('aLive', new THREE.BufferAttribute(r.live, 1))
  g.setIndex(new THREE.BufferAttribute(r.index, 1))
  return g
}

export function Links({ theme, reducedMotion }: { theme: Theme; reducedMotion: boolean }) {
  const meshes = useMemo(() => {
    const color: Record<Family, string> = { main: theme.fibre, fibre: theme.fibre, data: theme.data, broken: theme.err }
    return new Map(FAMILIES.map((f) => {
      const material = new THREE.ShaderMaterial({
        vertexShader, fragmentShader, transparent: true, depthWrite: false, side: THREE.DoubleSide,
        uniforms: {
          uColor: { value: new THREE.Color(color[f]) }, uTime: { value: 0 },
          uPeriod: { value: DASH[f].period }, uDuty: { value: DASH[f].duty }, uBase: { value: DASH[f].base },
        },
      })
      const mesh = new THREE.Mesh(new THREE.BufferGeometry(), material)
      mesh.frustumCulled = false
      mesh.raycast = () => {}
      mesh.renderOrder = 1
      return [f, mesh] as const
    }))
  }, [theme])
  useEffect(() => () => meshes.forEach((m) => { m.geometry.dispose(); (m.material as THREE.Material).dispose() }), [meshes])
  const built = useRef('')
  const question = useMemo(questionTexture, [])

  useFrame(({ clock, camera, invalidate }) => {
    const st = useCluster.getState()
    world.update(st)
    const focus = world.focusFor(st.selection, st.hoverNet, st.hover?.uid ?? null)
    const far = camera.zoom < LOD_PX
    const key = `${world.version}|${world.links.length}|${st.selection?.type}:${st.selection?.key}|${st.hoverNet}|${st.hover?.uid}|${far}`
    if (key !== built.current) {
      built.current = key
      const byFamily = new Map<Family, { points: [number, number][]; alpha: number; live: boolean }[]>(FAMILIES.map((f) => [f, []]))
      for (const l of world.links) {
        const lit = (!!focus.path && isLit(l, focus.path)) || (!!focus.hover && isLit(l, focus.hover))
        // Fibres : seulement sur le chemin sélectionné ou survolé, et de près.
        if (l.family === 'fibre' && (!lit || far)) continue
        const alpha = focus.dim && focus.path && !isLit(l, focus.path) ? 0.2 : 1
        byFamily.get(l.family)!.push({ points: l.points, alpha, live: l.family === 'broken' || l.live })
      }
      for (const [f, mesh] of meshes) {
        mesh.geometry.dispose()
        mesh.geometry = geometryOf(ribbon(byFamily.get(f)!, WIDTH[f], Y[f]))
      }
    }
    if (!reducedMotion && !document.hidden && !far && world.links.length) {
      const t = clock.elapsedTime
      for (const [f, mesh] of meshes) (mesh.material as THREE.ShaderMaterial).uniforms.uTime.value = t * DASH[f].speed
      tick(invalidate)
    }
  })

  useCluster((s) => s.version)
  const signs = world.links.filter((l) => l.sign)
  return (
    <>
      {[...meshes.values()].map((m, i) => <primitive key={i} object={m} />)}
      {signs.map((l, i) => (
        <sprite key={`sign-${i}`} position={[l.sign![0], 0.6, l.sign![1]]} scale={[0.45, 0.45, 1]} raycast={() => null} renderOrder={10}>
          <spriteMaterial map={question} depthTest={false} transparent />
        </sprite>
      ))}
    </>
  )
}
```

Dans `web/src/scene/Scene.tsx`, importer `Links` et l'ajouter juste avant `<Network …/>` :

```tsx
      <Links theme={theme} reducedMotion={reducedMotion} />
```

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS. En démo : des lignes bleues discrètes des portes vers les relais, avec des paquets qui défilent ; des conduites turquoise des citernes postgres vers leurs pods, avec des gouttes ; une ligne rouge pointillée de `traefik` vers le tronçon `production` avec un « ? » (route `admin`). Avec `prefers-reduced-motion` (DevTools > Rendering), plus rien ne défile.

- [ ] **Step 4 : commit**

```bash
git add web/src/scene
git commit -m "feat(web): liens au sol animés (paquets, gouttes) et routes cassées signalées"
```

### Task 21 : sélection, survol et atténuation hors du chemin

**Files:**
- Modify: `web/src/scene/Scene.tsx` (`Picker`, `StoreInvalidator`), `web/src/scene/Selection.tsx`, `web/src/scene/Pods.tsx`

- [ ] **Step 1 : picking**

Dans `Picker` (`Scene.tsx`), remplacer `hitAt` par :

```tsx
    const hitAt = (clientX: number, clientY: number): { type: SelectionType; key: string } | null => {
      const r = el.getBoundingClientRect()
      ptr.set(((clientX - r.left) / r.width) * 2 - 1, -((clientY - r.top) / r.height) * 2 + 1)
      ray.setFromCamera(ptr, camera)
      const pods = pickables.pods.meshes, nodes = pickables.nodes.meshes
      const net = pickables.net.map((n) => n.mesh)
      const hit = ray.intersectObjects([...pods, ...nodes, ...net], false)[0]
      if (!hit || hit.instanceId === undefined) return null
      if (pods.includes(hit.object as THREE.InstancedMesh)) {
        const uid = pickables.pods.uids[hit.instanceId]
        return uid ? { type: 'pod', key: uid } : null
      }
      const owner = pickables.net.find((n) => n.mesh === hit.object)
      if (owner) {
        const k = owner.keys[hit.instanceId]
        if (!k) return null // relais regroupé : passer par la recherche ou la liste
        const i = k.indexOf(':')
        return { type: k.slice(0, i) as SelectionType, key: k.slice(i + 1) }
      }
      const name = pickables.nodes.names[hit.instanceId]
      return name ? { type: 'node', key: name } : null
    }
```

(import `type SelectionType` depuis `../store/cluster`). Dans `onMove`, après le calcul de `hit` :

```tsx
        const pod = hit?.type === 'pod' ? hit.key : null
        const netKey = hit && hit.type !== 'pod' && hit.type !== 'node' ? `${hit.type}:${hit.key}` : null
        el.style.cursor = hit ? 'pointer' : ''
        useCluster.getState().setHover(pod ? { uid: pod, x: e.clientX, y: e.clientY } : null)
        useCluster.getState().setHoverNet(netKey)
```

et dans `onLeave`, ajouter `useCluster.getState().setHoverNet(null)`.

Dans `StoreInvalidator`, le sélecteur devient `(s) => [s.version, s.selection, s.nsFilter, s.podView, s.hover?.uid, s.hoverNet] as const`.

- [ ] **Step 2 : marqueur**

Dans `Selection.tsx`, après la branche `node` :

```tsx
    } else if (sel) {
      const at = world.positionOf(sel.type, sel.key)
      if (at) {
        marker.visible = true
        const h = sel.type === 'gate' || sel.type === 'route' ? 3.4 : sel.type === 'volume' ? 2.3 : 1.3
        marker.position.set(at.x, h + Math.sin(t * 3) * 0.12, at.z)
      }
    }
```

- [ ] **Step 3 : pods hors du chemin**

Dans `Pods.tsx`, avant la boucle des pods :

```tsx
    const focus = world.focusFor(st.selection, st.hoverNet, st.hover?.uid ?? null)
```

et remplacer le calcul de `dim` :

```tsx
      const dim = (st.nsFilter !== null && p.namespace !== st.nsFilter) || (focus.dim && !!focus.path && !focus.path.has(`pod:${p.uid}`))
```

- [ ] **Step 4 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS. En démo : un clic sur le relais `api-gateway` (production) ouvre l'inspecteur (vide jusqu'à la tâche 23), pose le marqueur dessus, allume ses fibres vers ses 3 pods et la ligne depuis `nginx`, et estompe les autres pods ; le survol d'une porte affiche les fibres de ses Services sans rien estomper ; Échap rétablit la ville.

- [ ] **Step 5 : commit**

```bash
git add web/src/scene
git commit -m "feat(web): sélection et survol des relais, portes et citernes ; pods hors chemin estompés"
```

---

## Phase D — Interface

### Task 22 : appels de l'inspecteur pour les objets réseau

**Files:**
- Modify: `web/src/api/inspect.ts`, `web/src/inspector/EventsTab.tsx`, `web/src/inspector/Inspector.tsx` (appel d'`EventsTab`)
- Create: `web/src/inspector/RefYamlTab.tsx`
- Test: `web/src/api/inspect.test.ts`

- [ ] **Step 1 : test qui échoue**

Ajouter à `web/src/api/inspect.test.ts` (imports `eventsResource, routeRef, serviceRef, volumeRef` depuis `./inspect` et `route, service, volume` depuis `../store/fixtures`) :

```ts
describe('références', () => {
  it('désigne Services, PVC, Ingress et IngressRoute pour le YAML et les événements', () => {
    expect(serviceRef(service())).toEqual({ group: '', version: 'v1', kind: 'Service', namespace: 'production', name: 'api' })
    expect(volumeRef(volume())).toEqual({ group: '', version: 'v1', kind: 'PersistentVolumeClaim', namespace: 'production', name: 'data-0' })
    expect(routeRef(route())).toEqual({ group: 'networking.k8s.io', version: 'v1', kind: 'Ingress', namespace: 'production', name: 'storefront' })
    expect(routeRef(route({ source: 'IngressRoute', group: 'traefik.containo.us' })))
      .toEqual(expect.objectContaining({ group: 'traefik.containo.us', version: 'v1alpha1', kind: 'IngressRoute' }))
    expect(['Pod', 'Service', 'PersistentVolumeClaim', 'Ingress', 'IngressRoute'].map(eventsResource))
      .toEqual(['pods', 'services', 'persistentvolumeclaims', 'ingresses', 'ingressroutes'])
  })
})
```

Run: `npx vitest run src/api/inspect.test.ts`
Expected: FAIL.

- [ ] **Step 2 : implémentation**

Dans `web/src/api/inspect.ts` (import `type Route, type Service, type Volume` depuis `./types`) :

```ts
export const getEvents = (ns: string, name: string, resource = 'pods') =>
  getJSON<{ events: KubeEvent[] }>(`/api/namespaces/${seg(ns)}/${seg(resource)}/${seg(name)}/events`).then((r) => r.events)

export const serviceRef = (s: Pick<Service, 'namespace' | 'name'>): Ref =>
  ({ group: '', version: 'v1', kind: 'Service', namespace: s.namespace, name: s.name })

export const volumeRef = (v: Pick<Volume, 'namespace' | 'name'>): Ref =>
  ({ group: '', version: 'v1', kind: 'PersistentVolumeClaim', namespace: v.namespace, name: v.name })

export const routeRef = (r: Pick<Route, 'source' | 'group' | 'namespace' | 'name'>): Ref =>
  r.source === 'Ingress'
    ? { group: 'networking.k8s.io', version: 'v1', kind: 'Ingress', namespace: r.namespace, name: r.name }
    : { group: r.group, version: 'v1alpha1', kind: 'IngressRoute', namespace: r.namespace, name: r.name }

const RESOURCES: Record<string, string> = {
  Pod: 'pods', Service: 'services', PersistentVolumeClaim: 'persistentvolumeclaims', Ingress: 'ingresses', IngressRoute: 'ingressroutes',
}

/** Ressource de l'URL des événements d'un kind. */
export const eventsResource = (kind: string) => RESOURCES[kind] ?? `${kind.toLowerCase()}s`
```

Remplacer `EventsTab` (`web/src/inspector/EventsTab.tsx`) par une version générique :

```tsx
/** Événements d'un objet, du plus récent au plus ancien, rafraîchis tant que l'onglet est ouvert. */
export function EventsTab({ ns, name, resource = 'pods', empty = 'Aucun événement récent pour ce pod.' }: {
  ns: string; name: string; resource?: string; empty?: string
}) {
  const [events, setEvents] = useState<KubeEvent[] | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    setEvents(null)
    setError('')
    const load = () =>
      getEvents(ns, name, resource)
        .then((e) => { if (live) { setEvents(e); setError('') } })
        .catch((e: Error) => live && setError(e.message))
    load()
    const id = setInterval(load, REFRESH_MS)
    return () => { live = false; clearInterval(id) }
  }, [ns, name, resource])

  if (error) return <div className="p-body"><p className="note s-err">{error}</p></div>
  if (!events) return <div className="p-body"><p className="note">Chargement…</p></div>
  if (!events.length) return <div className="p-body"><p className="note">{empty}</p></div>
```

(le tableau qui suit est inchangé ; retirer l'import de `Pod`). Dans `Inspector.tsx`, `PodBody` : `return <EventsTab ns={p.namespace} name={p.name} />`.

Créer `web/src/inspector/RefYamlTab.tsx` :

```tsx
import { Suspense, lazy, useEffect, useState } from 'react'
import { getYaml, type Ref, type YamlDoc } from '../api/inspect'
import { useTheme } from '../scene/theme'

const MonacoYaml = lazy(() => import('./MonacoYaml'))

/** YAML en lecture seule d'un objet désigné directement (Service, route, PVC). */
export function RefYamlTab({ target }: { target: Ref }) {
  const theme = useTheme()
  const [doc, setDoc] = useState<YamlDoc | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let live = true
    setDoc(null)
    setError('')
    getYaml(target).then((d) => live && setDoc(d)).catch((e: Error) => live && setError(e.message))
    return () => { live = false }
  }, [target.group, target.version, target.kind, target.namespace, target.name]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <>
      {error && <div className="log-banner"><span className="s-err">{error}</span></div>}
      {!error && !doc && <div className="p-body"><p className="note">Chargement…</p></div>}
      {doc && (
        <Suspense fallback={<div className="p-body"><p className="note">Chargement de l'éditeur…</p></div>}>
          <MonacoYaml value={doc.yaml} dark={theme.dark} />
        </Suspense>
      )}
      <div className="toolbar footer"><span className="note" style={{ margin: 0 }}>Lecture seule.</span></div>
    </>
  )
}
```

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add web/src
git commit -m "feat(web): YAML et événements d'un Service, d'une route ou d'un PVC"
```

### Task 23 : inspecteur des Services, routes, portes et PVC

**Files:**
- Create: `web/src/inspector/NetOverview.tsx`
- Modify: `web/src/inspector/Inspector.tsx`

- [ ] **Step 1 : aperçus**

Créer `web/src/inspector/NetOverview.tsx` :

```tsx
import { useEffect, useState } from 'react'
import { getEvents, type KubeEvent } from '../api/inspect'
import { routeKey, type Route, type Service, type Volume } from '../api/types'
import { clusterColors } from '../scene/colors'
import { postureFor } from '../scene/posture'
import { useCluster } from '../store/cluster'
import { readyCount, routeBroken, routesTo, type Gate } from '../store/net'
import { fmtMem } from '../ui/format'
import { BADGE, goPod } from './common'

const goService = (ns: string, name: string) => () => useCluster.getState().select({ type: 'service', key: `${ns}/${name}` })
const goRoute = (r: Route) => () => useCluster.getState().select({ type: 'route', key: routeKey(r) })
const goGate = (name: string) => () => useCluster.getState().select({ type: 'gate', key: name })

const STATE: Record<string, [string, string]> = {
  ok: ['', ''], missing: ['s-err', 'Service introuvable'], indirect: ['s-mute', 'TraefikService'],
}

/** Événements qui expliquent presque toujours un PVC bloqué en Pending. */
const BLOCKING = new Set(['ProvisioningFailed', 'WaitForFirstConsumer', 'ExternalProvisioning', 'FailedBinding'])

function PodItem({ uid, extra }: { uid: string; extra?: React.ReactNode }) {
  const st = useCluster.getState()
  const p = st.pods.get(uid)
  return (
    <li>
      <button onClick={goPod(uid)} disabled={!p}>
        <i style={{ background: p ? clusterColors(st).get(p.namespace) : undefined }} />
        <span className="nm">{p?.name ?? 'pod non visible'}</span>
        {extra}
      </button>
    </li>
  )
}

export function ServiceOverview({ s }: { s: Service }) {
  const via = routesTo(useCluster.getState().routes.values(), s)
  return (
    <div className="p-body">
      <dl className="kv">
        <dt>Type</dt><dd>{s.type}{s.headless ? ' (headless)' : ''}</dd>
        {s.clusterIP && <><dt>Cluster IP</dt><dd>{s.clusterIP}</dd></>}
        {s.loadBalancer?.length ? <><dt>LoadBalancer</dt><dd>{s.loadBalancer.join(', ')}</dd></> : null}
        {s.externalName && <><dt>Nom externe</dt><dd>{s.externalName}</dd></>}
      </dl>
      {s.ports.length > 0 && (
        <>
          <h3>Ports</h3>
          <div className="tags">
            {s.ports.map((p) => (
              <span key={`${p.port}/${p.protocol}`} className="tag">
                {p.name ? `${p.name} ` : ''}{p.port}{p.targetPort ? ` → ${p.targetPort}` : ''}/{p.protocol}{p.nodePort ? ` · node ${p.nodePort}` : ''}
              </span>
            ))}
          </div>
        </>
      )}
      {s.type !== 'ExternalName' && (
        <>
          <h3>Endpoints ({readyCount(s)}/{s.endpoints.length} ready)</h3>
          {s.endpoints.length ? (
            <ul className="podlist" data-testid="endpoints">
              {s.endpoints.map((e) => (
                <PodItem key={e.podUID} uid={e.podUID} extra={<span className={e.ready ? 's-ok' : 's-warn'}>{e.ready ? 'ready' : 'non ready'}</span>} />
              ))}
            </ul>
          ) : <p className="note s-err">Aucun pod derrière ce Service : vérifiez son selector.</p>}
        </>
      )}
      <h3>Routes ({via.length})</h3>
      {via.length ? (
        <ul className="podlist">
          {via.map((r) => (
            <li key={routeKey(r)}><button onClick={goRoute(r)}><span className="nm">{r.name}</span><span className="later">{r.source} · porte {r.gate}</span></button></li>
          ))}
        </ul>
      ) : <p className="note">Aucune Ingress ni IngressRoute ne vise ce Service.</p>}
    </div>
  )
}

export function RouteOverview({ r }: { r: Route }) {
  const services = useCluster.getState().services
  return (
    <div className="p-body">
      <dl className="kv">
        <dt>Porte</dt><dd><button className="link" onClick={goGate(r.gate)}>{r.gate}</button></dd>
        <dt>Source</dt><dd>{r.source} ({r.group})</dd>
        {r.addresses?.length ? <><dt>Adresses</dt><dd>{r.addresses.join(', ')}</dd></> : null}
      </dl>
      <h3>Règles ({r.rules.length})</h3>
      <table className="evt" data-testid="rules">
        <tbody>
          {r.rules.map((rule, i) => {
            const b = rule.backend
            const [cls, label] = STATE[b.state] ?? STATE.ok
            return (
              <tr key={i} className={b.state === 'missing' ? 'warning' : ''}>
                <td>
                  {rule.host || '*'}{rule.path ?? ''}
                  {rule.match && <><br /><code>{rule.match}</code></>}
                </td>
                <td>
                  {services.has(`${b.namespace}/${b.service}`)
                    ? <button className="link" onClick={goService(b.namespace, b.service)}>{b.service}</button>
                    : b.service}
                  {b.port ? `:${b.port}` : ''}{b.namespace !== r.namespace ? ` (${b.namespace})` : ''}
                  {label && <> <span className={cls}>{label}</span></>}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

export function GateOverview({ g }: { g: Gate }) {
  return (
    <div className="p-body">
      <p className="note">Contrôleur d'entrée « {g.name} » : chaque route ci-dessous entre dans la ville par cette porte.</p>
      <ul className="podlist" data-testid="gate-routes">
        {g.routes.map((r) => (
          <li key={routeKey(r)}>
            <button onClick={goRoute(r)}>
              <span className="nm">{r.name}</span>
              <span className="later">{r.source} · {r.namespace}</span>
              {routeBroken(r) && <span className="s-err">Service introuvable</span>}
            </button>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function VolumeOverview({ v }: { v: Volume }) {
  const pods = useCluster.getState().pods
  const [why, setWhy] = useState<KubeEvent | null>(null)
  useEffect(() => {
    setWhy(null)
    if (v.phase !== 'Pending') return
    let live = true
    getEvents(v.namespace, v.name, 'persistentvolumeclaims')
      .then((evs) => live && setWhy(evs.find((e) => BLOCKING.has(e.reason)) ?? null))
      .catch(() => {})
    return () => { live = false }
  }, [v.namespace, v.name, v.phase])

  return (
    <div className="p-body">
      {why && (
        <p className={`note ${why.type === 'Warning' ? 's-err' : 's-warn'}`} data-testid="pvc-why"><b>{why.reason}</b> : {why.message}</p>
      )}
      <dl className="kv">
        <dt>Classe</dt><dd>{v.storageClass || '(aucune)'}</dd>
        <dt>Demandé</dt><dd>{fmtMem(v.requested)}</dd>
        <dt>Capacité</dt><dd>{v.capacity ? fmtMem(v.capacity) : '—'}</dd>
        <dt>Accès</dt><dd>{v.accessModes.join(', ') || '—'}</dd>
        {v.volumeName && <><dt>Volume</dt><dd>{v.volumeName}</dd></>}
      </dl>
      <h3>Pods ({v.pods.length})</h3>
      {v.pods.length ? (
        <ul className="podlist">
          {v.pods.map((uid) => {
            const p = pods.get(uid)
            return <PodItem key={uid} uid={uid} extra={p && <span className={BADGE[postureFor(p).antenna]}>{p.displayStatus}</span>} />
          })}
        </ul>
      ) : <p className="note">Aucun pod ne monte ce PVC.</p>}
    </div>
  )
}
```

- [ ] **Step 2 : panneau**

Dans `web/src/inspector/Inspector.tsx` :

1. Imports : `type ReactNode` depuis `react` ; `routeRef, serviceRef, volumeRef, eventsResource, type Ref` depuis `../api/inspect` ; `type Selection` depuis `../store/cluster` ; `gatesOf, readyCount, routeBroken` depuis `../store/net` ; `HEALTH_LABEL, healthSignal, volumeSignal` depuis `../scene/health` ; `GateOverview, RouteOverview, ServiceOverview, VolumeOverview` depuis `./NetOverview` ; `RefYamlTab` depuis `./RefYamlTab`.
2. Ajouter :

```tsx
const NET_TABS: [InspectorTab, string][] = [['overview', 'Aperçu'], ['yaml', 'YAML'], ['events', 'Événements']]

function NetBody({ target, children }: { target: Ref; children: ReactNode }) {
  const tab = useCluster((s) => s.inspectorTab)
  if (tab === 'yaml') return <div className="p-body flush"><RefYamlTab target={target} /></div>
  if (tab === 'events') {
    return <EventsTab ns={target.namespace} name={target.name} resource={eventsResource(target.kind)} empty="Aucun événement récent pour cet objet." />
  }
  return <>{children}</>
}

/** Panneau d'un Service, d'une route, d'une porte ou d'un PVC. */
function NetPanel({ selection, onClose }: { selection: NonNullable<Selection>; onClose: () => void }) {
  const st = useCluster.getState()
  const colors = clusterColors(st)
  const gone = (kind: string, text: string) => (
    <>
      <Head kind={kind} name={selection.name} badge="Supprimé" badgeClass="s-mute" tabs={[['overview', 'Aperçu']]} onClose={onClose} />
      <div className="gone">{text}</div>
    </>
  )
  switch (selection.type) {
    case 'service': {
      const s = st.services.get(selection.key)
      if (!s) return gone('Service', 'Ce Service a été supprimé.')
      const badge = s.health === 'external' || s.health === 'down' ? HEALTH_LABEL[s.health] : `${readyCount(s)}/${s.endpoints.length} ready`
      return (
        <>
          <Head kind={`Service · ${s.namespace}`} name={s.name} badge={badge} badgeClass={BADGE[healthSignal(s.health)]}
            color={colors.get(s.namespace)} tabs={NET_TABS} onClose={onClose} />
          <NetBody target={serviceRef(s)}><ServiceOverview s={s} /></NetBody>
        </>
      )
    }
    case 'route': {
      const r = st.routes.get(selection.key)
      if (!r) return gone('Route', 'Cette route a été supprimée.')
      const broken = routeBroken(r)
      return (
        <>
          <Head kind={`${r.source} · ${r.namespace}`} name={r.name} badge={broken ? 'Service introuvable' : `Porte ${r.gate}`}
            badgeClass={broken ? 's-err' : 's-ok'} color={colors.get(r.namespace)} tabs={NET_TABS} onClose={onClose} />
          <NetBody target={routeRef(r)}><RouteOverview r={r} /></NetBody>
        </>
      )
    }
    case 'volume': {
      const v = st.volumes.get(selection.key)
      if (!v) return gone('PVC', 'Ce PVC a été supprimé.')
      return (
        <>
          <Head kind={`PVC · ${v.namespace}`} name={v.name} badge={v.phase} badgeClass={BADGE[volumeSignal(v)]}
            color={colors.get(v.namespace)} tabs={NET_TABS} onClose={onClose} />
          <NetBody target={volumeRef(v)}><VolumeOverview v={v} /></NetBody>
        </>
      )
    }
    case 'gate': {
      const g = gatesOf(st.routes.values()).find((x) => x.name === selection.key)
      if (!g) return gone("Porte d'entrée", 'Plus aucune route ne passe par cette porte.')
      return (
        <>
          <Head kind="Porte d'entrée" name={g.name} badge={g.broken ? `${g.broken} route(s) cassée(s)` : `${g.routes.length} route(s)`}
            badgeClass={g.broken ? 's-warn' : 's-ok'} tabs={[['overview', 'Aperçu']]} onClose={onClose} />
          <GateOverview g={g} />
        </>
      )
    }
  }
  return null
}
```

3. Dans `Inspector`, après la branche `node` :

```tsx
  } else if (selection) {
    content = <NetPanel selection={selection} onClose={close} />
  }
```

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS. En démo, ouvrir `/services/production/api-gateway` (après la tâche 25) ou cliquer le relais : badge « 3/3 ready », ports, endpoints cliquables, route `storefront` ; onglet YAML : `kind: Service` ; cliquer la porte `traefik` : routes `grafana`, `admin` (Service introuvable), `argocd` ; citerne `uploads-preview` : « WaitForFirstConsumer : waiting for first consumer… ».

- [ ] **Step 4 : commit**

```bash
git add web/src/inspector
git commit -m "feat(web): inspecteur des Services, routes, portes et PVC"
```

### Task 24 : vue Liste — Entrées, Services, Stockage

**Files:**
- Modify: `web/src/ui/tree.ts`, `web/src/ui/ListView.tsx`
- Test: `web/src/ui/tree.test.ts`

- [ ] **Step 1 : test qui échoue**

Ajouter à `web/src/ui/tree.test.ts` (imports `route, service, volume` depuis `../store/fixtures`) :

```ts
describe('réseau et stockage', () => {
  const withNet = {
    ...st,
    services: new Map([
      ['production/api', service()],
      ['production/ghost', service({ name: 'ghost', endpoints: [], health: 'down' })],
    ]),
    routes: new Map([['Ingress/production/storefront', route()]]),
    volumes: new Map([['production/data-0', volume()]]),
  }

  it('ajoute Entrées, Services et Stockage, seulement s’ils ne sont pas vides', () => {
    expect(buildTree(st).map((g) => g.label)).toEqual(['Namespaces', 'Nodes'])
    const roots = buildTree(withNet)
    expect(roots.map((g) => g.label)).toEqual(['Namespaces', 'Nodes', 'Entrées', 'Services', 'Stockage'])
    const [, , gates, services, storage] = roots
    expect(gates.children![0]).toEqual(expect.objectContaining({ label: 'nginx', select: { type: 'gate', key: 'nginx' } }))
    expect(gates.children![0].children![0].select).toEqual({ type: 'route', key: 'Ingress/production/storefront' })
    expect(services.children![0].label).toBe('production')
    expect(services.children![0].children!.map((s) => [s.label, s.status])).toEqual([['api', undefined], ['ghost', 'down']])
    expect(storage.children![0].label).toBe('standard-rwo')
    expect(storage.children![0].children![0]).toEqual(expect.objectContaining({ label: 'data-0', select: { type: 'volume', key: 'production/data-0' } }))
  })
})
```

Run: `npx vitest run src/ui/tree.test.ts`
Expected: FAIL.

- [ ] **Step 2 : implémentation**

Dans `web/src/ui/tree.ts` (imports `routeKey, serviceKey, volumeKey, type Route, type Service, type Volume` ; `gatesOf, readyCount, routeBroken` depuis `../store/net` ; `fmtMem` depuis `./format`) :

1. Signature : `buildTree(st: { pods: …; nodes: …; workloads: …; services?: ReadonlyMap<string, Service>; routes?: ReadonlyMap<string, Route>; volumes?: ReadonlyMap<string, Volume> })`.
2. Remplacer le `return [...]` final par :

```ts
  const roots: TreeNode[] = [
    { id: 'group:namespaces', label: 'Namespaces', detail: `${namespaces.length}`, children: namespaces },
    { id: 'group:nodes', label: 'Nodes', detail: `${nodes.length}`, children: nodes },
  ]

  const gates = gatesOf(st.routes?.values() ?? [])
  if (gates.length) roots.push({
    id: 'group:gates', label: 'Entrées', detail: `${gates.length}`,
    children: gates.map((g) => ({
      id: `gate:${g.name}`, label: g.name, detail: `${g.routes.length} routes`, status: g.broken ? 'route cassée' : undefined,
      select: { type: 'gate', key: g.name },
      children: g.routes.map((r) => ({
        id: `route:${routeKey(r)}`, label: r.name, detail: `${r.source} · ${r.namespace}`,
        status: routeBroken(r) ? 'Service introuvable' : undefined, select: { type: 'route', key: routeKey(r) },
      })),
    })),
  })

  const services = [...(st.services?.values() ?? [])]
  if (services.length) {
    const byNs = new Map<string, Service[]>()
    for (const s of services) byNs.set(s.namespace, [...(byNs.get(s.namespace) ?? []), s])
    roots.push({
      id: 'group:services', label: 'Services', detail: `${services.length}`,
      children: [...byNs].map(([ns, ss]) => ({
        id: `svcns:${ns}`, label: ns, detail: `${ss.length} Services`,
        children: ss.map((s) => ({
          id: `service:${serviceKey(s)}`, label: s.name,
          detail: s.type === 'ExternalName' ? `ExternalName · ${s.externalName}` : `${s.type} · ${readyCount(s)}/${s.endpoints.length} ready`,
          status: s.health === 'down' || s.health === 'degraded' ? s.health : undefined,
          select: { type: 'service' as const, key: serviceKey(s) },
        })).sort(byLabel),
      })).sort(byLabel),
    })
  }

  const volumes = [...(st.volumes?.values() ?? [])]
  if (volumes.length) {
    const byClass = new Map<string, Volume[]>()
    for (const v of volumes) byClass.set(v.storageClass || '(aucune)', [...(byClass.get(v.storageClass || '(aucune)') ?? []), v])
    roots.push({
      id: 'group:storage', label: 'Stockage', detail: `${volumes.length}`,
      children: [...byClass].map(([c, vs]) => ({
        id: `class:${c}`, label: c, detail: `${vs.length} PVC`,
        children: vs.map((v) => ({
          id: `volume:${volumeKey(v)}`, label: v.name, detail: `${v.namespace} · ${fmtMem(v.requested)}`,
          status: v.phase !== 'Bound' ? v.phase : undefined, select: { type: 'volume' as const, key: volumeKey(v) },
        })).sort(byLabel),
      })).sort(byLabel),
    })
  }
  return roots
```

Dans `web/src/ui/ListView.tsx`, remplacer `statusClass` :

```ts
const NET_STATUS: Record<string, string> = {
  down: 's-err', Lost: 's-err', 'Service introuvable': 's-err', degraded: 's-warn', Pending: 's-warn', 'route cassée': 's-warn',
}

function statusClass(n: TreeNode): string {
  if (!n.status) return ''
  if (n.select?.type === 'node') return 's-warn'
  if (n.select && n.select.type !== 'pod') return NET_STATUS[n.status] ?? 's-warn'
  return BADGE[postureFor({ displayStatus: n.status, ready: n.status === 'Running' }).antenna]
}
```

- [ ] **Step 3 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add web/src/ui
git commit -m "feat(web): vue Liste avec Entrées, Services et Stockage"
```

### Task 25 : recherche et liens profonds

**Files:**
- Modify: `web/src/ui/searchRank.ts`, `web/src/ui/Search.tsx`, `web/src/ui/route.ts`
- Test: `web/src/ui/searchRank.test.ts`, `web/src/ui/route.test.ts`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `web/src/ui/searchRank.test.ts` (imports `route, service, volume` depuis `../store/fixtures` ; réutiliser l'état de test du fichier ou en créer un avec `pods`, `nodes`, `workloads` vides) :

```ts
describe('réseau et stockage', () => {
  const st = {
    pods: new Map(), nodes: new Map(), workloads: new Map(),
    services: new Map([['production/api', service()]]),
    routes: new Map([['Ingress/production/storefront', route()]]),
    volumes: new Map([['production/data-0', volume()]]),
  }

  it('trouve Services, routes (par nom ou par hôte), PVC et portes', () => {
    expect(search('api', st).map((r) => [r.type, r.key])).toEqual([['service', 'production/api']])
    expect(search('shop.example', st).map((r) => r.key)).toEqual(['Ingress/production/storefront'])
    expect(search('data', st)[0]).toEqual(expect.objectContaining({ type: 'volume', key: 'production/data-0' }))
    expect(search('ngi', st)[0]).toEqual(expect.objectContaining({ type: 'gate', key: 'nginx' }))
  })
})
```

Ajouter au tableau `it.each` de `parseRoute / pathFor` dans `web/src/ui/route.test.ts` :

```ts
    ['/services/production/api', { type: 'service', namespace: 'production', name: 'api' }],
    ['/volumes/production/data-0', { type: 'volume', namespace: 'production', name: 'data-0' }],
    ['/routes/ingressroute/monitoring/grafana', { type: 'route', source: 'IngressRoute', namespace: 'monitoring', name: 'grafana' }],
    ['/gates/traefik', { type: 'gate', name: 'traefik' }],
    ['/routes/httproute/a/b', null],
```

et le test :

```ts
  it('rétablit un Service partagé par lien', () => {
    const w = fakeWindow('/services/production/api')
    const stop = syncRoute(w)
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, services: [service()] }])
    expect(useCluster.getState().selection).toEqual({ type: 'service', key: 'production/api', name: 'api' })
    useCluster.getState().select({ type: 'route', key: 'Ingress/production/storefront' })
    expect(w.location.pathname).toBe('/routes/ingress/production/storefront')
    stop()
  })
```

(placer ce test dans le `describe('syncRoute')`, import `service` depuis `../store/fixtures`).

Run: `npx vitest run src/ui`
Expected: FAIL.

- [ ] **Step 2 : recherche**

Dans `web/src/ui/searchRank.ts` (imports `routeKey, serviceKey, volumeKey, type Route, type Service, type Volume` ; `gatesOf` depuis `../store/net`) :

```ts
export interface SearchResult {
  type: 'pod' | 'node' | 'workload' | 'service' | 'route' | 'volume' | 'gate'
  key: string // uid du pod, nom du node ou de la porte, clé du workload, du Service, de la route ou du volume
  label: string
  detail: string
  /** Pod à sélectionner pour un workload (le premier de ses pods). */
  podUid?: string
}
```

Signature : `search(query, st: { pods; nodes; workloads; services?: ReadonlyMap<string, Service>; routes?: ReadonlyMap<string, Route>; volumes?: ReadonlyMap<string, Volume> })`. Avant le tri final :

```ts
  const routes = [...(st.routes?.values() ?? [])]
  for (const g of gatesOf(routes)) {
    const s = score(g.name, 0)
    if (s >= 0) scored.push([s, { type: 'gate', key: g.name, label: g.name, detail: `Porte · ${g.routes.length} routes` }])
  }
  for (const sv of st.services?.values() ?? []) {
    const s = score(sv.name, 1)
    if (s >= 0) scored.push([s, { type: 'service', key: serviceKey(sv), label: sv.name, detail: `Service · ${sv.namespace} · ${sv.type}` }])
  }
  for (const r of routes) {
    const ss = [r.name, ...r.rules.map((x) => x.host ?? '')].map((n) => (n ? score(n, 1) : -1)).filter((x) => x >= 0)
    if (ss.length) scored.push([Math.min(...ss), { type: 'route', key: routeKey(r), label: r.name, detail: `${r.source} · ${r.namespace} · porte ${r.gate}` }])
  }
  for (const v of st.volumes?.values() ?? []) {
    const s = score(v.name, 1)
    if (s >= 0) scored.push([s, { type: 'volume', key: volumeKey(v), label: v.name, detail: `PVC · ${v.namespace} · ${v.phase}` }])
  }
```

Dans `web/src/ui/Search.tsx` :

```tsx
/** Position à viser dans la ville pour un résultat. */
function positionOf(r: SearchResult): { x: number; z: number } | null {
  if (r.type === 'workload') return r.podUid ? world.positionOf('pod', r.podUid) : null
  return world.positionOf(r.type, r.key)
}
```

et dans `choose` :

```tsx
    if (r.type === 'workload') st.select({ type: 'pod', key: r.podUid! })
    else st.select({ type: r.type, key: r.key })
```

(retirer l'import devenu inutile de `podPositions` ; libellés : `aria-label="Rechercher un pod, un node, un workload, un Service, une route ou un PVC"`, `placeholder="Pod, node, Service, route, PVC…"`).

- [ ] **Step 3 : liens profonds**

Remplacer `web/src/ui/route.ts` :

```ts
// Liens profonds : /pods/{ns}/{nom}, /nodes/{nom}, /services/{ns}/{nom},
// /volumes/{ns}/{nom}, /routes/{ingress|ingressroute}/{ns}/{nom}, /gates/{nom}.
// L'URL suit la sélection (sans recharger la page) et une sélection partagée
// par lien est rétablie dès que l'objet arrive dans le flux.

import { useCluster, type ClusterState, type Selection, type SelectionType } from '../store/cluster'

export type Route =
  | { type: 'pod'; namespace: string; name: string }
  | { type: 'node'; name: string }
  | { type: 'service'; namespace: string; name: string }
  | { type: 'volume'; namespace: string; name: string }
  | { type: 'route'; source: 'Ingress' | 'IngressRoute'; namespace: string; name: string }
  | { type: 'gate'; name: string }
  | null

const SOURCES: Record<string, 'Ingress' | 'IngressRoute'> = { ingress: 'Ingress', ingressroute: 'IngressRoute' }

export function parseRoute(pathname: string): Route {
  const [head, ...rest] = pathname.split('/').filter(Boolean).map(decodeURIComponent)
  if (head === 'pods' && rest.length === 2) return { type: 'pod', namespace: rest[0], name: rest[1] }
  if (head === 'nodes' && rest.length === 1) return { type: 'node', name: rest[0] }
  if (head === 'services' && rest.length === 2) return { type: 'service', namespace: rest[0], name: rest[1] }
  if (head === 'volumes' && rest.length === 2) return { type: 'volume', namespace: rest[0], name: rest[1] }
  if (head === 'routes' && rest.length === 3 && SOURCES[rest[0]]) return { type: 'route', source: SOURCES[rest[0]], namespace: rest[1], name: rest[2] }
  if (head === 'gates' && rest.length === 1) return { type: 'gate', name: rest[0] }
  return null
}

export function pathFor(r: Route): string {
  if (!r) return '/'
  const e = encodeURIComponent
  switch (r.type) {
    case 'pod': return `/pods/${e(r.namespace)}/${e(r.name)}`
    case 'node': return `/nodes/${e(r.name)}`
    case 'service': return `/services/${e(r.namespace)}/${e(r.name)}`
    case 'volume': return `/volumes/${e(r.namespace)}/${e(r.name)}`
    case 'route': return `/routes/${r.source.toLowerCase()}/${e(r.namespace)}/${e(r.name)}`
    case 'gate': return `/gates/${e(r.name)}`
  }
}

/** Sélection désignée par une route, si l'objet est déjà dans le flux. */
function selectionFor(r: NonNullable<Route>, st: ClusterState): { type: SelectionType; key: string } | null {
  switch (r.type) {
    case 'pod': {
      const p = [...st.pods.values()].find((x) => x.namespace === r.namespace && x.name === r.name)
      return p ? { type: 'pod', key: p.uid } : null
    }
    case 'node':
      return st.nodes.has(r.name) || [...st.pods.values()].some((x) => x.nodeName === r.name) ? { type: 'node', key: r.name } : null
    case 'service': {
      const key = `${r.namespace}/${r.name}`
      return st.services.has(key) ? { type: 'service', key } : null
    }
    case 'volume': {
      const key = `${r.namespace}/${r.name}`
      return st.volumes.has(key) ? { type: 'volume', key } : null
    }
    case 'route': {
      const key = `${r.source}/${r.namespace}/${r.name}`
      return st.routes.has(key) ? { type: 'route', key } : null
    }
    case 'gate':
      return [...st.routes.values()].some((x) => x.gate === r.name) ? { type: 'gate', key: r.name } : null
  }
}

function routeFor(sel: Selection, st: ClusterState): Route {
  if (!sel) return null
  switch (sel.type) {
    case 'pod': {
      const p = st.pods.get(sel.key)
      return p ? { type: 'pod', namespace: p.namespace, name: p.name } : null
    }
    case 'node': return { type: 'node', name: sel.key }
    case 'gate': return { type: 'gate', name: sel.key }
    case 'service':
    case 'volume': {
      const [namespace, name] = sel.key.split('/')
      return { type: sel.type, namespace, name }
    }
    case 'route': {
      const [source, namespace, name] = sel.key.split('/')
      return { type: 'route', source: source as 'Ingress' | 'IngressRoute', namespace, name }
    }
  }
}

/** Synchronise l'URL et la sélection ; renvoie la fonction de désabonnement. */
export function syncRoute(win: Pick<Window, 'location' | 'history'> = window): () => void {
  let pending = parseRoute(win.location.pathname)

  const tryRestore = () => {
    if (!pending) return
    const st = useCluster.getState()
    const sel = selectionFor(pending, st)
    if (sel) {
      pending = null
      st.select(sel)
    }
  }

  const unsubVersion = useCluster.subscribe((s) => s.version, tryRestore)
  const unsubSel = useCluster.subscribe((s) => s.selection, (sel) => {
    if (pending) return // lien profond pas encore rétabli : on ne touche pas à l'URL
    const path = pathFor(routeFor(sel, useCluster.getState()))
    if (path !== win.location.pathname) win.history.replaceState(null, '', path)
  })
  tryRestore()
  return () => { unsubVersion(); unsubSel() }
}
```

Vérifier que `ClusterState` est exporté par `store/cluster.ts` (il l'est : `export interface ClusterState`).

- [ ] **Step 4 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add web/src/ui
git commit -m "feat(web): recherche et liens profonds des Services, routes, portes et PVC"
```

### Task 26 : résumé du chemin allumé

**Files:**
- Create: `web/src/ui/PathSummary.tsx`
- Modify: `web/src/App.tsx`, `web/src/styles.css`

- [ ] **Step 1 : implémentation**

Créer `web/src/ui/PathSummary.tsx` :

```tsx
import { world } from '../scene/world'
import { useCluster } from '../store/cluster'

const LABELS: [string, string, string][] = [['gate', 'porte', 'portes'], ['service', 'Service', 'Services'], ['pod', 'pod', 'pods'], ['volume', 'PVC', 'PVC']]

/**
 * Résumé du chemin allumé par la sélection d'une porte, d'une route, d'un
 * Service ou d'un PVC : ce que la ville montre, dit aussi en texte (lecteurs
 * d'écran, tests).
 */
export function PathSummary() {
  const selection = useCluster((s) => s.selection)
  useCluster((s) => s.version)
  if (!selection || selection.type === 'pod' || selection.type === 'node') return null
  world.update(useCluster.getState())
  const path = world.focusFor(selection, null, null).path
  if (!path) return null
  const parts = LABELS.map(([type, one, many]) => {
    const n = [...path].filter((k) => k.startsWith(`${type}:`)).length
    return n ? `${n} ${n === 1 ? one : many}` : ''
  }).filter(Boolean)
  return <div className="path-summary" role="status" data-testid="path-summary">Chemin : {parts.join(' · ') || 'aucun lien'}</div>
}
```

Dans `web/src/App.tsx`, importer `PathSummary` et l'ajouter après `<Hint />` : `{view === '3d' && <PathSummary />}`.

Dans `web/src/styles.css`, à la suite des styles du HUD :

```css
.path-summary{position:fixed;top:64px;left:50%;transform:translateX(-50%);z-index:5;padding:4px 12px;border:1px solid var(--line);border-radius:999px;background:var(--panel);color:var(--ink);font-size:12px;white-space:nowrap}
```

- [ ] **Step 2 : vérifier**

Run: `npx tsc --noEmit && npx vitest run`
Expected: PASS. En démo, cliquer la porte `nginx` affiche « Chemin : 1 porte · 4 Services · … pods ».

- [ ] **Step 3 : commit**

```bash
git add web/src
git commit -m "feat(web): résumé textuel du chemin sélectionné"
```

---

## Phase E — Intégration, banc et documentation

### Task 27 : e2e en mode démo

**Files:**
- Create: `web/e2e/network.spec.ts`

- [ ] **Step 1 : tests**

Créer `web/e2e/network.spec.ts` :

```ts
import { expect, test, type Page } from '@playwright/test'

// Réseau et stockage en mode démo : portes nginx et traefik, Service en panne,
// chemin allumé depuis un relais, route cassée, PVC en attente, recherche par hôte.

const panel = (page: Page) => page.locator('aside.panel')
const editorText = (page: Page) =>
  page.getByTestId('yaml-editor').locator('.view-lines').innerText().then((t) => t.replace(/ /g, ' '))

function collectProblems(page: Page) {
  const problems: string[] = []
  page.on('console', (m) => { if (m.type() === 'error') problems.push(m.text()) })
  page.on('pageerror', (e) => problems.push(e.message))
  return problems
}

test('vue Liste : Entrées, Services et Stockage', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/')
  await page.getByRole('button', { name: 'Liste' }).click()
  const tree = page.getByRole('tree', { name: 'Cluster' })
  for (const group of ['Entrées', 'Services', 'Stockage']) await tree.getByRole('treeitem', { name: new RegExp(`^${group}`) }).click()
  await expect(tree.getByRole('treeitem', { name: /^nginx/ })).toBeVisible()
  await expect(tree.getByRole('treeitem', { name: /^traefik/ })).toBeVisible()
  await expect(tree.getByRole('treeitem', { name: /^standard-rwo/ })).toBeVisible()
  expect(problems).toEqual([])
})

test('un Service en panne, puis le chemin d’un Service sain', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/services/staging/checkout-preview')
  await expect(panel(page).getByText('Service · staging')).toBeVisible()
  await expect(panel(page).getByText('Aucun endpoint ready')).toBeVisible()

  await page.goto('/services/production/api-gateway')
  await expect(panel(page).getByText(/\d+\/\d+ ready/).first()).toBeVisible()
  await expect(panel(page).getByTestId('endpoints').getByRole('button').first()).toBeVisible()
  await expect(page.getByTestId('path-summary')).toContainText('1 porte')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: 'e2e/__screenshots__/network-service.png' })

  await panel(page).getByRole('tab', { name: 'YAML' }).click()
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toContain('kind: Service')
  expect(problems).toEqual([])
})

test('porte traefik : une route vers un Service introuvable', async ({ page }) => {
  await page.goto('/gates/traefik')
  const p = panel(page)
  await expect(p.getByText("Porte d'entrée")).toBeVisible()
  await p.getByTestId('gate-routes').getByRole('button', { name: /admin/ }).click()
  await expect(p.getByTestId('rules')).toContainText('Service introuvable')
  await expect(page).toHaveURL(/\/routes\/ingressroute\/production\/admin$/)
})

test('PVC en attente : la raison est affichée', async ({ page }) => {
  await page.goto('/volumes/staging/uploads-preview')
  await expect(panel(page).getByText('PVC · staging')).toBeVisible()
  await expect(panel(page).getByTestId('pvc-why')).toContainText('WaitForFirstConsumer')
})

test('recherche d’une route par son hôte', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByTestId('pods-running')).toHaveText(/\d+\/\d+/)
  await page.keyboard.press('/')
  await page.getByRole('combobox').fill('grafana.example')
  await page.keyboard.press('Enter')
  await expect(panel(page).getByText('IngressRoute · monitoring')).toBeVisible()
  await expect(page).toHaveURL(/\/routes\/ingressroute\/monitoring\/grafana$/)
})
```

- [ ] **Step 2 : vérifier**

Run (depuis la racine) : `make e2e`
Expected: tous les tests Playwright passent, y compris les anciens (`demo`, `inspector`, `actions`, `finition`) : la ville agrandie ne doit pas casser le test « un clic dans la ville ouvre l'inspecteur », qui accepte désormais aussi un relais, une porte ou une citerne. Si ce test clique un objet réseau, adapter sa regex : `panel.getByText(/^(Pod|Node|Service|PVC|IngressRoute|Ingress|Porte)/)` et ne poursuivre la navigation node → pod que si le panneau est celui d'un node ou d'un pod.

- [ ] **Step 3 : commit**

```bash
git add web/e2e
git commit -m "test(e2e): portes, Services, routes cassées et PVC en mode démo"
```

### Task 28 : scénarios kind et test d'intégration

**Files:**
- Create: `hack/scenarios/network.yaml`, `hack/scenarios-traefik/ingressroute.yaml`
- Modify: `Makefile` (cible `scenarios`), `internal/kube/live_test.go`

- [ ] **Step 1 : scénarios**

Créer `hack/scenarios/network.yaml` :

```yaml
# Réseau et stockage (jalon 8) : Service sain, Service en panne (pods en
# CrashLoopBackOff), headless, ExternalName, Ingress nginx avec un backend
# manquant, StatefulSet avec PVC liés, PVC en attente de consommateur (la
# StorageClass « standard » de kind est en WaitForFirstConsumer).
apiVersion: v1
kind: Service
metadata: { name: api-gateway, namespace: production }
spec:
  selector: { app: api-gateway }
  ports: [{ name: http, port: 80, targetPort: 8080 }]
---
apiVersion: v1
kind: Service
metadata: { name: payment-worker, namespace: production }
spec:
  selector: { app: payment-worker }
  ports: [{ name: http, port: 8080 }]
---
apiVersion: v1
kind: Service
metadata: { name: postgres-payments, namespace: production }
spec:
  clusterIP: None
  selector: { app: postgres-payments }
  ports: [{ name: pg, port: 5432 }]
---
apiVersion: v1
kind: Service
metadata: { name: stripe-api, namespace: production }
spec: { type: ExternalName, externalName: api.stripe.com }
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: { name: storefront, namespace: production }
spec:
  ingressClassName: nginx
  rules:
    - host: shop.localtest.me
      http:
        paths:
          - { path: /api, pathType: Prefix, backend: { service: { name: api-gateway, port: { number: 80 } } } }
          - { path: /old, pathType: Prefix, backend: { service: { name: legacy-shop, port: { number: 80 } } } }
---
apiVersion: apps/v1
kind: StatefulSet
metadata: { name: orders-db, namespace: production }
spec:
  serviceName: orders-db
  replicas: 2
  selector: { matchLabels: { app: orders-db } }
  template:
    metadata: { labels: { app: orders-db } }
    spec:
      containers:
        - name: db
          image: registry.k8s.io/pause:3.10
          resources: { requests: { cpu: 50m, memory: 32Mi } }
          volumeMounts: [{ name: data, mountPath: /data }]
  volumeClaimTemplates:
    - metadata: { name: data }
      spec: { accessModes: [ReadWriteOnce], resources: { requests: { storage: 1Gi } } }
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: uploads, namespace: staging }
spec:
  accessModes: [ReadWriteOnce]
  resources: { requests: { storage: 1Gi } }
```

Créer `hack/scenarios-traefik/ingressroute.yaml` :

```yaml
# IngressRoute Traefik (CRD installée par make scenarios, sans contrôleur) :
# sa porte « traefik » apparaît, et sa route vers grafana est cassée (pas de
# Service grafana dans les scénarios).
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata: { name: grafana, namespace: monitoring }
spec:
  entryPoints: [web]
  routes:
    - match: Host(`grafana.localtest.me`)
      kind: Rule
      services: [{ name: grafana, port: 3000 }]
```

Dans le `Makefile`, remplacer la cible `scenarios` :

```make
# CRD Traefik (IngressRoute) : https://doc.traefik.io/traefik/reference/install-configuration/providers/kubernetes/kubernetes-crd/
TRAEFIK_CRD ?= https://raw.githubusercontent.com/traefik/traefik/v3.5/docs/content/reference/dynamic-configuration/kubernetes-crd-definition-v1.yml

scenarios:
	kubectl --context $(KIND_CTX) apply -f hack/scenarios/
	kubectl --context $(KIND_CTX) apply --server-side -f $(TRAEFIK_CRD)
	kubectl --context $(KIND_CTX) wait --for condition=established crd/ingressroutes.traefik.io --timeout=60s
	kubectl --context $(KIND_CTX) apply -f hack/scenarios-traefik/
```

Run: `make kind-up scenarios` (ou `make scenarios` sur un cluster existant), puis `kubectl --context kind-atlas get svc,ingress,pvc -A | grep -E 'production|staging'` et `kubectl --context kind-atlas -n monitoring get ingressroute`
Expected: les Services, l'Ingress `storefront`, les PVC `data-orders-db-0/1` (Bound) et `uploads` (Pending), l'IngressRoute `grafana`. Si l'URL de la CRD a changé, reprendre celle indiquée par la documentation Traefik citée dans le Makefile.

- [ ] **Step 2 : test d'intégration**

Dans `internal/kube/live_test.go`, extraire le démarrage et l'attente de `TestLiveLatency` en deux aides (imports supplémentaires : `"k8s.io/client-go/dynamic"`) :

```go
// startLive branche une source et un hub sur le cluster kind (ATLAS_CONTEXT, kind-atlas par défaut).
func startLive(t *testing.T) (*kubernetes.Clientset, *stream.Subscription, context.Context) {
	t.Helper()
	ctxName := os.Getenv("ATLAS_CONTEXT")
	if ctxName == "" {
		ctxName = "kind-atlas"
	}
	rc, err := RestConfig("", ctxName)
	if err != nil {
		t.Skipf("pas de cluster : %v", err)
	}
	client := kubernetes.NewForConfigOrDie(rc)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	hub := stream.NewHub(stream.Options{})
	go hub.Run(ctx)
	src := NewSource(client, hub, Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Dynamic: dynamic.NewForConfigOrDie(rc)})
	go func() { _ = src.Run(ctx) }()
	for !hub.Ready() {
		time.Sleep(50 * time.Millisecond)
	}
	_, sub := hub.Subscribe(hub.Rev())
	t.Cleanup(sub.Close)
	return client, sub, ctx
}

// waitFor attend un message diffusé qui satisfait match et renvoie le délai écoulé.
func waitFor(t *testing.T, sub *stream.Subscription, what string, match func(stream.Message) bool) time.Duration {
	t.Helper()
	start := time.Now()
	for {
		select {
		case batch := <-sub.C:
			for _, m := range batch {
				if match(m) {
					return time.Since(start)
				}
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s : rien reçu en 10 s", what)
		}
	}
}
```

`TestLiveLatency` commence alors par `client, sub, ctx := startLive(t)` et appelle `waitFor(t, sub, …)` à la place de sa fonction locale `wait` (comportement inchangé). Ajouter :

```go
// TestLiveServiceEndpoints : supprimer un pod endpoint met à jour son Service en moins de 2 s.
func TestLiveServiceEndpoints(t *testing.T) {
	client, sub, ctx := startLive(t)
	const ns = "atlas-it-svc"
	_, _ = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{})
	t.Cleanup(func() { _ = client.CoreV1().Namespaces().Delete(context.Background(), ns, metav1.DeleteOptions{}) })

	labels := map[string]string{"app": "probe"}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: ns},
		Spec: corev1.ServiceSpec{Selector: labels, Ports: []corev1.ServicePort{{Port: 80}}}}
	if _, err := client.CoreV1().Services(ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: ns, Labels: labels},
		Spec: corev1.PodSpec{TerminationGracePeriodSeconds: new(int64), Containers: []corev1.Container{{Name: "c", Image: "registry.k8s.io/pause:3.10"}}}}
	if _, err := client.CoreV1().Pods(ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	endpoints := func(n int) func(stream.Message) bool {
		return func(m stream.Message) bool {
			s, ok := m.Obj.(model.Service)
			return m.Type == "upsert" && ok && s.Namespace == ns && s.Name == "probe" && len(s.Endpoints) == n
		}
	}
	_ = waitFor(t, sub, "endpoint ajouté", endpoints(1))
	if err := client.CoreV1().Pods(ns).Delete(ctx, "probe", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	gone := waitFor(t, sub, "endpoint retiré", endpoints(0))
	t.Logf("endpoint retiré visible en %v", gone)
	if gone > 2*time.Second {
		t.Errorf("endpoint retiré visible en %v (> 2 s)", gone)
	}
}
```

- [ ] **Step 3 : vérifier**

Run: `go vet -tags integration ./internal/kube/ && make test-integration`
Expected: `TestLiveLatency` et `TestLiveServiceEndpoints` passent sur kind (`--- PASS`).

Run (cluster kind avec Dex, voir README) : `make e2e-auth`
Expected: PASS ; le test de bob, qui vérifie déjà qu'aucune frame ne contient `kube-system`, couvre aussi le Service `kube-dns` désormais diffusé.

- [ ] **Step 4 : commit**

```bash
git add hack Makefile internal/kube/live_test.go
git commit -m "test: scénarios kind réseau et stockage (CRD Traefik), endpoint retiré en moins de 2 s"
```

### Task 29 : banc de charge

**Files:**
- Modify: `hack/load/kwok-up.sh`

- [ ] **Step 1 : Services et PVC kwok**

Dans `hack/load/kwok-up.sh`, ajouter `SERVICES_PER_DEPLOY="${SERVICES_PER_DEPLOY:-4}"` et `PVCS="${PVCS:-150}"` aux variables, puis dans le heredoc des Deployments, après le Deployment :

```sh
    s=1
    while [ "$s" -le "$SERVICES_PER_DEPLOY" ]; do
      cat <<YAML
---
apiVersion: v1
kind: Service
metadata: { name: load-$(printf %03d "$d")-$s, namespace: load-test }
spec:
  selector: { app: load-$(printf %03d "$d") }
  ports: [{ port: 80 }]
YAML
      s=$((s + 1))
    done
```

et après la boucle des Deployments (avant l'attente des pods) :

```sh
# PVC sans consommateur (Pending) : des citernes dans les entrepôts.
v=1
{
  while [ "$v" -le "$PVCS" ]; do
    cat <<YAML
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: data-$(printf %03d "$v"), namespace: load-test }
spec: { accessModes: [ReadWriteOnce], resources: { requests: { storage: 1Gi } } }
YAML
    v=$((v + 1))
  done
} | k apply -f - >/dev/null
echo "$((DEPLOYS * SERVICES_PER_DEPLOY)) Services et $PVCS PVC créés dans load-test"
```

(`kwok-down.sh` supprime déjà le namespace `load-test` : rien à changer.)

- [ ] **Step 2 : mesurer**

Run : `make run-kind` après `make load-up`, puis ouvrir http://localhost:8080/?perf=1.
Expected: environ 400 relais (regroupés par namespace sur les avenues), 150 citernes ; le compteur `?perf=1` reste à 60 images/s ou plus au repos sur un portable récent, et le rendu retombe à 15 images/s (paquets) puis à 0 quand on dézoome au maximum.

Run : `go run ./cmd/atlas --demo --demo-scale 100x30` puis http://localhost:8080/?perf=1
Expected: même constat avec environ 450 Services et 230 PVC simulés.

Noter les mesures (images/s, temps de frame, draw calls) dans le README (tâche 30).

- [ ] **Step 3 : commit**

```bash
git add hack/load
git commit -m "test(charge): Services et PVC dans le banc kwok"
```

### Task 30 : documentation

**Files:**
- Modify: `README.md`, `docs/spec.md`, `docs/superpowers/specs/2026-10-07-jalon-8-reseau-stockage-design.md`

- [ ] **Step 1 : README**

- Introduction : « … chaque pod un bloc ; les Services sont des relais sur les avenues, les entrées (Ingress, IngressRoute Traefik) des portes à l'ouest, les PVC des citernes dans le quartier Entrepôts. »
- Section « Sur un vrai cluster (kind) » : `make scenarios` installe aussi la CRD Traefik et ajoute Services, Ingress, IngressRoute, StatefulSet avec PVC et un PVC en attente.
- **Droits du ServiceAccount** : ajouter `list`/`watch` sur services, persistentvolumeclaims, endpointslices, ingresses, ingressclasses et ingressroutes (`traefik.io`, `traefik.containo.us`) ; « un type que le ServiceAccount ne peut pas lister est désactivé (warning dans les logs) ; une CRD Traefik installée après le démarrage est prise en compte au prochain redémarrage ».
- **Architecture**, nouvelle puce :

  ```markdown
  - **Réseau et stockage** (`internal/kube/source_net.go`, `traefik.go`) : Services (santé et endpoints lus dans les EndpointSlices), routes (Ingress et IngressRoute ramenées à une porte : IngressClass, annotation, classe par défaut, ou `traefik`) et PVC (pods qui les montent, lus dans le cache des pods) sont trois kinds du flux, calculés par la même boucle d'objets sales. Côté front, `netLayout.ts` range les relais par namespace sur les avenues, les portes à l'entrée ouest et les citernes par StorageClass ; `links.ts` trace les liens en angles droits par les rues ; sélectionner un objet allume son chemin (porte → Service → pods → PVC). Lignes principales et conduites sont toujours visibles, les fibres Service → pods seulement au survol ou à la sélection ; les paquets défilent à 15 images/s.
  ```

- **Performance** : les mesures de la tâche 29.
- Liens profonds : `/services/<ns>/<nom>`, `/routes/<ingress|ingressroute>/<ns>/<nom>`, `/volumes/<ns>/<nom>`, `/gates/<nom>`.

- [ ] **Step 2 : spécification**

Dans `docs/spec.md` :
- Table « Correspondances » : ajouter les lignes Porte, Route, Relais, Fibre, Citerne, Conduite reprises de la table du design du jalon 8.
- « Hors MVP » : retirer « Services, Ingress, PVC représentés dans la 3D » (renvoyer au design du jalon 8).
- « Plan de livraison » : ajouter « 8. **Réseau et stockage** : Services, Ingress et IngressRoute, PVC dans la ville ; design `docs/superpowers/specs/2026-10-07-jalon-8-reseau-stockage-design.md`. »

Dans le design du jalon 8 : remplacer les liens profonds `?select=…` par les chemins ci-dessus, et le banc « environ 400 Services et 150 PVC » par « environ 450 Services et 230 PVC en démo (`--demo-scale 100x30`), 400 Services et 150 PVC sur kwok ».

- [ ] **Step 3 : commit**

```bash
git add README.md docs
git commit -m "docs: README et spécification du jalon 8"
```

### Task 31 : vérification finale

- [ ] **Step 1 : tout lancer**

Run (racine) : `gofmt -l . && go vet ./... && make test && make e2e && helm lint deploy/helm/cluster-atlas --set auth.oidc.clientID=ci --set publicURL=https://atlas.example.com && go run github.com/rhysd/actionlint/cmd/actionlint@latest`
Expected: aucune sortie de `gofmt`, tous les tests verts, `0 chart(s) failed`.

- [ ] **Step 2 : critères de fin du design**

Cocher, preuves à l'appui (sortie de commande ou capture dans `web/e2e/__screenshots__/`) :
- tests Go, Vitest et e2e démo verts ;
- `make test-integration` vert sur kind (endpoint retiré en moins de 2 s) ;
- `make e2e-auth` vert (bob ne reçoit rien de `kube-system`) ;
- banc 3 000 pods et environ 400 Services tenu (tâche 29) ;
- README et `docs/spec.md` à jour.

Un critère qui ne passe pas se signale tel quel, avec sa sortie, au lieu d'être coché.
