# Jalon 9 — Suite du réseau et du stockage : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** compléter le réseau et le stockage de la ville : Gateway API (Gateways en portes, HTTPRoute et GRPCRoute), Traefik complet (IngressRouteTCP/UDP, résolution des TraefikService), prise en compte à chaud des CRD, PersistentVolumes sans PVC. Design : [`docs/superpowers/specs/2026-10-07-jalon-9-reseau-suite-design.md`](../specs/2026-10-07-jalon-9-reseau-suite-design.md).

**Architecture :** un registre de types dynamiques (`internal/kube/dynkinds.go`) démarre et arrête un informer `unstructured` par ressource quand la CRD correspondante apparaît ou disparaît (informer dynamique sur `customresourcedefinitions`) ; Traefik y migre. Deux nouveaux kinds dans `/api/stream` (`gateway`, `persistentVolume`), le kind `route` gagne quatre sources et des champs optionnels (`gates`, `parents`, `weight`, `mirror`, `percent`, `via`). Le simulateur produit le même modèle. Le front déduit toujours les portes des routes, en les enrichissant des Gateways reçus, et dessine les PV orphelins en citernes vides.

**Tech Stack :** Go 1.26, client-go v0.37 (informers dynamiques, `dynamicinformer.NewFilteredDynamicInformer`, fake dynamic client), React 19, React Three Fiber, three.js, Zustand, Vitest, Playwright, Helm.

**Commits :** messages en français au format des jalons précédents (`feat(kube): …`) ; chaque message se termine par la ligne `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

---

## Décisions

1. **Clés** : Gateway `namespace/name` ; PV `name` ; routes `Source/namespace/name` avec `Source` ∈ `Ingress`, `IngressRoute`, `IngressRouteTCP`, `IngressRouteUDP`, `HTTPRoute`, `GRPCRoute`. Côté front, désignations `gateway:ns/name`, `pv:name`, et les existantes (`gate:<nom>`, `service:…`, `route:…`, `volume:…`, `pod:<uid>`). Une porte Gateway a pour nom `ns/name` (`gate:infra/public`) ; le `/` distingue un Gateway d'une IngressClass.
2. **Tri-état** : `accepted`, `programmed`, `ready` (listener) et les conditions de route valent `"true"`, `"false"` ou `"unknown"` (condition absente). Constantes Go `model.CondTrue`, `model.CondFalse`, `model.CondUnknown`.
3. **Poids** : `weight` = part du trafic en pour mille (entier, arrondi), même unité pour Gateway API et Traefik, publié seulement quand la règle source a plusieurs backends ; en Go c'est un `*int` (`model.Weight(n)`) pour qu'un poids nul soit publié. Miroirs : `mirror: true`, `percent` (0 à 100), pas de `weight`.
4. **`gates`** : présent sur toutes les routes, jamais vide ; `gate == gates[0]`. Route Gateway API sans `parentRef` de kind Gateway : `gates = ["(sans gateway)"]`.
5. **Registre dynamique** : un informer par GVR, arrêtable seul. Arrêt : on retire d'abord l'informer du registre, puis on marque chaque objet de son store (`on(o)`) ; la réconciliation, ne trouvant plus le type, publie les suppressions (dans l'ordre inverse, une réconciliation intercalée republierait les objets). Aucun accès à `s.last` hors de la boucle de réconciliation. Le registre est protégé par un `sync.RWMutex`. Les informers démarrés à chaud n'entrent pas dans `s.synced` (`/readyz` ne les attend pas).
6. **Repli** : si `customresourcedefinitions` n'est pas listable, découverte d'API au démarrage (généralisation de `traefikGroupsServed`), sans prise en compte à chaud ; warning.
7. **Gateway API** : version `v1` seulement, lue en `unstructured`, aucune dépendance `sigs.k8s.io/gateway-api`. État des backends d'après `status.parents[]` (pas de calcul des ReferenceGrant).
8. **TraefikService** : résolution récursive, profondeur maximale 8, détection de cycle ; même nom dans les deux groupes Traefik : `traefik.io` l'emporte. L'état `indirect` n'est plus produit.
9. **PV publiés** : seulement sans PVC existant (phase `Available`, `Released`, `Failed`, ou `Bound` avec `claimRef` vers un PVC disparu, ou PVC non listables : alors seuls `Available`, `Released`, `Failed`).
10. **YAML et événements des objets cluster-scoped** : le segment de namespace vaut `_` (`/api/yaml/core/v1/PersistentVolume/_/pv-1`, `/api/namespaces/_/persistentvolumes/pv-1/events`) ; le serveur le traduit en namespace vide.
11. **Liens profonds** : `/gateways/<ns>/<nom>`, `/persistentvolumes/<nom>`, `/routes/<httproute|grpcroute|ingressroutetcp|ingressrouteudp|ingress|ingressroute>/<ns>/<nom>` ; `/gates/<nom>` accepte un nom encodé contenant `/` (`/gates/infra%2Fpublic`), mais un Gateway visible redirige vers `/gateways/…`.

## Contrats partagés

Ces signatures sont la référence de toutes les phases ; une tâche qui en a besoin les reprend telles quelles.

### Go — `internal/model/network.go` (ajouts)

```go
const (
	SourceIngress         = "Ingress"
	SourceIngressRoute    = "IngressRoute"
	SourceIngressRouteTCP = "IngressRouteTCP"
	SourceIngressRouteUDP = "IngressRouteUDP"
	SourceHTTPRoute       = "HTTPRoute"
	SourceGRPCRoute       = "GRPCRoute"
)

const (
	CondTrue    = "true"
	CondFalse   = "false"
	CondUnknown = "unknown"
)

const BackendRefused = "refused" // route refusée par toutes ses Gateways (Accepted=False)

// NoGateway : porte d'une route Gateway API sans parentRef de kind Gateway.
const NoGateway = "(sans gateway)"

// Backend gagne (tous optionnels) :
//	Weight  *int   `json:"weight,omitempty"`  // part du trafic, pour mille ; nil : un seul backend

func Weight(n int) *int { return &n } // un poids nul reste publié
//	Mirror  bool   `json:"mirror,omitempty"`
//	Percent int    `json:"percent,omitempty"` // miroir
//	Via     string `json:"via,omitempty"`     // « ns/name » du TraefikService racine

// RouteParent : état d'une route Gateway API vis-à-vis d'un de ses Gateways.
type RouteParent struct {
	Gateway      string `json:"gateway"` // « ns/name »
	Accepted     string `json:"accepted"`
	ResolvedRefs string `json:"resolvedRefs"`
	Reason       string `json:"reason,omitempty"`
}

// Route gagne :
//	Gates   []string      `json:"gates"`
//	Parents []RouteParent `json:"parents,omitempty"`

type Listener struct {
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	Port           int32  `json:"port"`
	Hostname       string `json:"hostname,omitempty"`
	AttachedRoutes int32  `json:"attachedRoutes"`
	Ready          string `json:"ready"`
}

type Gateway struct {
	Namespace  string     `json:"namespace"`
	Name       string     `json:"name"`
	Class      string     `json:"class"`
	Accepted   string     `json:"accepted"`
	Programmed string     `json:"programmed"`
	Reason     string     `json:"reason,omitempty"`
	Message    string     `json:"message,omitempty"`
	Addresses  []string   `json:"addresses,omitempty"`
	Listeners  []Listener `json:"listeners"`
}

type PersistentVolume struct {
	Name          string   `json:"name"`
	StorageClass  string   `json:"storageClass"`
	Capacity      int64    `json:"capacity"`
	AccessModes   []string `json:"accessModes"`
	ReclaimPolicy string   `json:"reclaimPolicy"`
	Phase         string   `json:"phase"` // Available | Released | Failed | Bound
	ClaimRef      string   `json:"claimRef,omitempty"` // « ns/name »
}

func GatewayKey(g Gateway) string                   { return g.Namespace + "/" + g.Name }
func PersistentVolumeKey(p PersistentVolume) string { return p.Name }
```

### Go — `internal/stream`

`KindGateway Kind = "gateway"`, `KindPersistentVolume Kind = "persistentVolume"` ; `Message` gagne `Gateways []model.Gateway \`json:"gateways,omitempty"\`` et `PersistentVolumes []model.PersistentVolume \`json:"persistentVolumes,omitempty"\``, remplis par le snapshot (hub et filtre ws) et triés par clé.

### Go — `internal/kube/dynkinds.go`

```go
// dynKind : type optionnel apporté par une CRD.
type dynKind struct {
	gvr      schema.GroupVersionResource
	on       func(s *Source, o any) // marque l'objet (et ce qui en dépend)
	indexers func(s *Source) cache.Indexers
}

func (k dynKind) crd() string { return k.gvr.Resource + "." + k.gvr.Group }

// dynKinds : registre complet, dans l'ordre de démarrage.
var dynKinds []dynKind

type dynInformer struct {
	kind dynKind
	inf  cache.SharedIndexInformer
	stop chan struct{}
}

// Méthodes de Source :
func (s *Source) dynIndexer(gvr schema.GroupVersionResource) cache.Indexer // nil si le type n'est pas démarré
func (s *Source) startDyn(ctx context.Context, k dynKind) bool             // sonde, démarre, attend la synchro, marque tout
func (s *Source) stopDyn(gvr schema.GroupVersionResource)                  // retire, marque tout, arrête
```

GVR utilisés (variables de package) : `gvrIngressRoute(g)`, `gvrIngressRouteTCP(g)`, `gvrIngressRouteUDP(g)`, `gvrTraefikService(g)` pour `g` ∈ `traefikGroups`, et `gvrGateway`, `gvrHTTPRoute`, `gvrGRPCRoute` (groupe `gateway.networking.k8s.io`, version `v1`). Index : `indexByBackend` (existant, « ns/service »), `indexByTraefikService` (« ns/name » des TraefikService visés), `indexByGateway` (« ns/name » des Gateways parents).

### TypeScript — `web/src/api/types.ts` (ajouts)

```ts
export type Tri = 'true' | 'false' | 'unknown'
export type RouteSource = 'Ingress' | 'IngressRoute' | 'IngressRouteTCP' | 'IngressRouteUDP' | 'HTTPRoute' | 'GRPCRoute'

// Backend : state: 'ok' | 'missing' | 'refused' | 'indirect' ; weight?: number ; mirror?: boolean ; percent?: number ; via?: string
// Route : source: RouteSource ; gates: string[] ; parents?: RouteParent[]

export interface RouteParent { gateway: string; accepted: Tri; resolvedRefs: Tri; reason?: string }
export interface Listener { name: string; protocol: string; port: number; hostname?: string; attachedRoutes: number; ready: Tri }
export interface Gateway {
  namespace: string; name: string; class: string; accepted: Tri; programmed: Tri
  reason?: string; message?: string; addresses?: string[]; listeners: Listener[]
}
export interface PersistentVolume {
  name: string; storageClass: string; capacity: number; accessModes: string[]
  reclaimPolicy: string; phase: 'Available' | 'Released' | 'Failed' | 'Bound'; claimRef?: string
}
// Kind gagne 'gateway' | 'persistentVolume' ; Message : snapshot.gateways?, snapshot.persistentVolumes?, et les upsert/delete correspondants.
export const gatewayKey = (g: Pick<Gateway, 'namespace' | 'name'>) => `${g.namespace}/${g.name}`
export const pvKey = (p: Pick<PersistentVolume, 'name'>) => p.name
```

### TypeScript — store et dérivés

- `useCluster` gagne `gateways: Map<string, Gateway>` et `persistentVolumes: Map<string, PersistentVolume>` ; `SelectionType` gagne `'gateway'` et `'pv'`.
- `web/src/store/net.ts` : `Gate` devient `{ name: string; routes: Route[]; broken: number; refused: number; gateway?: Gateway }` ; `gatesOf(routes, gateways?: ReadonlyMap<string, Gateway>)` range une route sous chacune de ses `gates` (repli sur `[gate]`) ; `routeBroken(r)` (backend `missing`) ; `routeRefused(r)` (backend `refused`) ; `isGatewayGate(name) = name.includes('/')`.
- `web/src/scene/health.ts` : `gateSignal(g: Gate)` : Gateway `programmed === 'false'` → `err` ; listener non ready (`'false'`), route cassée ou refusée → `warn` ; porte Gateway sans objet ou `programmed === 'unknown'` → `mute` ; sinon `ok`. Portes déduites (sans `/`) : règle du jalon 8. `pvSignal(p)` : `Failed` → `err`, sinon `mute`.
- `web/src/scene/netLayout.ts` : `layoutNetwork(city, relays, gates, tanks, orphans: OrphanInput[] = [])` avec `OrphanInput = { key: string; name: string; storageClass: string; capacity: number }` ; `NetLayout` gagne `orphans: TankSlot[]` (même îlot que la classe, après les PVC).
- `web/src/scene/links.ts` : `Family` gagne `'mirror'` et `'refused'` ; `Link` gagne `weight?: number`.

---

## Fichiers

**Backend**
- Modifier `internal/model/network.go` (+ `model_test.go`) : sources, tri-état, `BackendRefused`, `NoGateway`, `Weight`, `RouteParent`, `Gateway`, `Listener`, `PersistentVolume`, clés.
- Modifier `internal/stream/hub.go`, `internal/stream/ws.go` (+ tests) : kinds `gateway` et `persistentVolume`, snapshot.
- Créer `internal/kube/dynkinds.go` (+ `dynkinds_test.go`, qui reprend `traefik_test.go`) : registre des types dynamiques, `servedKinds`.
- Créer `internal/kube/crd.go` (+ `crd_test.go`) : suivi des CRD à chaud.
- Remplacer `internal/kube/traefik.go` : GVR et entrées Traefik du registre (IngressRoute, TCP, UDP, TraefikService), remontée des TraefikService.
- Créer `internal/kube/gateway.go`, `internal/kube/gateway_convert.go` (+ `_test`) : Gateway API.
- Modifier `internal/kube/network.go` (+ `network_test.go`, `traefik_convert_test.go`) : `gates`, routes Traefik TCP/UDP, `resolveTraefik`, `ConvertPV`.
- Modifier `internal/kube/source.go`, `internal/kube/source_net.go` (+ `source_gw_test.go`, `source_pv_test.go`) : registre, PV, `build` des nouveaux kinds.
- Modifier `internal/access/filter.go` (+ tests) : droits des nouveaux kinds.
- Modifier `internal/inspect/inspect.go`, `internal/server/server.go` (+ tests) : kinds YAML, segment `_`.
- Modifier `internal/demo/{network.go,scale.go,inspect.go}` (+ tests) : Gateway API, Traefik complet, PV simulés.
- Modifier `deploy/helm/cluster-atlas/templates/clusterrole.yaml`, `deploy/helm/chart_test.go`.

**Front**
- Modifier `web/src/api/types.ts`, `web/src/api/inspect.ts`, `web/src/store/{cluster.ts,fixtures.ts,net.ts}` (+ tests).
- Modifier `web/src/scene/{health.ts,netLayout.ts,links.ts,world.ts,Network.tsx,GroundLinks.tsx,Selection.tsx}` (+ tests).
- Modifier `web/src/inspector/{Inspector.tsx,NetOverview.tsx}`, `web/src/ui/{format.ts,tree.ts,ListView.tsx,searchRank.ts,Search.tsx,route.ts,PathSummary.tsx}` (+ tests).
- Créer `web/e2e/gateway.spec.ts` ; modifier `web/e2e/auth.spec.ts`.

**Banc et docs**
- Créer `hack/scenarios/30-storage.yaml`, `hack/scenarios-gateway/{gateways.yaml,status.sh}` ; modifier `hack/scenarios-traefik/ingressroute.yaml`, `hack/dev-rbac.yaml`, `Makefile` (cible `crds`, CRD épinglées dans `hack/crds/.cache/`), `.gitignore`.
- Modifier `internal/kube/live_test.go`, `hack/load/kwok-up.sh`, `hack/load/kwok-down.sh`.
- Modifier `README.md`, `docs/spec.md`, la spec du jalon.

---

## Phase A — Modèle commun, registre dynamique et CRD à chaud

### Task 1 : modèle commun du jalon 9

**Files:**
- Modify: `internal/model/network.go`
- Modify: `internal/kube/network.go` (`ConvertIngress`, `ConvertIngressRoute`)
- Modify: `internal/demo/network.go` (`withBackendStates`)
- Test: `internal/model/model_test.go`, `internal/kube/network_test.go`, `internal/demo/sim_test.go`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `internal/model/model_test.go` :

```go
func TestGatewayAndPersistentVolumeKeys(t *testing.T) {
	if k := GatewayKey(Gateway{Namespace: "infra", Name: "public"}); k != "infra/public" {
		t.Errorf("GatewayKey = %s", k)
	}
	if k := PersistentVolumeKey(PersistentVolume{Name: "pv-1"}); k != "pv-1" {
		t.Errorf("PersistentVolumeKey = %s", k)
	}
}

func TestRouteJSONCarriesGatesAndOmitsOptionalFields(t *testing.T) {
	r := Route{Source: SourceIngress, Namespace: "prod", Name: "web", Gate: "nginx", Gates: []string{"nginx"},
		Rules: []Rule{{Backend: Backend{Namespace: "prod", Service: "api", Kind: "Service", State: BackendOK}}}}
	b, _ := json.Marshal(r)
	s := string(b)
	if !strings.Contains(s, `"gates":["nginx"]`) {
		t.Errorf("gates absent : %s", s)
	}
	for _, f := range []string{"weight", "mirror", "percent", "via", "parents"} {
		if strings.Contains(s, `"`+f+`"`) {
			t.Errorf("%s doit être omis : %s", f, s)
		}
	}
	w, _ := json.Marshal(Backend{Kind: "Service", State: BackendOK, Weight: Weight(900), Via: "prod/canary"})
	if z, _ := json.Marshal(Backend{Kind: "Service", State: BackendOK, Weight: Weight(0)}); !strings.Contains(string(z), `"weight":0`) {
		t.Errorf("un poids nul doit être publié : %s", z)
	}
	if !strings.Contains(string(w), `"weight":900`) || !strings.Contains(string(w), `"via":"prod/canary"`) {
		t.Errorf("backend = %s", w)
	}
	g, _ := json.Marshal(Gateway{Namespace: "infra", Name: "public", Accepted: CondTrue, Programmed: CondUnknown,
		Listeners: []Listener{{Name: "http", Protocol: "HTTP", Port: 80, Ready: CondFalse}}})
	if gs := string(g); !strings.Contains(gs, `"programmed":"unknown"`) || !strings.Contains(gs, `"attachedRoutes":0`) || !strings.Contains(gs, `"ready":"false"`) {
		t.Errorf("gateway = %s", gs)
	}
}
```

Ajouter à `internal/kube/network_test.go` :

```go
func TestRoutesCarryTheirGate(t *testing.T) {
	cls := "nginx"
	i := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod"}, Spec: networkingv1.IngressSpec{IngressClassName: &cls}}
	if r := ConvertIngress(i, "nginx", nil); len(r.Gates) != 1 || r.Gates[0] != "nginx" || r.Gate != "nginx" {
		t.Errorf("Ingress : gate %q, gates %v", r.Gate, r.Gates)
	}
	u := ingressRoute("traefik.io", "mon", "grafana", []any{})
	if r := ConvertIngressRoute(u, nil); len(r.Gates) != 1 || r.Gates[0] != "traefik" {
		t.Errorf("IngressRoute : gates %v", r.Gates)
	}
}
```

Dans `internal/demo/sim_test.go`, à la fin de `TestDemoNetworkAndStorage`, avant le commentaire « Les endpoints suivent les pods. », ajouter :

```go
	for k, o := range sink.net {
		if r, ok := o.(model.Route); ok && (len(r.Gates) == 0 || r.Gates[0] != r.Gate) {
			t.Errorf("%s : gates %v, gate %q", k, r.Gates, r.Gate)
		}
	}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/model/ ./internal/kube/ ./internal/demo/ -run 'TestGatewayAndPersistentVolumeKeys|TestRouteJSONCarriesGatesAndOmitsOptionalFields|TestRoutesCarryTheirGate|TestDemoNetworkAndStorage'`
Expected: FAIL à la compilation (`undefined: GatewayKey`, `r.Gates undefined`).

- [ ] **Step 3 : modèle**

Dans `internal/model/network.go`, remplacer le bloc des états de backend et les types `Backend` et `Route` :

```go
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
```

par :

```go
// État du backend d'une règle de route.
const (
	BackendOK       = "ok"
	BackendMissing  = "missing"  // Service introuvable
	BackendRefused  = "refused"  // route refusée par toutes ses Gateways (Accepted=False)
	BackendIndirect = "indirect" // TraefikService non résolu (jalon 8 ; plus produit, gardé pour la compatibilité)
)

// Sources d'une route.
const (
	SourceIngress         = "Ingress"
	SourceIngressRoute    = "IngressRoute"
	SourceIngressRouteTCP = "IngressRouteTCP"
	SourceIngressRouteUDP = "IngressRouteUDP"
	SourceHTTPRoute       = "HTTPRoute"
	SourceGRPCRoute       = "GRPCRoute"
)

// Tri-état d'une condition Kubernetes ; unknown : condition absente (pas de contrôleur).
const (
	CondTrue    = "true"
	CondFalse   = "false"
	CondUnknown = "unknown"
)

// NoGateway : porte d'une route Gateway API sans parentRef de kind Gateway.
const NoGateway = "(sans gateway)"

// Weight : poids d'un backend (un poids nul doit rester publié, d'où le pointeur).
func Weight(n int) *int { return &n }

type Backend struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      string `json:"port,omitempty"`
	Kind      string `json:"kind"` // Service | TraefikService
	State     string `json:"state"`
	Weight    *int   `json:"weight,omitempty"`  // part du trafic de la règle source, pour mille ; nil : règle à un seul backend
	Mirror    bool   `json:"mirror,omitempty"`  // copie du trafic (miroir Traefik)
	Percent   int    `json:"percent,omitempty"` // part du trafic copiée vers le miroir
	Via       string `json:"via,omitempty"`     // « ns/name » du TraefikService racine
}
```

Puis remplacer le type `Route` :

```go
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
```

par :

```go
// RouteParent : état d'une route Gateway API vis-à-vis d'un de ses Gateways.
type RouteParent struct {
	Gateway      string `json:"gateway"` // « ns/name »
	Accepted     string `json:"accepted"`
	ResolvedRefs string `json:"resolvedRefs"`
	Reason       string `json:"reason,omitempty"`
}

// Route : une Ingress, une IngressRoute Traefik (HTTP, TCP, UDP) ou une route
// Gateway API, rattachée à ses portes (contrôleurs d'entrée ou Gateways).
type Route struct {
	Source    string        `json:"source"` // une des constantes Source*
	Group     string        `json:"group"`  // networking.k8s.io | traefik.io | traefik.containo.us | gateway.networking.k8s.io
	Namespace string        `json:"namespace"`
	Name      string        `json:"name"`
	Gate      string        `json:"gate"`  // Gates[0], pour la compatibilité
	Gates     []string      `json:"gates"` // jamais vide
	Rules     []Rule        `json:"rules"`
	Addresses []string      `json:"addresses,omitempty"`
	Parents   []RouteParent `json:"parents,omitempty"`
}
```

Ajouter, après le type `Volume` :

```go
type Listener struct {
	Name           string `json:"name"`
	Protocol       string `json:"protocol"`
	Port           int32  `json:"port"`
	Hostname       string `json:"hostname,omitempty"`
	AttachedRoutes int32  `json:"attachedRoutes"`
	Ready          string `json:"ready"`
}

// Gateway : un Gateway de la Gateway API, dessiné en porte.
type Gateway struct {
	Namespace  string     `json:"namespace"`
	Name       string     `json:"name"`
	Class      string     `json:"class"`
	Accepted   string     `json:"accepted"`
	Programmed string     `json:"programmed"`
	Reason     string     `json:"reason,omitempty"`
	Message    string     `json:"message,omitempty"`
	Addresses  []string   `json:"addresses,omitempty"`
	Listeners  []Listener `json:"listeners"`
}

// PersistentVolume : un PV sans PVC existant (citerne vide). Capacité en octets.
type PersistentVolume struct {
	Name          string   `json:"name"`
	StorageClass  string   `json:"storageClass"`
	Capacity      int64    `json:"capacity"`
	AccessModes   []string `json:"accessModes"`
	ReclaimPolicy string   `json:"reclaimPolicy"`
	Phase         string   `json:"phase"`              // Available | Released | Failed | Bound
	ClaimRef      string   `json:"claimRef,omitempty"` // « ns/name »
}
```

Et, après `VolumeKey` :

```go
func GatewayKey(g Gateway) string                   { return g.Namespace + "/" + g.Name }
func PersistentVolumeKey(p PersistentVolume) string { return p.Name }
```

- [ ] **Step 4 : portes des routes existantes**

Dans `internal/kube/network.go`, `ConvertIngress` :

```go
	r := model.Route{Source: "Ingress", Group: "networking.k8s.io", Namespace: i.Namespace, Name: i.Name, Gate: gate, Rules: []model.Rule{}}
```

devient :

```go
	r := model.Route{Source: model.SourceIngress, Group: "networking.k8s.io", Namespace: i.Namespace, Name: i.Name,
		Gate: gate, Gates: []string{gate}, Rules: []model.Rule{}}
```

et dans `ConvertIngressRoute` :

```go
	r := model.Route{Source: "IngressRoute", Group: u.GroupVersionKind().Group, Namespace: u.GetNamespace(), Name: u.GetName(), Gate: gate, Rules: []model.Rule{}}
```

devient :

```go
	r := model.Route{Source: model.SourceIngressRoute, Group: u.GroupVersionKind().Group, Namespace: u.GetNamespace(), Name: u.GetName(),
		Gate: gate, Gates: []string{gate}, Rules: []model.Rule{}}
```

Dans `internal/demo/network.go`, `withBackendStates`, après `out := r` :

```go
	if len(out.Gates) == 0 {
		out.Gates = []string{r.Gate}
	}
```

- [ ] **Step 5 : vérifier**

Run: `go test ./internal/model/ ./internal/kube/ ./internal/demo/`
Expected: PASS.

- [ ] **Step 6 : commit**

```bash
git add internal/model internal/kube/network.go internal/kube/network_test.go internal/demo/network.go internal/demo/sim_test.go
git commit -m "feat(model): Gateways, PV orphelins, portes multiples et backends pondérés

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2 : kinds `gateway` et `persistentVolume` dans le flux

**Files:**
- Modify: `internal/stream/hub.go`, `internal/stream/ws.go`
- Test: `internal/stream/hub_test.go`, `internal/stream/ws_test.go`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `internal/stream/hub_test.go` :

```go
func TestSnapshotCarriesGatewaysAndPersistentVolumes(t *testing.T) {
	h := newTestHub(16)
	h.Upsert(KindGateway, "infra/public", model.Gateway{Namespace: "infra", Name: "public"})
	h.Upsert(KindGateway, "infra/internal", model.Gateway{Namespace: "infra", Name: "internal"})
	h.Upsert(KindPersistentVolume, "pv-b", model.PersistentVolume{Name: "pv-b"})
	h.Upsert(KindPersistentVolume, "pv-a", model.PersistentVolume{Name: "pv-a"})
	init, sub := h.Subscribe(0)
	defer sub.Close()
	m := init[0]
	if len(m.Gateways) != 2 || m.Gateways[0].Name != "internal" || len(m.PersistentVolumes) != 2 || m.PersistentVolumes[0].Name != "pv-a" {
		t.Fatalf("snapshot = %+v", m)
	}
}
```

Dans `internal/stream/ws_test.go`, compléter le `switch` de `nsOnly.Allow` :

```go
	case model.Gateway:
		return x.Namespace == string(o)
	case model.PersistentVolume:
		return false // cluster-scoped : jamais visible avec un droit de namespace
```

et ajouter :

```go
func TestViewFiltersGatewaysAndPersistentVolumes(t *testing.T) {
	v := newView(nsOnly("prod"))
	out := v.apply(context.Background(), []Message{{Type: "snapshot",
		Gateways:          []model.Gateway{{Namespace: "prod", Name: "public"}, {Namespace: "kube-system", Name: "sys"}},
		PersistentVolumes: []model.PersistentVolume{{Name: "pv-1"}},
	}})
	s := out[0]
	if len(s.Gateways) != 1 || s.Gateways[0].Name != "public" || len(s.PersistentVolumes) != 0 {
		t.Fatalf("snapshot filtré = %+v", s)
	}
	d := v.apply(context.Background(), []Message{{Type: "upsert", Kind: KindPersistentVolume, Obj: model.PersistentVolume{Name: "pv-2"}}})
	if len(d) != 0 {
		t.Errorf("PV transmis sans droit : %+v", d)
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/stream/ -run 'TestSnapshotCarriesGatewaysAndPersistentVolumes|TestViewFiltersGatewaysAndPersistentVolumes'`
Expected: FAIL à la compilation (`undefined: KindGateway`, `unknown field Gateways`).

- [ ] **Step 3 : hub**

Dans `internal/stream/hub.go`, compléter les constantes :

```go
	KindVolume    Kind = "volume"
	KindGateway   Kind = "gateway"
	// KindPersistentVolume : PV sans PVC existant (jalon 9).
	KindPersistentVolume Kind = "persistentVolume"
```

Ajouter à `Message`, après `Volumes` :

```go
	Gateways          []model.Gateway          `json:"gateways,omitempty"`
	PersistentVolumes []model.PersistentVolume `json:"persistentVolumes,omitempty"`
```

Dans `snapshotLocked`, l'initialisation devient :

```go
	m := Message{Type: "snapshot", Rev: h.rev,
		Nodes: []model.Node{}, Pods: []model.Pod{}, Workloads: []model.Workload{}, Namespaces: []model.Namespace{},
		Services: []model.Service{}, Routes: []model.Route{}, Volumes: []model.Volume{},
		Gateways: []model.Gateway{}, PersistentVolumes: []model.PersistentVolume{}}
```

ajouter au `switch` :

```go
		case model.Gateway:
			m.Gateways = append(m.Gateways, o)
		case model.PersistentVolume:
			m.PersistentVolumes = append(m.PersistentVolumes, o)
```

et aux tris, après celui des volumes :

```go
	sort.Slice(m.Gateways, func(i, j int) bool { return model.GatewayKey(m.Gateways[i]) < model.GatewayKey(m.Gateways[j]) })
	sort.Slice(m.PersistentVolumes, func(i, j int) bool { return m.PersistentVolumes[i].Name < m.PersistentVolumes[j].Name })
```

- [ ] **Step 4 : filtre par client**

Dans `internal/stream/ws.go`, `(*view).snapshot`, l'initialisation devient :

```go
	s := Message{Type: "snapshot", Rev: m.Rev,
		Nodes: []model.Node{}, Pods: []model.Pod{}, Workloads: []model.Workload{}, Namespaces: []model.Namespace{},
		Services: []model.Service{}, Routes: []model.Route{}, Volumes: []model.Volume{},
		Gateways: []model.Gateway{}, PersistentVolumes: []model.PersistentVolume{}}
```

et, avant `return s` :

```go
	for _, o := range m.Gateways {
		if v.f.Allow(ctx, KindGateway, o) {
			s.Gateways = append(s.Gateways, o)
		}
	}
	for _, o := range m.PersistentVolumes {
		if v.f.Allow(ctx, KindPersistentVolume, o) {
			s.PersistentVolumes = append(s.PersistentVolumes, o)
		}
	}
```

Dans `delta`, le commentaire devient `// workloads, namespaces, objets réseau et stockage : visibles selon le droit courant`.

Le filtre d'accès (`internal/access`) ne connaît pas encore ces kinds : `attributes` renvoie `false`, ils ne sont donc envoyés qu'en `auth.mode=none` (`AllowAll`). Les droits arrivent en phase B.

- [ ] **Step 5 : vérifier**

Run: `go test ./internal/stream/`
Expected: PASS.

- [ ] **Step 6 : commit**

```bash
git add internal/stream
git commit -m "feat(stream): Gateways et PV orphelins dans le snapshot et les deltas

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3 : registre des types dynamiques, IngressRoute migrées

Le registre remplace `traefik.go` : un informer `unstructured` par ressource, démarrable et arrêtable seul. Dans cette tâche, les types servis sont trouvés par la découverte au démarrage ; le suivi des CRD arrive à la tâche 4.

**Files:**
- Create: `internal/kube/dynkinds.go`
- Delete: `internal/kube/traefik.go`
- Move: `internal/kube/traefik_test.go` → `internal/kube/dynkinds_test.go`
- Modify: `internal/kube/source.go`, `internal/kube/source_net.go`

- [ ] **Step 1 : tests qui échouent**

```bash
git mv internal/kube/traefik_test.go internal/kube/dynkinds_test.go
```

Remplacer tout le contenu de `internal/kube/dynkinds_test.go` par :

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

var irGVR = gvrIngressRoute("traefik.io")

// fakeDynamic : client dynamique factice qui sait lister tous les types du registre.
func fakeDynamic(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	kinds := map[schema.GroupVersionResource]string{}
	for _, k := range dynKinds {
		kinds[k.gvr] = k.gvr.Resource + "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, objs...)
}

// servedDyn : client dynamique factice d'un cluster qui sert les types donnés
// (déclarés à la découverte du client typé).
func servedDyn(client *fake.Clientset, served []dynKind, objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	byGV := map[string][]metav1.APIResource{}
	for _, k := range served {
		gv := k.gvr.GroupVersion().String()
		byGV[gv] = append(byGV[gv], metav1.APIResource{Name: k.gvr.Resource, Namespaced: true})
	}
	for gv, rs := range byGV {
		client.Resources = append(client.Resources, &metav1.APIResourceList{GroupVersion: gv, APIResources: rs})
	}
	return fakeDynamic(objs...)
}

// kindsOf : entrées du registre pour ces ressources, dans l'ordre donné.
func kindsOf(gvrs ...schema.GroupVersionResource) []dynKind {
	var out []dynKind
	for _, g := range gvrs {
		for _, k := range dynKinds {
			if k.gvr == g {
				out = append(out, k)
			}
		}
	}
	return out
}

func adminRoute(group string, services ...string) *unstructured.Unstructured {
	var svcs []any
	for _, s := range services {
		svcs = append(svcs, map[string]any{"name": s, "port": int64(80)})
	}
	return ingressRoute(group, "prod", "admin", []any{map[string]any{"match": "Host(`admin.example.com`)", "services": svcs}})
}

const adminID = "IngressRoute/prod/admin"

func TestIngressRoutesFromTraefik(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api", "ghost"))

	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	o, ok := sk.get(stream.KindRoute, adminID)
	if !ok {
		t.Fatal("IngressRoute absente")
	}
	r := o.(model.Route)
	if r.Gate != "traefik" || len(r.Gates) != 1 || r.Group != "traefik.io" || r.Rules[0].Host != "admin.example.com" ||
		r.Rules[0].Backend.State != model.BackendOK || r.Rules[1].Backend.State != model.BackendMissing {
		t.Fatalf("route = %+v", r)
	}

	if err := dyn.Resource(irGVR).Namespace("prod").Delete(context.Background(), "admin", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute supprimée", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return !ok })
}

func TestNoTraefikWithoutCRD(t *testing.T) {
	_, sk := startSourceWith(t, fake.NewClientset(netFixtures()...), Options{Dynamic: fakeDynamic()})
	if _, ok := sk.get(stream.KindRoute, "Ingress/prod/storefront"); !ok {
		t.Error("la source doit fonctionner sans CRD Traefik")
	}
}

func TestTraefikIOWinsOverContainous(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	old := gvrIngressRoute("traefik.containo.us")
	dyn := servedDyn(client, kindsOf(irGVR, old), adminRoute("traefik.containo.us", "api"), adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if o, ok := sk.get(stream.KindRoute, adminID); !ok || o.(model.Route).Group != "traefik.io" {
		t.Fatalf("route = %v %+v", ok, o)
	}
	// Sans traefik.io, l'ancien groupe prend le relais.
	lastSource.stopDyn(irGVR)
	eventually(t, "repli sur traefik.containo.us", func() bool {
		o, ok := sk.get(stream.KindRoute, adminID)
		return ok && o.(model.Route).Group == "traefik.containo.us"
	})
}

func TestStoppedKindLeavesTheStream(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, adminID); !ok {
		t.Fatal("IngressRoute absente")
	}
	lastSource.stopDyn(irGVR)
	eventually(t, "IngressRoute retirée", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return !ok })
	if lastSource.dynIndexer(irGVR) != nil {
		t.Error("type toujours inscrit")
	}
	if !lastSource.startDyn(context.Background(), kindsOf(irGVR)[0]) {
		t.Fatal("redémarrage refusé")
	}
	eventually(t, "IngressRoute revenue", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return ok })
}

func TestServiceDeletedTurnsIngressRouteBackendMissing(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if err := client.CoreV1().Services("prod").Delete(context.Background(), "api", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backend manquant", func() bool {
		o, _ := sk.get(stream.KindRoute, adminID)
		return o.(model.Route).Rules[0].Backend.State == model.BackendMissing
	})
}
```

Ajouter `"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"` aux imports.

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'Traefik|StoppedKind|IngressRouteBackend'`
Expected: FAIL à la compilation (`undefined: gvrIngressRoute`, `undefined: dynKinds`).

- [ ] **Step 3 : registre**

Supprimer `internal/kube/traefik.go` (`git rm internal/kube/traefik.go`) et créer `internal/kube/dynkinds.go` :

```go
package kube

import (
	"context"
	"log/slog"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

// Types optionnels apportés par une CRD (Traefik, Gateway API) : un informer
// dynamique par ressource, lu en unstructured, démarré quand la CRD est servie
// et arrêté quand elle disparaît (crd.go).

// Groupes Traefik : v3 (traefik.io), puis l'ancien groupe v2. À nom égal, traefik.io l'emporte.
var traefikGroups = []string{"traefik.io", "traefik.containo.us"}

func gvrIngressRoute(g string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "ingressroutes"}
}

// dynKind : type optionnel apporté par une CRD. on et indexers ne doivent pas
// référencer dynKinds (cycle d'initialisation) : passer par s.dynRunning().
type dynKind struct {
	gvr      schema.GroupVersionResource
	on       func(s *Source, o any) // marque l'objet (et ce qui en dépend)
	indexers func(s *Source) cache.Indexers
}

// crd : nom de la CRD qui apporte le type.
func (k dynKind) crd() string { return k.gvr.Resource + "." + k.gvr.Group }

// dynKinds : registre complet, dans l'ordre de démarrage. Ajouter un type,
// c'est ajouter une entrée.
var dynKinds = []dynKind{
	ingressRouteKind("traefik.io"),
	ingressRouteKind("traefik.containo.us"),
}

func ingressRouteKind(g string) dynKind {
	return dynKind{gvr: gvrIngressRoute(g), on: (*Source).onIngressRoute, indexers: func(*Source) cache.Indexers {
		return cache.Indexers{indexByBackend: func(o any) ([]string, error) {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return nil, nil
			}
			return routeBackends(ConvertIngressRoute(u, nil)), nil
		}}
	}}
}

type dynInformer struct {
	kind dynKind
	inf  cache.SharedIndexInformer
	stop chan struct{}
}

// dynSyncTimeout borne l'attente du premier list d'un type démarré à chaud.
const dynSyncTimeout = 30 * time.Second

// dynIndexer : cache du type, nil s'il n'est pas démarré.
func (s *Source) dynIndexer(gvr schema.GroupVersionResource) cache.Indexer {
	s.dynMu.RLock()
	defer s.dynMu.RUnlock()
	if d := s.dyn[gvr]; d != nil {
		return d.inf.GetIndexer()
	}
	return nil
}

// dynRunning : types démarrés, dans un ordre quelconque.
func (s *Source) dynRunning() []*dynInformer {
	s.dynMu.RLock()
	defer s.dynMu.RUnlock()
	out := make([]*dynInformer, 0, len(s.dyn))
	for _, d := range s.dyn {
		out = append(out, d)
	}
	return out
}

// startDyn démarre l'informer d'un type si la sonde l'autorise, attend sa
// synchronisation, l'inscrit puis marque tous ses objets. Sans effet s'il
// tourne déjà.
func (s *Source) startDyn(ctx context.Context, k dynKind) bool {
	dyn := s.opts.Dynamic
	if dyn == nil {
		return false
	}
	s.dynStart.Lock()
	defer s.dynStart.Unlock()
	if s.dynIndexer(k.gvr) != nil {
		return true
	}
	if !s.probe(ctx, k.crd(), func(ctx context.Context) error {
		_, err := dyn.Resource(k.gvr).List(ctx, probeOpts)
		return err
	}) {
		return false
	}
	inf := dynamicinformer.NewFilteredDynamicInformer(dyn, k.gvr, metav1.NamespaceAll, 0,
		cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, nil).Informer()
	_ = inf.SetTransform(transform)
	if k.indexers != nil {
		_ = inf.AddIndexers(k.indexers(s))
	}
	s.handle(inf, func(o any) { k.on(s, o) })
	stop := make(chan struct{})
	go inf.Run(stop)
	syncCtx, cancel := context.WithTimeout(ctx, dynSyncTimeout)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), inf.HasSynced) {
		close(stop)
		s.opts.Log.Warn("type dynamique non synchronisé", "type", k.crd())
		return false
	}
	s.dynMu.Lock()
	s.dyn[k.gvr] = &dynInformer{kind: k, inf: inf, stop: stop}
	s.dynMu.Unlock()
	// Les événements reçus avant l'inscription ne trouvaient pas le type : on remarque tout.
	for _, o := range inf.GetStore().List() {
		k.on(s, o)
	}
	s.opts.Log.Info("type dynamique démarré", "type", k.crd())
	return true
}

// stopDyn arrête un type et retire ses objets du flux. Le type quitte d'abord
// le registre, puis chaque objet est marqué : la réconciliation, qui ne le
// trouve plus, publie leur suppression (et recalcule ce qui en dépendait).
func (s *Source) stopDyn(gvr schema.GroupVersionResource) {
	s.dynStart.Lock()
	defer s.dynStart.Unlock()
	s.dynMu.Lock()
	d := s.dyn[gvr]
	delete(s.dyn, gvr)
	s.dynMu.Unlock()
	if d == nil {
		return
	}
	close(d.stop)
	for _, o := range d.inf.GetStore().List() {
		d.kind.on(s, o)
	}
	s.opts.Log.Info("type dynamique arrêté", "type", d.kind.crd())
}

// stopAllDyn : arrêt de la source, sans rien publier.
func (s *Source) stopAllDyn() {
	s.dynStart.Lock()
	defer s.dynStart.Unlock()
	s.dynMu.Lock()
	defer s.dynMu.Unlock()
	for gvr, d := range s.dyn {
		close(d.stop)
		delete(s.dyn, gvr)
	}
}

// markDynamic marque tous les objets des types démarrés.
func (s *Source) markDynamic() {
	for _, d := range s.dynRunning() {
		for _, o := range d.inf.GetStore().List() {
			d.kind.on(s, o)
		}
	}
}

// servedKinds : types du registre que l'API server sert, d'après la découverte.
// Repli quand les CRD ne peuvent pas être suivies : un type installé ensuite
// attend le prochain redémarrage.
func servedKinds(d discovery.DiscoveryInterface, log *slog.Logger) []dynKind {
	served := map[string]map[string]bool{} // groupe/version → ressources servies
	var out []dynKind
	for _, k := range dynKinds {
		gv := k.gvr.GroupVersion().String()
		res, ok := served[gv]
		if !ok {
			res = map[string]bool{}
			rl, err := d.ServerResourcesForGroupVersion(gv)
			if err != nil && !apierrors.IsNotFound(err) {
				log.Warn("découverte impossible", "groupVersion", gv, "err", err)
			}
			if rl != nil {
				for _, r := range rl.APIResources {
					res[r.Name] = true
				}
			}
			served[gv] = res
		}
		if res[k.gvr.Resource] {
			out = append(out, k)
		}
	}
	return out
}

// startDynamic démarre les types servis au démarrage (remplacé par le suivi des CRD, crd.go).
func (s *Source) startDynamic(ctx context.Context) {
	if s.opts.Dynamic == nil {
		return
	}
	for _, k := range servedKinds(s.client.Discovery(), s.opts.Log) {
		s.startDyn(ctx, k)
	}
}
```

- [ ] **Step 4 : la source utilise le registre**

Dans `internal/kube/source.go` :

- imports : retirer `"k8s.io/client-go/dynamic/dynamicinformer"`, ajouter `"k8s.io/apimachinery/pkg/runtime/schema"` ;
- commentaire de `Options.Dynamic` : `// Dynamic lit les types apportés par une CRD (Traefik, Gateway API) ; nil : aucun.` ;
- dans `Source`, remplacer

```go
	traefik    []traefikInformer
	dynFactory dynamicinformer.DynamicSharedInformerFactory
```

par

```go

	// Jalon 9 : types apportés par une CRD, démarrés et arrêtés à chaud (dynkinds.go).
	dynStart sync.Mutex // sérialise startDyn et stopDyn
	dynMu    sync.RWMutex
	dyn      map[schema.GroupVersionResource]*dynInformer
```

- dans `NewSource`, la construction devient :

```go
	s := &Source{opts: opts, sink: sink, factory: f, client: client, dirty: map[ref]struct{}{}, last: map[ref]any{},
		dyn: map[schema.GroupVersionResource]*dynInformer{}}
```

- `Run` devient :

```go
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
	if err := s.markAll(); err != nil {
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
			s.factory.Shutdown()
			s.stopAllDyn()
			return nil
		case <-t.C:
			s.reconcile()
		}
	}
}
```

- `watch` est scindé :

```go
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
```

Dans `internal/kube/source_net.go` :

- `startNetwork` : supprimer la dernière ligne `s.startTraefik(ctx)` (les types dynamiques démarrent dans `Run`, après la synchronisation) ;
- `markRoutesTo` devient :

```go
func (s *Source) markRoutesTo(svc string) {
	if s.ingressIdx != nil {
		objs, _ := s.ingressIdx.ByIndex(indexByBackend, svc)
		for _, o := range objs {
			s.onIngress(o)
		}
	}
	for _, d := range s.dynRunning() {
		objs, err := d.inf.GetIndexer().ByIndex(indexByBackend, svc)
		if err != nil {
			continue // type sans index par Service
		}
		for _, o := range objs {
			d.kind.on(s, o)
		}
	}
}
```

- dans `markNetwork`, remplacer la boucle `for _, t := range s.traefik { … }` par `s.markDynamic()` ;
- dans `buildRoute`, le cas `"IngressRoute"` devient :

```go
	case model.SourceIngressRoute:
		// traefik.io avant traefik.containo.us (ordre de traefikGroups).
		for _, g := range traefikGroups {
			idx := s.dynIndexer(gvrIngressRoute(g))
			if idx == nil {
				continue
			}
			o, ok, err := idx.GetByKey(ns + "/" + name)
			if err != nil {
				return nil, "", err
			}
			if ok {
				return ConvertIngressRoute(o.(*unstructured.Unstructured), s.serviceExists()), id, nil
			}
		}
		return nil, "", nil
```

et le cas `"Ingress"` devient `case model.SourceIngress:`. Ajouter l'import `"github.com/no-inspi/atlas-k8s/internal/model"` s'il manque.

- [ ] **Step 5 : vérifier**

Run: `go test ./internal/kube/`
Expected: PASS (dont `TestTraefikIOWinsOverContainous`, `TestStoppedKindLeavesTheStream`, `TestServiceDeletedTurnsIngressRouteBackendMissing`).

Run: `go vet ./...`
Expected: aucune sortie.

- [ ] **Step 6 : commit**

```bash
git add internal/kube
git commit -m "refactor(kube): registre des types dynamiques, IngressRoute par informer arrêtable

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4 : CRD suivies à chaud

**Files:**
- Create: `internal/kube/crd.go`
- Modify: `internal/kube/dynkinds.go` (retrait de `startDynamic`)
- Test: `internal/kube/crd_test.go`, `internal/kube/dynkinds_test.go`

- [ ] **Step 1 : tests qui échouent**

Dans `internal/kube/dynkinds_test.go`, `fakeDynamic` sait aussi lister les CRD, et `servedDyn` installe leur CRD :

```go
// fakeDynamic : client dynamique factice qui sait lister les CRD et tous les types du registre.
func fakeDynamic(objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	kinds := map[schema.GroupVersionResource]string{gvrCRD: "CustomResourceDefinitionList"}
	for _, k := range dynKinds {
		kinds[k.gvr] = k.gvr.Resource + "List"
	}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), kinds, objs...)
}

// servedDyn : client dynamique factice d'un cluster qui sert les types donnés :
// CRD installées, et déclarées à la découverte (repli sans droit sur les CRD).
func servedDyn(client *fake.Clientset, served []dynKind, objs ...runtime.Object) *dynamicfake.FakeDynamicClient {
	byGV := map[string][]metav1.APIResource{}
	for _, k := range served {
		gv := k.gvr.GroupVersion().String()
		byGV[gv] = append(byGV[gv], metav1.APIResource{Name: k.gvr.Resource, Namespaced: true})
		objs = append(objs, crdObject(k, true))
	}
	for gv, rs := range byGV {
		client.Resources = append(client.Resources, &metav1.APIResourceList{GroupVersion: gv, APIResources: rs})
	}
	return fakeDynamic(objs...)
}

// crdObject : CRD factice du type ; served=false : installée, version attendue non servie.
func crdObject(k dynKind, served bool) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1", "kind": "CustomResourceDefinition",
		"metadata": map[string]any{"name": k.crd()},
		"spec": map[string]any{"group": k.gvr.Group, "versions": []any{
			map[string]any{"name": k.gvr.Version, "served": served, "storage": true}}},
	}}
}
```

`servedDyn` ne doit jamais recevoir deux fois le même type (la CRD serait créée deux fois).

Créer `internal/kube/crd_test.go` :

```go
package kube

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/atlas-k8s/internal/stream"
)

func TestKindStartsWhenItsCRDIsInstalled(t *testing.T) {
	dyn := fakeDynamic(adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, fake.NewClientset(netFixtures()...), Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, adminID); ok {
		t.Fatal("IngressRoute publiée sans CRD")
	}
	crd := crdObject(kindsOf(irGVR)[0], true)
	if _, err := dyn.Resource(gvrCRD).Create(context.Background(), crd, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute publiée après l'installation de la CRD", func() bool {
		_, ok := sk.get(stream.KindRoute, adminID)
		return ok
	})
}

func TestKindStopsWhenItsCRDIsRemoved(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	k := kindsOf(irGVR)[0]
	dyn := servedDyn(client, []dynKind{k}, adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, adminID); !ok {
		t.Fatal("IngressRoute absente")
	}
	if err := dyn.Resource(gvrCRD).Delete(context.Background(), k.crd(), metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute retirée avec sa CRD", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return !ok })
}

func TestKindStopsWhenItsVersionIsNoLongerServed(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	k := kindsOf(irGVR)[0]
	dyn := servedDyn(client, []dynKind{k}, adminRoute("traefik.io", "api"))
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if _, err := dyn.Resource(gvrCRD).Update(context.Background(), crdObject(k, false), metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "IngressRoute retirée", func() bool { _, ok := sk.get(stream.KindRoute, adminID); return !ok })
}

func TestForbiddenCRDsFallBackToDiscovery(t *testing.T) {
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(irGVR), adminRoute("traefik.io", "api"))
	dyn.PrependReactor("list", "customresourcedefinitions", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apiextensions.k8s.io", Resource: "customresourcedefinitions"}, "", errors.New("refusé"))
	})
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	if _, ok := sk.get(stream.KindRoute, adminID); !ok {
		t.Fatal("la découverte au démarrage doit servir de repli")
	}
}

func TestSlimCRDKeepsOnlyServedVersions(t *testing.T) {
	crd := crdObject(kindsOf(irGVR)[0], true)
	crd.Object["spec"].(map[string]any)["versions"].([]any)[0].(map[string]any)["schema"] = map[string]any{"openAPIV3Schema": map[string]any{"type": "object"}}
	o, _ := slimCRD(crd)
	u := o.(*unstructured.Unstructured)
	if u.GetName() != "ingressroutes.traefik.io" || !crdServes(u, "v1alpha1") || crdServes(u, "v1") {
		t.Fatalf("crd = %+v", u.Object)
	}
	if _, found, _ := unstructured.NestedFieldNoCopy(u.Object, "spec", "group"); found {
		t.Error("le schéma et le reste de la spec doivent être retirés")
	}
}
```

Ajouter `"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"` aux imports de `crd_test.go`.

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'CRD|NoLongerServed'`
Expected: FAIL à la compilation (`undefined: gvrCRD`, `undefined: slimCRD`).

- [ ] **Step 3 : suivi des CRD**

Dans `internal/kube/dynkinds.go`, supprimer la fonction `startDynamic` (elle passe dans `crd.go`). Créer `internal/kube/crd.go` :

```go
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
// ne la sert plus. Sans droit de lister les CRD, repli sur la découverte au
// démarrage.

var gvrCRD = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}

// startDynamic démarre le suivi des CRD et attend que les types déjà servis
// aient démarré.
func (s *Source) startDynamic(ctx context.Context) {
	dyn := s.opts.Dynamic
	if dyn == nil {
		return
	}
	if !s.probe(ctx, "customresourcedefinitions", func(ctx context.Context) error {
		_, err := dyn.Resource(gvrCRD).List(ctx, probeOpts)
		return err
	}) {
		s.opts.Log.Warn("CRD non suivies : types dynamiques découverts au démarrage seulement")
		for _, k := range servedKinds(s.client.Discovery(), s.opts.Log) {
			s.startDyn(ctx, k)
		}
		return
	}
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
	// reg.HasSynced : le handler a traité les CRD déjà installées, donc les types servis ont démarré.
	cache.WaitForCacheSync(ctx.Done(), reg.HasSynced)
}

// syncCRD démarre ou arrête les types qu'apporte une CRD selon qu'elle sert leur version.
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
			s.startDyn(ctx, k)
		} else {
			s.stopDyn(k.gvr)
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
			s.stopDyn(k.gvr)
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
```

`syncCRD` et `dropCRD` lisent `dynKinds` : ils ne doivent jamais être appelés depuis un `on` du registre (cycle d'initialisation).

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/kube/`
Expected: PASS (les tests de la tâche 3 passent désormais par les CRD installées par `servedDyn`, `TestForbiddenCRDsFallBackToDiscovery` par la découverte).

- [ ] **Step 5 : commit**

```bash
git add internal/kube
git commit -m "feat(kube): CRD suivies à chaud, types dynamiques démarrés et arrêtés sans redémarrage

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5 : vérification de la phase A

Le registre introduit de la concurrence (handlers des CRD, réconciliation, arrêt) : on la passe au détecteur de courses.

**Files:** aucun.

- [ ] **Step 1 : courses**

Run: `go test -race -count=3 ./internal/kube/ ./internal/stream/`
Expected: PASS, aucun `WARNING: DATA RACE`. Une course sur `s.dyn` signale un accès hors de `dynMu` ; une course sur `s.last` signale un appel à la réconciliation hors de la boucle de `Run` (interdit, décision 5).

- [ ] **Step 2 : suite complète et démo**

Run: `go vet ./... && go test ./...`
Expected: PASS.

Run: `make demo` puis ouvrir http://localhost:8080
Expected: la ville du jalon 8 inchangée (portes `nginx` et `traefik`, relais, citernes) ; dans l'onglet réseau du navigateur, chaque route du snapshot porte `gates`.

- [ ] **Step 3 : commit éventuel**

Si une correction a été nécessaire :

```bash
git add internal
git commit -m "fix(kube): accès concurrents au registre des types dynamiques

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

## Phase B — Gateway API, Traefik complet, PersistentVolumes

La phase A a livré le modèle commun (`model.Gateway`, `model.PersistentVolume`, sources, `Gates`, `Parents`, champs pondérés de `Backend`), les kinds `gateway` et `persistentVolume` du flux, et le registre `dynKinds` avec ses outils (`dynIndexer`, `dynRunning`, `startDyn`, `stopDyn`, `servedDyn`, `kindsOf`, `fakeDynamic` dans les tests). Cette phase ajoute les conversions pures, les entrées du registre, les PV, les droits, l'inspecteur et la démo.

### Task 6 : conversion des Gateways, HTTPRoute et GRPCRoute

Conversions pures depuis `unstructured` (aucune dépendance `sigs.k8s.io/gateway-api`). L'état des backends vient du statut écrit par le contrôleur (`status.parents[]`), jamais d'un calcul des ReferenceGrant.

**Files:**
- Create: `internal/kube/gateway_convert.go`
- Test: `internal/kube/gateway_convert_test.go`

- [ ] **Step 1 : tests qui échouent**

Créer `internal/kube/gateway_convert_test.go` :

```go
package kube

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// unstr : objet unstructured de test (status omis si nil).
func unstr(apiVersion, kind, ns, name string, spec, status map[string]any) *unstructured.Unstructured {
	o := map[string]any{"apiVersion": apiVersion, "kind": kind,
		"metadata": map[string]any{"name": name, "namespace": ns}, "spec": spec}
	if status != nil {
		o["status"] = status
	}
	return &unstructured.Unstructured{Object: o}
}

func condOf(typ, status, reason string) map[string]any {
	return map[string]any{"type": typ, "status": status, "reason": reason, "message": reason + " (test)"}
}

const gwAPI = gatewayGroup + "/v1"

// w : poids publié d'un backend, -1 s'il n'en a pas.
func w(b model.Backend) int {
	if b.Weight == nil {
		return -1
	}
	return *b.Weight
}

func TestConvertGateway(t *testing.T) {
	u := unstr(gwAPI, "Gateway", "infra", "public",
		map[string]any{"gatewayClassName": "eg", "listeners": []any{
			map[string]any{"name": "http", "protocol": "HTTP", "port": int64(80), "hostname": "*.example.com"},
			map[string]any{"name": "https", "protocol": "HTTPS", "port": int64(443)},
		}},
		map[string]any{
			"conditions": []any{condOf("Accepted", "True", "Accepted"), condOf("Programmed", "True", "Programmed")},
			"addresses":  []any{map[string]any{"type": "IPAddress", "value": "34.1.2.3"}},
			"listeners": []any{map[string]any{"name": "http", "attachedRoutes": int64(2),
				"conditions": []any{condOf("Programmed", "True", "Programmed")}}},
		})
	g := ConvertGateway(u)
	if g.Namespace != "infra" || g.Name != "public" || g.Class != "eg" || g.Accepted != model.CondTrue || g.Programmed != model.CondTrue ||
		g.Reason != "Programmed" || len(g.Addresses) != 1 || g.Addresses[0] != "34.1.2.3" {
		t.Fatalf("gateway = %+v", g)
	}
	want := []model.Listener{
		{Name: "http", Protocol: "HTTP", Port: 80, Hostname: "*.example.com", AttachedRoutes: 2, Ready: model.CondTrue},
		{Name: "https", Protocol: "HTTPS", Port: 443, Ready: model.CondUnknown},
	}
	if len(g.Listeners) != 2 || g.Listeners[0] != want[0] || g.Listeners[1] != want[1] {
		t.Errorf("listeners = %+v", g.Listeners)
	}
}

func TestConvertGatewayStatus(t *testing.T) {
	bare := ConvertGateway(unstr(gwAPI, "Gateway", "infra", "new", map[string]any{"gatewayClassName": "eg"}, nil))
	if bare.Accepted != model.CondUnknown || bare.Programmed != model.CondUnknown || bare.Listeners == nil || bare.Reason != "" {
		t.Errorf("sans contrôleur = %+v", bare)
	}
	down := ConvertGateway(unstr(gwAPI, "Gateway", "infra", "internal", map[string]any{"gatewayClassName": "eg"},
		map[string]any{"conditions": []any{condOf("Accepted", "True", "Accepted"), condOf("Programmed", "False", "AddressNotAssigned")}}))
	if down.Programmed != model.CondFalse || down.Reason != "AddressNotAssigned" || down.Message != "AddressNotAssigned (test)" {
		t.Errorf("non programmé = %+v", down)
	}
	// Sans condition Programmed, la raison vient d'Accepted.
	refused := ConvertGateway(unstr(gwAPI, "Gateway", "infra", "bad", map[string]any{"gatewayClassName": "eg"},
		map[string]any{"conditions": []any{condOf("Accepted", "False", "InvalidParameters")}}))
	if refused.Accepted != model.CondFalse || refused.Programmed != model.CondUnknown || refused.Reason != "InvalidParameters" {
		t.Errorf("refusé = %+v", refused)
	}
}

func parentStatus(ns, name string, conds ...any) map[string]any {
	return map[string]any{"parentRef": map[string]any{"name": name, "namespace": ns}, "conditions": conds}
}

func TestConvertHTTPRoute(t *testing.T) {
	u := unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{
		"parentRefs": []any{
			map[string]any{"name": "public", "namespace": "infra"},
			map[string]any{"name": "local"},
			map[string]any{"name": "mesh", "kind": "Service", "group": ""}, // maillage (GAMMA) : pas une porte
		},
		"hostnames": []any{"shop.example.com", "www.example.com"},
		"rules": []any{
			map[string]any{
				"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/api"}}},
				"backendRefs": []any{
					map[string]any{"name": "api", "port": int64(80), "weight": int64(9)},
					map[string]any{"name": "canary", "port": int64(80), "weight": int64(1)},
				}},
			map[string]any{"backendRefs": []any{map[string]any{"name": "web", "port": int64(8080)}}},
		},
	}, map[string]any{"parents": []any{
		parentStatus("infra", "public", condOf("Accepted", "True", "Accepted"), condOf("ResolvedRefs", "True", "ResolvedRefs")),
	}})
	exists := func(ns, name string) bool { return name != "canary" }
	r := ConvertGatewayRoute(u, model.SourceHTTPRoute, exists)
	if r.Source != model.SourceHTTPRoute || r.Group != gatewayGroup || r.Gate != "infra/public" ||
		len(r.Gates) != 2 || r.Gates[1] != "prod/local" || len(r.Rules) != 3 {
		t.Fatalf("route = %+v", r)
	}
	api, canary, web := r.Rules[0], r.Rules[1], r.Rules[2]
	if api.Host != "shop.example.com" || api.Path != "/api" || w(api.Backend) != 900 || api.Backend.Port != "80" ||
		api.Backend.Namespace != "prod" || api.Backend.Kind != "Service" || api.Backend.State != model.BackendOK {
		t.Errorf("api = %+v", api)
	}
	if w(canary.Backend) != 100 || canary.Backend.State != model.BackendMissing {
		t.Errorf("canary = %+v", canary)
	}
	if web.Backend.Weight != nil || web.Path != "" || web.Backend.State != model.BackendOK {
		t.Errorf("une règle à un seul backend n'a pas de poids : %+v", web)
	}
	wantParent := model.RouteParent{Gateway: "infra/public", Accepted: model.CondTrue, ResolvedRefs: model.CondTrue}
	if len(r.Parents) != 1 || r.Parents[0] != wantParent {
		t.Errorf("parents = %+v", r.Parents)
	}
	if got := routeBackends(r); len(got) != 3 || got[0] != "prod/api" {
		t.Errorf("routeBackends = %v", got)
	}
}

func TestConvertHTTPRouteZeroWeight(t *testing.T) {
	// Un backend de poids 0 (canary coupé) reste publié, avec un poids 0 explicite.
	u := unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{
		"parentRefs": []any{map[string]any{"name": "public", "namespace": "infra"}},
		"rules": []any{map[string]any{"backendRefs": []any{
			map[string]any{"name": "api", "port": int64(80), "weight": int64(1)},
			map[string]any{"name": "canary", "port": int64(80), "weight": int64(0)},
		}}},
	}, nil)
	r := ConvertGatewayRoute(u, model.SourceHTTPRoute, nil)
	if len(r.Rules) != 2 || w(r.Rules[0].Backend) != 1000 || r.Rules[1].Backend.Weight == nil || *r.Rules[1].Backend.Weight != 0 {
		t.Errorf("règles = %+v", r.Rules)
	}
}

func TestConvertHTTPRouteRefused(t *testing.T) {
	spec := func(parents ...any) map[string]any {
		return map[string]any{"parentRefs": parents,
			"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api", "port": int64(80)}}}}}
	}
	refused := []any{parentStatus("infra", "public", condOf("Accepted", "False", "NotAllowedByListeners"), condOf("ResolvedRefs", "True", "ResolvedRefs"))}
	r := ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "legacy",
		spec(map[string]any{"name": "public", "namespace": "infra"}), map[string]any{"parents": refused}), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendRefused || r.Parents[0].Reason != "NotAllowedByListeners" || r.Parents[0].Accepted != model.CondFalse {
		t.Errorf("route refusée = %+v", r)
	}
	// Refusée par un seul de ses deux Gateways : pas refusée.
	r = ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "legacy",
		spec(map[string]any{"name": "public", "namespace": "infra"}, map[string]any{"name": "local"}), map[string]any{"parents": refused}), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("refus partiel = %+v", r.Rules[0])
	}
}

func TestConvertHTTPRouteUnresolvedRefs(t *testing.T) {
	status := map[string]any{"parents": []any{parentStatus("infra", "public",
		condOf("Accepted", "True", "Accepted"), condOf("ResolvedRefs", "False", "RefNotPermitted"))}}
	parents := []any{map[string]any{"name": "public", "namespace": "infra"}}
	// Un backend d'un autre namespace (ReferenceGrant absente) : lui seul est manquant.
	r := ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{"parentRefs": parents,
		"rules": []any{map[string]any{"backendRefs": []any{
			map[string]any{"name": "api", "port": int64(80)},
			map[string]any{"name": "auth", "namespace": "sso", "port": int64(80)},
		}}}}, status), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendOK || r.Rules[1].Backend.State != model.BackendMissing || r.Parents[0].Reason != "RefNotPermitted" {
		t.Errorf("route = %+v", r)
	}
	// Aucun coupable identifiable : tous les backends sont manquants.
	r = ConvertGatewayRoute(unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{"parentRefs": parents,
		"rules": []any{map[string]any{"backendRefs": []any{map[string]any{"name": "api", "port": int64(80)}}}}}, status), model.SourceHTTPRoute, nil)
	if r.Rules[0].Backend.State != model.BackendMissing {
		t.Errorf("route = %+v", r)
	}
}

func TestConvertHTTPRouteWithoutGateway(t *testing.T) {
	u := unstr(gwAPI, "HTTPRoute", "prod", "mc", map[string]any{
		"rules": []any{map[string]any{"backendRefs": []any{
			map[string]any{"name": "api", "kind": "ServiceImport", "group": "multicluster.x-k8s.io", "port": int64(80)},
		}}},
	}, nil)
	r := ConvertGatewayRoute(u, model.SourceHTTPRoute, func(string, string) bool { return true })
	if r.Gate != model.NoGateway || len(r.Gates) != 1 || r.Gates[0] != model.NoGateway || len(r.Parents) != 0 {
		t.Errorf("portes = %q %v", r.Gate, r.Gates)
	}
	if b := r.Rules[0].Backend; b.Kind != "ServiceImport.multicluster.x-k8s.io" || b.State != model.BackendMissing {
		t.Errorf("backend non-Service = %+v", b)
	}
	if got := routeBackends(r); len(got) != 0 {
		t.Errorf("un ServiceImport n'est pas un Service : %v", got)
	}
}

func TestConvertGRPCRoute(t *testing.T) {
	u := unstr(gwAPI, "GRPCRoute", "prod", "orders", map[string]any{
		"parentRefs": []any{map[string]any{"name": "public", "namespace": "infra"}},
		"hostnames":  []any{"grpc.example.com"},
		"rules": []any{
			map[string]any{"matches": []any{map[string]any{"method": map[string]any{"service": "orders.v1.Orders", "method": "Create"}}},
				"backendRefs": []any{map[string]any{"name": "orders", "port": int64(9090)}}},
			map[string]any{"matches": []any{map[string]any{"method": map[string]any{"service": "orders.v1.Admin"}}},
				"backendRefs": []any{map[string]any{"name": "admin", "port": int64(9090)}}},
		},
	}, nil)
	r := ConvertGatewayRoute(u, model.SourceGRPCRoute, nil)
	if r.Source != model.SourceGRPCRoute || len(r.Rules) != 2 || r.Rules[0].Host != "grpc.example.com" ||
		r.Rules[0].Match != "orders.v1.Orders/Create" || r.Rules[0].Path != "" || r.Rules[1].Match != "orders.v1.Admin" {
		t.Errorf("route = %+v", r)
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'TestConvertGateway|TestConvertHTTPRoute|TestConvertGRPCRoute'`
Expected: FAIL à la compilation (`undefined: ConvertGateway`, `undefined: gatewayGroup`).

- [ ] **Step 3 : conversions**

Créer `internal/kube/gateway_convert.go` :

```go
package kube

import (
	"fmt"
	"math"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// Gateway API (v1), lue en unstructured : Gateways, HTTPRoute et GRPCRoute.

const gatewayGroup = "gateway.networking.k8s.io"

// condition : état tri-valué d'une condition de statut, avec sa raison et son
// message ; unknown si elle est absente (aucun contrôleur ne l'a écrite).
func condition(conds []any, typ string) (status, reason, message string) {
	for _, c := range conds {
		m, ok := c.(map[string]any)
		if !ok || m["type"] != typ {
			continue
		}
		reason, _ = m["reason"].(string)
		message, _ = m["message"].(string)
		switch m["status"] {
		case "True":
			return model.CondTrue, reason, message
		case "False":
			return model.CondFalse, reason, message
		}
		return model.CondUnknown, reason, message
	}
	return model.CondUnknown, "", ""
}

// toInt lit un nombre JSON (int64 ou float64 selon le décodeur).
func toInt(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// permille : part w/total en pour mille, arrondie.
func permille(w, total float64) int {
	if total <= 0 {
		return 0
	}
	return int(math.Round(1000 * w / total))
}

// ConvertGateway réduit un Gateway. Raison et message : ceux de Programmed,
// sinon d'Accepted.
func ConvertGateway(u *unstructured.Unstructured) model.Gateway {
	g := model.Gateway{Namespace: u.GetNamespace(), Name: u.GetName(), Listeners: []model.Listener{}}
	g.Class, _, _ = unstructured.NestedString(u.Object, "spec", "gatewayClassName")
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	var aReason, aMessage string
	g.Accepted, aReason, aMessage = condition(conds, "Accepted")
	g.Programmed, g.Reason, g.Message = condition(conds, "Programmed")
	if g.Reason == "" && g.Message == "" {
		g.Reason, g.Message = aReason, aMessage
	}
	addrs, _, _ := unstructured.NestedSlice(u.Object, "status", "addresses")
	for _, a := range addrs {
		if m, ok := a.(map[string]any); ok {
			if v, _ := m["value"].(string); v != "" {
				g.Addresses = append(g.Addresses, v)
			}
		}
	}
	byName := map[string]map[string]any{}
	sl, _, _ := unstructured.NestedSlice(u.Object, "status", "listeners")
	for _, l := range sl {
		if m, ok := l.(map[string]any); ok {
			if n, _ := m["name"].(string); n != "" {
				byName[n] = m
			}
		}
	}
	ls, _, _ := unstructured.NestedSlice(u.Object, "spec", "listeners")
	for _, l := range ls {
		m, ok := l.(map[string]any)
		if !ok {
			continue
		}
		li := model.Listener{Port: int32(toInt(m["port"])), Ready: model.CondUnknown}
		li.Name, _ = m["name"].(string)
		li.Protocol, _ = m["protocol"].(string)
		li.Hostname, _ = m["hostname"].(string)
		if st, ok := byName[li.Name]; ok {
			li.AttachedRoutes = int32(toInt(st["attachedRoutes"]))
			c, _ := st["conditions"].([]any)
			li.Ready, _, _ = condition(c, "Programmed")
		}
		g.Listeners = append(g.Listeners, li)
	}
	return g
}

// parentKey : « ns/name » d'une référence de parent si elle désigne un
// Gateway (kind et groupe par défaut), sinon "" (Service d'un maillage…).
func parentKey(ns string, ref any) string {
	m, ok := ref.(map[string]any)
	if !ok {
		return ""
	}
	if k, _ := m["kind"].(string); k != "" && k != "Gateway" {
		return ""
	}
	if g, ok := m["group"].(string); ok && g != gatewayGroup {
		return ""
	}
	name, _ := m["name"].(string)
	if name == "" {
		return ""
	}
	if n, _ := m["namespace"].(string); n != "" {
		ns = n
	}
	return ns + "/" + name
}

// parentGateways : Gateways visés par spec.parentRefs, dans l'ordre, sans doublon.
func parentGateways(u *unstructured.Unstructured) []string {
	refs, _, _ := unstructured.NestedSlice(u.Object, "spec", "parentRefs")
	var out []string
	seen := map[string]bool{}
	for _, r := range refs {
		if k := parentKey(u.GetNamespace(), r); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// firstMatch : premier chemin (HTTPRoute) ou première méthode « service/méthode » (GRPCRoute).
func firstMatch(rule map[string]any, source string) (path, match string) {
	ms, _ := rule["matches"].([]any)
	for _, x := range ms {
		m, ok := x.(map[string]any)
		if !ok {
			continue
		}
		if source == model.SourceGRPCRoute {
			meth, _ := m["method"].(map[string]any)
			svc, _ := meth["service"].(string)
			name, _ := meth["method"].(string)
			if svc != "" || name != "" {
				return "", strings.TrimSuffix(svc+"/"+name, "/")
			}
			continue
		}
		p, _ := m["path"].(map[string]any)
		if v, _ := p["value"].(string); v != "" {
			return v, ""
		}
	}
	return "", ""
}

// gatewayBackend : un backendRef. Seul un Service (groupe core) peut être résolu ;
// un autre kind est publié manquant, avec son kind qualifié par son groupe.
func gatewayBackend(routeNS string, m map[string]any, exists ServiceExists) model.Backend {
	b := model.Backend{Namespace: routeNS, Kind: "Service"}
	b.Service, _ = m["name"].(string)
	if ns, _ := m["namespace"].(string); ns != "" {
		b.Namespace = ns
	}
	if k, _ := m["kind"].(string); k != "" {
		b.Kind = k
	}
	if g, _ := m["group"].(string); g != "" && g != "core" {
		b.Kind += "." + g
	}
	if p, ok := m["port"]; ok && p != nil {
		b.Port = fmt.Sprint(toInt(p))
	}
	if b.Kind == "Service" {
		b.State = backendState(exists, b.Namespace, b.Service)
	} else {
		b.State = model.BackendMissing
	}
	return b
}

// ConvertGatewayRoute lit une HTTPRoute ou une GRPCRoute (source SourceHTTPRoute
// ou SourceGRPCRoute) : une règle par backendRef, pondérée en pour mille quand
// la règle source en a plusieurs. État : refused si tous ses Gateways la
// refusent (Accepted=False) ; si ResolvedRefs=False, les backends suspects
// (autre namespace, autre kind, introuvables) sont manquants, ou tous à défaut.
func ConvertGatewayRoute(u *unstructured.Unstructured, source string, exists ServiceExists) model.Route {
	r := model.Route{Source: source, Group: gatewayGroup, Namespace: u.GetNamespace(), Name: u.GetName(),
		Gates: parentGateways(u), Rules: []model.Rule{}}
	if len(r.Gates) == 0 {
		r.Gates = []string{model.NoGateway}
	}
	r.Gate = r.Gates[0]

	byGate := map[string]model.RouteParent{}
	unresolved := false
	ps, _, _ := unstructured.NestedSlice(u.Object, "status", "parents")
	for _, p := range ps {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		key := parentKey(r.Namespace, m["parentRef"])
		if key == "" {
			continue
		}
		conds, _ := m["conditions"].([]any)
		rp := model.RouteParent{Gateway: key}
		var ar, rr string
		rp.Accepted, ar, _ = condition(conds, "Accepted")
		rp.ResolvedRefs, rr, _ = condition(conds, "ResolvedRefs")
		switch {
		case rp.Accepted == model.CondFalse:
			rp.Reason = ar
		case rp.ResolvedRefs == model.CondFalse:
			rp.Reason = rr
		}
		r.Parents = append(r.Parents, rp)
		byGate[key] = rp
		unresolved = unresolved || rp.ResolvedRefs == model.CondFalse
	}
	refused := r.Gate != model.NoGateway
	for _, g := range r.Gates {
		if p, ok := byGate[g]; !ok || p.Accepted != model.CondFalse {
			refused = false
		}
	}

	host := ""
	if hs, _, _ := unstructured.NestedStringSlice(u.Object, "spec", "hostnames"); len(hs) > 0 {
		host = hs[0]
	}
	rules, _, _ := unstructured.NestedSlice(u.Object, "spec", "rules")
	for _, ro := range rules {
		rm, ok := ro.(map[string]any)
		if !ok {
			continue
		}
		path, match := firstMatch(rm, source)
		refs, _ := rm["backendRefs"].([]any)
		ws := make([]float64, len(refs))
		total := 0.0
		for i, x := range refs {
			ws[i] = 1
			if m, ok := x.(map[string]any); ok {
				if w, ok := m["weight"]; ok && w != nil {
					ws[i] = float64(toInt(w))
				}
			}
			total += ws[i]
		}
		for i, x := range refs {
			m, ok := x.(map[string]any)
			if !ok {
				continue
			}
			b := gatewayBackend(r.Namespace, m, exists)
			if len(refs) > 1 {
				b.Weight = model.Weight(permille(ws[i], total))
			}
			r.Rules = append(r.Rules, model.Rule{Host: host, Path: path, Match: match, Backend: b})
		}
	}

	switch {
	case refused:
		for i := range r.Rules {
			r.Rules[i].Backend.State = model.BackendRefused
		}
	case unresolved:
		marked := false
		for i := range r.Rules {
			b := &r.Rules[i].Backend
			if b.State != model.BackendOK || b.Kind != "Service" || b.Namespace != r.Namespace {
				b.State = model.BackendMissing
				marked = true
			}
		}
		if !marked {
			for i := range r.Rules {
				r.Rules[i].Backend.State = model.BackendMissing
			}
		}
	}
	return r
}
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/kube/ -run 'TestConvertGateway|TestConvertHTTPRoute|TestConvertGRPCRoute'`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/kube/gateway_convert.go internal/kube/gateway_convert_test.go
git commit -m "feat(kube): conversion des Gateways, HTTPRoute et GRPCRoute

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7 : routes Traefik TCP et UDP, résolution des TraefikService

`ConvertIngressRoute` devient un cas particulier de `ConvertTraefikRoute`, qui lit les trois types de routes et résout les TraefikService jusqu'aux Services. Sans résolveur (`lookup` nil), un TraefikService est `missing` : l'état `indirect` n'est plus produit.

**Files:**
- Modify: `internal/kube/network.go`
- Test: `internal/kube/network_test.go`, `internal/kube/traefik_convert_test.go`

- [ ] **Step 1 : tests qui échouent**

Dans `internal/kube/network_test.go`, `TestConvertIngressRoute`, remplacer :

```go
	if w := r.Rules[2].Backend; w.Kind != "TraefikService" || w.State != model.BackendIndirect {
```

par :

```go
	if w := r.Rules[2].Backend; w.Kind != "TraefikService" || w.State != model.BackendMissing {
```

Créer `internal/kube/traefik_convert_test.go` :

```go
package kube

import (
	"fmt"
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

func traefikService(ns, name string, spec map[string]any) *unstructured.Unstructured {
	return unstr("traefik.io/v1alpha1", "TraefikService", ns, name, spec, nil)
}

func lookupOf(ts ...*unstructured.Unstructured) TraefikLookup {
	return func(ns, name string) (*unstructured.Unstructured, bool) {
		for _, u := range ts {
			if u.GetNamespace() == ns && u.GetName() == name {
				return u, true
			}
		}
		return nil, false
	}
}

func toTS(name string) map[string]any { return map[string]any{"name": name, "kind": "TraefikService"} }

func checkoutRoute(services ...any) *unstructured.Unstructured {
	return ingressRoute("traefik.io", "prod", "checkout", []any{map[string]any{"match": "Host(`c.example.com`)", "services": services}})
}

func TestResolveTraefikService(t *testing.T) {
	split := traefikService("prod", "split", map[string]any{"weighted": map[string]any{"services": []any{
		map[string]any{"name": "api", "port": int64(80), "weight": int64(3)},
		map[string]any{"name": "shadow", "kind": "TraefikService", "weight": int64(1)},
	}}})
	shadow := traefikService("prod", "shadow", map[string]any{"mirroring": map[string]any{"name": "orders", "port": int64(8080),
		"mirrors": []any{map[string]any{"name": "audit", "namespace": "sec", "port": int64(9000), "percent": int64(10)}}}})
	exists := func(ns, name string) bool { return name != "orders" }
	r := ConvertTraefikRoute(checkoutRoute(toTS("split")), model.SourceIngressRoute, exists, lookupOf(split, shadow))
	want := []model.Backend{
		{Namespace: "prod", Service: "api", Port: "80", Kind: "Service", State: model.BackendOK, Weight: model.Weight(750), Via: "prod/split"},
		{Namespace: "prod", Service: "orders", Port: "8080", Kind: "Service", State: model.BackendMissing, Weight: model.Weight(250), Via: "prod/split"},
		{Namespace: "sec", Service: "audit", Port: "9000", Kind: "Service", State: model.BackendOK, Mirror: true, Percent: 10, Via: "prod/split"},
	}
	if len(r.Rules) != len(want) {
		t.Fatalf("règles = %+v", r.Rules)
	}
	for i, w := range want {
		if !reflect.DeepEqual(r.Rules[i].Backend, w) || r.Rules[i].Host != "c.example.com" {
			t.Errorf("règle %d = %+v, attendu %+v", i, r.Rules[i], w)
		}
	}
	if got := routeBackends(r); len(got) != 3 {
		t.Errorf("Services atteints = %v", got)
	}
}

func TestConvertTraefikRouteWeightedServices(t *testing.T) {
	u := checkoutRoute(map[string]any{"name": "a", "port": int64(80)}, map[string]any{"name": "b", "port": int64(80), "weight": int64(3)})
	r := ConvertTraefikRoute(u, model.SourceIngressRoute, nil, nil)
	if w(r.Rules[0].Backend) != 250 || w(r.Rules[1].Backend) != 750 || r.Rules[0].Backend.Via != "" {
		t.Errorf("règles = %+v", r.Rules)
	}
	single := ConvertTraefikRoute(checkoutRoute(map[string]any{"name": "a"}), model.SourceIngressRoute, nil, nil)
	if single.Rules[0].Backend.Weight != nil {
		t.Errorf("un seul backend : poids %d", *single.Rules[0].Backend.Weight)
	}
	// TraefikService weighted dont un service a un poids 0 : publié avec un poids 0 explicite.
	zero := traefikService("prod", "zero", map[string]any{"weighted": map[string]any{"services": []any{
		map[string]any{"name": "a", "weight": int64(1)}, map[string]any{"name": "b", "weight": int64(0)},
	}}})
	r = ConvertTraefikRoute(checkoutRoute(toTS("zero")), model.SourceIngressRoute, nil, lookupOf(zero))
	if len(r.Rules) != 2 || w(r.Rules[0].Backend) != 1000 || r.Rules[1].Backend.Weight == nil || *r.Rules[1].Backend.Weight != 0 || r.Rules[1].Backend.Via != "prod/zero" {
		t.Errorf("poids 0 = %+v", r.Rules)
	}
}

// chain : TraefikService ts0 → ts1 → … → ts(n-1) → Service api.
func chain(n int) []*unstructured.Unstructured {
	var out []*unstructured.Unstructured
	for i := range n {
		next := toTS(fmt.Sprintf("ts%d", i+1))
		if i == n-1 {
			next = map[string]any{"name": "api", "port": int64(80)}
		}
		out = append(out, traefikService("prod", fmt.Sprintf("ts%d", i), map[string]any{"weighted": map[string]any{"services": []any{next}}}))
	}
	return out
}

func TestResolveTraefikServiceFailures(t *testing.T) {
	missing := model.Backend{Namespace: "prod", Service: "ts0", Kind: "TraefikService", State: model.BackendMissing}
	cases := []struct {
		name   string
		lookup TraefikLookup
		want   model.Backend
	}{
		{"profondeur 8", lookupOf(chain(8)...), model.Backend{Namespace: "prod", Service: "api", Port: "80", Kind: "Service", State: model.BackendOK, Via: "prod/ts0"}},
		{"profondeur 9", lookupOf(chain(9)...), missing},
		{"introuvable", lookupOf(), missing},
		{"sans résolveur", nil, missing},
		{"cycle", lookupOf(
			traefikService("prod", "ts0", map[string]any{"weighted": map[string]any{"services": []any{toTS("ts1")}}}),
			traefikService("prod", "ts1", map[string]any{"weighted": map[string]any{"services": []any{toTS("ts0")}}}),
		), missing},
		{"ni weighted ni mirroring", lookupOf(traefikService("prod", "ts0", map[string]any{})), missing},
	}
	for _, c := range cases {
		r := ConvertTraefikRoute(checkoutRoute(toTS("ts0")), model.SourceIngressRoute, nil, c.lookup)
		if len(r.Rules) != 1 || !reflect.DeepEqual(r.Rules[0].Backend, c.want) {
			t.Errorf("%s : %+v", c.name, r.Rules)
		}
	}
}

func TestConvertTraefikRouteTCPAndUDP(t *testing.T) {
	tcp := func(match string) *unstructured.Unstructured {
		return unstr("traefik.io/v1alpha1", "IngressRouteTCP", "prod", "pg", map[string]any{"entryPoints": []any{"postgres"},
			"routes": []any{map[string]any{"match": match, "services": []any{map[string]any{"name": "pg", "port": int64(5432)}}}}}, nil)
	}
	r := ConvertTraefikRoute(tcp("HostSNI(`db.example.com`)"), model.SourceIngressRouteTCP, nil, nil)
	if r.Source != model.SourceIngressRouteTCP || r.Group != "traefik.io" || r.Gate != "traefik" || r.Gates[0] != "traefik" ||
		r.Rules[0].Host != "db.example.com" || r.Rules[0].Path != "" || r.Rules[0].Match != "HostSNI(`db.example.com`)" || r.Rules[0].Backend.Port != "5432" {
		t.Errorf("TCP = %+v", r)
	}
	if h := ConvertTraefikRoute(tcp("HostSNI(`*`)"), model.SourceIngressRouteTCP, nil, nil).Rules[0].Host; h != "" {
		t.Errorf("HostSNI(*) : hôte %q", h)
	}
	udp := unstr("traefik.io/v1alpha1", "IngressRouteUDP", "kube-system", "dns", map[string]any{"entryPoints": []any{"dns"},
		"routes": []any{map[string]any{"services": []any{map[string]any{"name": "kube-dns", "port": int64(53)}}}}}, nil)
	r = ConvertTraefikRoute(udp, model.SourceIngressRouteUDP, nil, nil)
	if r.Source != model.SourceIngressRouteUDP || len(r.Rules) != 1 || r.Rules[0].Match != "" || r.Rules[0].Host != "" ||
		r.Rules[0].Backend.Service != "kube-dns" || r.Rules[0].Backend.Namespace != "kube-system" {
		t.Errorf("UDP = %+v", r)
	}
}

func TestTraefikRefs(t *testing.T) {
	svcs, ts := traefikRefs(checkoutRoute(map[string]any{"name": "api"}, toTS("split"), map[string]any{"name": "api"},
		map[string]any{"name": "auth", "namespace": "sso"}))
	if len(svcs) != 2 || svcs[0] != "prod/api" || svcs[1] != "sso/auth" || len(ts) != 1 || ts[0] != "prod/split" {
		t.Errorf("route : services %v, TraefikService %v", svcs, ts)
	}
	svcs, ts = traefikRefs(traefikService("prod", "shadow", map[string]any{"mirroring": map[string]any{"name": "orders",
		"mirrors": []any{map[string]any{"name": "audit"}, map[string]any{"name": "deep", "kind": "TraefikService"}}}}))
	if len(svcs) != 2 || svcs[0] != "prod/orders" || svcs[1] != "prod/audit" || len(ts) != 1 || ts[0] != "prod/deep" {
		t.Errorf("mirroring : services %v, TraefikService %v", svcs, ts)
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'Traefik|TestConvertIngressRoute'`
Expected: FAIL à la compilation (`undefined: TraefikLookup`, `undefined: ConvertTraefikRoute`, `undefined: traefikRefs`).

- [ ] **Step 3 : implémentation**

Dans `internal/kube/network.go`, ajouter `"math"` aux imports, ajouter `sniRe` au bloc `var ( hostRe … )` :

```go
	sniRe  = regexp.MustCompile("HostSNI\\([`\"]([^`\"]+)[`\"]")
```

puis remplacer toute la fonction `ConvertIngressRoute` (commentaire compris) par :

```go
// TraefikLookup retrouve un TraefikService (traefik.io d'abord). nil : aucun n'est lisible.
type TraefikLookup func(namespace, name string) (*unstructured.Unstructured, bool)

// traefikMaxDepth borne la résolution des TraefikService imbriqués.
const traefikMaxDepth = 8

// ConvertIngressRoute : IngressRoute HTTP, sans résolution des TraefikService.
func ConvertIngressRoute(u *unstructured.Unstructured, exists ServiceExists) model.Route {
	return ConvertTraefikRoute(u, model.SourceIngressRoute, exists, nil)
}

// traefikRef lit une référence de service Traefik (routes[].services[],
// weighted.services[], mirroring et ses mirrors[]).
func traefikRef(ns string, m map[string]any) (refNS, name, kind, port string) {
	refNS = ns
	name, _ = m["name"].(string)
	if n, _ := m["namespace"].(string); n != "" {
		refNS = n
	}
	kind, _ = m["kind"].(string)
	if kind == "" {
		kind = "Service"
	}
	if p, ok := m["port"]; ok && p != nil {
		port = fmt.Sprint(p)
	}
	return refNS, name, kind, port
}

// weightOf : poids Traefik d'une référence, 1 par défaut.
func weightOf(m map[string]any) float64 {
	if w, ok := m["weight"]; ok && w != nil {
		return float64(toInt(w))
	}
	return 1
}

// leaf : backend final d'une règle Traefik, avec sa part du trafic (0 à 1).
type leaf struct {
	b     model.Backend
	share float64
}

// resolveTraefik résout le TraefikService ns/name jusqu'à ses Services. weighted :
// chaque enfant reçoit sa part normalisée ; mirroring : le service principal
// garde toute la part, chaque miroir devient une feuille mirror avec son
// percent. ok=false : introuvable, cycle, plus de traefikMaxDepth niveaux, ou
// ni weighted ni mirroring.
func resolveTraefik(lookup TraefikLookup, ns, name string) ([]leaf, bool) {
	var out []leaf
	path := map[string]bool{}
	var walk func(ns, name string, share float64, depth int, mirror bool, percent int) bool
	walk = func(ns, name string, share float64, depth int, mirror bool, percent int) bool {
		key := ns + "/" + name
		if lookup == nil || depth > traefikMaxDepth || path[key] {
			return false
		}
		u, ok := lookup(ns, name)
		if !ok {
			return false
		}
		path[key] = true
		defer delete(path, key)
		visit := func(m map[string]any, share float64, mirror bool, percent int) bool {
			rns, rname, kind, port := traefikRef(ns, m)
			if kind == "TraefikService" {
				return walk(rns, rname, share, depth+1, mirror, percent)
			}
			out = append(out, leaf{b: model.Backend{Namespace: rns, Service: rname, Port: port, Kind: kind, Mirror: mirror, Percent: percent}, share: share})
			return true
		}
		if ws, found, _ := unstructured.NestedSlice(u.Object, "spec", "weighted", "services"); found {
			total := 0.0
			for _, x := range ws {
				if m, ok := x.(map[string]any); ok {
					total += weightOf(m)
				}
			}
			for _, x := range ws {
				m, ok := x.(map[string]any)
				if !ok {
					continue
				}
				s := 0.0
				if total > 0 {
					s = share * weightOf(m) / total
				}
				if !visit(m, s, mirror, percent) {
					return false
				}
			}
			return true
		}
		if main, found, _ := unstructured.NestedMap(u.Object, "spec", "mirroring"); found {
			if !visit(main, share, mirror, percent) {
				return false
			}
			mirrors, _ := main["mirrors"].([]any)
			for _, x := range mirrors {
				m, ok := x.(map[string]any)
				if !ok {
					continue
				}
				if !visit(m, 0, true, int(toInt(m["percent"]))) {
					return false
				}
			}
			return true
		}
		return false
	}
	if !walk(ns, name, 1, 1, false, 0) {
		return nil, false
	}
	return out, true
}

// ConvertTraefikRoute lit une IngressRoute, IngressRouteTCP ou IngressRouteUDP
// (traefik.io ou traefik.containo.us). Chaque service de chaque routes[] donne
// une règle ; un TraefikService est remplacé par ses Services (via : son nom),
// ou par une seule règle missing s'il ne se résout pas. Les poids, en pour
// mille de la route, ne sont publiés que s'il y a plusieurs backends non miroirs.
func ConvertTraefikRoute(u *unstructured.Unstructured, source string, exists ServiceExists, lookup TraefikLookup) model.Route {
	gate := u.GetAnnotations()[ingressClassAnnotation]
	if gate == "" {
		gate = traefikGate
	}
	r := model.Route{Source: source, Group: u.GroupVersionKind().Group, Namespace: u.GetNamespace(), Name: u.GetName(),
		Gate: gate, Gates: []string{gate}, Rules: []model.Rule{}}
	routes, _, _ := unstructured.NestedSlice(u.Object, "spec", "routes")
	for _, ro := range routes {
		rm, ok := ro.(map[string]any)
		if !ok {
			continue
		}
		match, _ := rm["match"].(string)
		host, path := parseMatch(match)
		if source != model.SourceIngressRoute {
			host, path = "", ""
			if m := sniRe.FindStringSubmatch(match); m != nil && m[1] != "*" {
				host = m[1]
			}
		}
		services, _ := rm["services"].([]any)
		total := 0.0
		for _, so := range services {
			if sm, ok := so.(map[string]any); ok {
				total += weightOf(sm)
			}
		}
		var leaves []leaf
		for _, so := range services {
			sm, ok := so.(map[string]any)
			if !ok {
				continue
			}
			share := 0.0
			if total > 0 {
				share = weightOf(sm) / total
			}
			ns, name, kind, port := traefikRef(r.Namespace, sm)
			if kind != "TraefikService" {
				leaves = append(leaves, leaf{b: model.Backend{Namespace: ns, Service: name, Port: port, Kind: kind}, share: share})
				continue
			}
			sub, ok := resolveTraefik(lookup, ns, name)
			if !ok {
				leaves = append(leaves, leaf{b: model.Backend{Namespace: ns, Service: name, Kind: kind, State: model.BackendMissing}, share: share})
				continue
			}
			for _, l := range sub {
				l.b.Via = ns + "/" + name
				l.share *= share
				leaves = append(leaves, l)
			}
		}
		weighted := 0
		for _, l := range leaves {
			if !l.b.Mirror {
				weighted++
			}
		}
		for _, l := range leaves {
			b := l.b
			if b.State == "" {
				b.State = backendState(exists, b.Namespace, b.Service)
			}
			if weighted > 1 && !b.Mirror {
				b.Weight = model.Weight(int(math.Round(l.share * 1000)))
			}
			r.Rules = append(r.Rules, model.Rule{Host: host, Path: path, Match: match, Backend: b})
		}
	}
	return r
}

// traefikRefs : Services et TraefikService visés directement par une route
// Traefik ou par un TraefikService (index de la source), « ns/name », sans doublon.
func traefikRefs(u *unstructured.Unstructured) (services, tservices []string) {
	ns := u.GetNamespace()
	seen := map[string]bool{}
	add := func(x any) {
		m, ok := x.(map[string]any)
		if !ok {
			return
		}
		rns, name, kind, _ := traefikRef(ns, m)
		k := kind + ":" + rns + "/" + name
		if name == "" || seen[k] {
			return
		}
		seen[k] = true
		if kind == "TraefikService" {
			tservices = append(tservices, rns+"/"+name)
		} else {
			services = append(services, rns+"/"+name)
		}
	}
	routes, _, _ := unstructured.NestedSlice(u.Object, "spec", "routes")
	for _, ro := range routes {
		rm, _ := ro.(map[string]any)
		svcs, _ := rm["services"].([]any)
		for _, s := range svcs {
			add(s)
		}
	}
	ws, _, _ := unstructured.NestedSlice(u.Object, "spec", "weighted", "services")
	for _, s := range ws {
		add(s)
	}
	if main, found, _ := unstructured.NestedMap(u.Object, "spec", "mirroring"); found {
		add(main)
		mirrors, _ := main["mirrors"].([]any)
		for _, s := range mirrors {
			add(s)
		}
	}
	return services, tservices
}
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/kube/ -run 'Traefik|TestConvertIngressRoute|TestParseMatch'`
Expected: PASS.

Run: `go test ./internal/kube/`
Expected: PASS (les tests du registre de la phase A utilisent toujours `ConvertIngressRoute`).

- [ ] **Step 5 : commit**

```bash
git add internal/kube/network.go internal/kube/network_test.go internal/kube/traefik_convert_test.go
git commit -m "feat(kube): routes Traefik TCP et UDP, TraefikService résolus jusqu'aux Services

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8 : conversion des PersistentVolumes

Un PV n'est publié que s'il n'est lié à aucun PVC existant. Sans lecture des PVC, seuls `Available`, `Released` et `Failed` le sont.

**Files:**
- Modify: `internal/kube/network.go`
- Test: `internal/kube/network_test.go`

- [ ] **Step 1 : test qui échoue**

Ajouter à `internal/kube/network_test.go` :

```go
func pvObj(name string, phase corev1.PersistentVolumePhase, claim string) *corev1.PersistentVolume {
	p := &corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{StorageClassName: "standard-rwo", PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimRetain,
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity:    corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}},
		Status: corev1.PersistentVolumeStatus{Phase: phase}}
	if ns, n, ok := strings.Cut(claim, "/"); ok {
		p.Spec.ClaimRef = &corev1.ObjectReference{Kind: "PersistentVolumeClaim", Namespace: ns, Name: n}
	}
	return p
}

func TestConvertPV(t *testing.T) {
	m := ConvertPV(pvObj("pv-1", corev1.VolumeReleased, "prod/old"))
	want := model.PersistentVolume{Name: "pv-1", StorageClass: "standard-rwo", Capacity: 10 << 30, AccessModes: []string{"ReadWriteOnce"},
		ReclaimPolicy: "Retain", Phase: "Released", ClaimRef: "prod/old"}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("pv = %+v", m)
	}
	if empty := ConvertPV(&corev1.PersistentVolume{ObjectMeta: metav1.ObjectMeta{Name: "x"}}); empty.AccessModes == nil || empty.Phase != "Pending" {
		t.Errorf("PV neuf = %+v", empty)
	}
}

func TestPublishPV(t *testing.T) {
	exists := func(ns, name string) bool { return ns == "prod" && name == "data" }
	cases := []struct {
		pv     *corev1.PersistentVolume
		exists ClaimExists
		want   bool
	}{
		{pvObj("a", corev1.VolumeAvailable, ""), exists, true},
		{pvObj("r", corev1.VolumeReleased, "prod/old"), exists, true},
		{pvObj("f", corev1.VolumeFailed, "prod/data"), exists, true},
		{pvObj("b", corev1.VolumeBound, "prod/data"), exists, false},
		{pvObj("d", corev1.VolumeBound, "prod/gone"), exists, true},
		{pvObj("d", corev1.VolumeBound, "prod/gone"), nil, false}, // PVC non listables : on ne sait pas
		{pvObj("p", corev1.VolumePending, ""), exists, false},
	}
	for _, c := range cases {
		if got := PublishPV(c.pv, c.exists); got != c.want {
			t.Errorf("%s (%s, exists %v) : %v", c.pv.Name, c.pv.Status.Phase, c.exists != nil, got)
		}
	}
}
```

Ajouter `"reflect"` et `"strings"` aux imports du fichier.

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'TestConvertPV|TestPublishPV'`
Expected: FAIL à la compilation (`undefined: ConvertPV`, `undefined: ClaimExists`).

- [ ] **Step 3 : implémentation**

Ajouter à la fin de `internal/kube/network.go` :

```go
// ConvertPV réduit un PersistentVolume ; ClaimRef : « ns/name » du PVC qui l'a réclamé.
func ConvertPV(pv *corev1.PersistentVolume) model.PersistentVolume {
	m := model.PersistentVolume{Name: pv.Name, StorageClass: pv.Spec.StorageClassName, Phase: string(pv.Status.Phase),
		ReclaimPolicy: string(pv.Spec.PersistentVolumeReclaimPolicy), AccessModes: []string{}}
	if m.Phase == "" {
		m.Phase = string(corev1.VolumePending)
	}
	if q, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
		m.Capacity = q.Value()
	}
	for _, a := range pv.Spec.AccessModes {
		m.AccessModes = append(m.AccessModes, string(a))
	}
	if c := pv.Spec.ClaimRef; c != nil && c.Name != "" {
		m.ClaimRef = c.Namespace + "/" + c.Name
	}
	return m
}

// ClaimExists : « ce PVC existe-t-il ? ». nil quand les PVC ne sont pas listables.
type ClaimExists func(namespace, name string) bool

// PublishPV : un PV n'est publié que s'il n'est lié à aucun PVC existant
// (Available, Released, Failed, ou Bound à un PVC disparu). Sans lecture des
// PVC, un PV Bound n'est jamais publié.
func PublishPV(pv *corev1.PersistentVolume, exists ClaimExists) bool {
	switch pv.Status.Phase {
	case corev1.VolumeAvailable, corev1.VolumeReleased, corev1.VolumeFailed:
		return true
	case corev1.VolumeBound:
		c := pv.Spec.ClaimRef
		return exists != nil && c != nil && !exists(c.Namespace, c.Name)
	}
	return false
}
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/kube/ -run 'TestConvertPV|TestPublishPV'`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/kube/network.go internal/kube/network_test.go
git commit -m "feat(kube): conversion des PersistentVolumes et règle de publication

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9 : Gateway API et Traefik complet dans la source

Nouvelles entrées du registre : TraefikService, IngressRouteTCP et IngressRouteUDP (deux groupes), Gateway, HTTPRoute, GRPCRoute. Les TraefikService et les Gateways passent avant les routes dans `dynKinds`, pour qu'au démarrage les routes trouvent ce qu'elles visent dès leur premier calcul. `markRoutesTo` (phase A) parcourt déjà l'index `indexByBackend` de chaque type démarré : un TraefikService indexé par les Services qu'il vise y répond en marquant les routes qui l'atteignent. GatewayClass n'a pas d'informer : l'inspecteur lit son YAML au nom de l'utilisateur (Task 12).

**Files:**
- Create: `internal/kube/gateway.go`, `internal/kube/traefik.go`
- Modify: `internal/kube/dynkinds.go`, `internal/kube/source_net.go`, `internal/kube/source.go`
- Test: `internal/kube/source_gw_test.go`

- [ ] **Step 1 : tests qui échouent**

Créer `internal/kube/source_gw_test.go` :

```go
package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// gwObjects : un Gateway programmé, une HTTPRoute 9:1 vers api et canary
// (absent), un TraefikService 3:1 vers les mêmes, l'IngressRoute checkout qui
// le vise et une IngressRouteTCP.
func gwObjects() []runtime.Object {
	return []runtime.Object{
		unstr(gwAPI, "Gateway", "infra", "public", map[string]any{"gatewayClassName": "eg",
			"listeners": []any{map[string]any{"name": "http", "protocol": "HTTP", "port": int64(80)}}},
			map[string]any{"conditions": []any{condOf("Programmed", "True", "Programmed")}}),
		unstr(gwAPI, "HTTPRoute", "prod", "shop", map[string]any{
			"parentRefs": []any{map[string]any{"name": "public", "namespace": "infra"}},
			"rules": []any{map[string]any{"backendRefs": []any{
				map[string]any{"name": "api", "port": int64(80), "weight": int64(9)},
				map[string]any{"name": "canary", "port": int64(80), "weight": int64(1)},
			}}}}, nil),
		traefikService("prod", "split", map[string]any{"weighted": map[string]any{"services": []any{
			map[string]any{"name": "api", "port": int64(80), "weight": int64(3)},
			map[string]any{"name": "canary", "port": int64(80), "weight": int64(1)},
		}}}),
		checkoutRoute(toTS("split")),
		unstr("traefik.io/v1alpha1", "IngressRouteTCP", "prod", "pg", map[string]any{"routes": []any{
			map[string]any{"match": "HostSNI(`*`)", "services": []any{map[string]any{"name": "api", "port": int64(5432)}}}}}, nil),
	}
}

var gwGVRs = []schema.GroupVersionResource{gvrTraefikService("traefik.io"), gvrGateway, gvrHTTPRoute, irGVR, gvrIngressRouteTCP("traefik.io")}

func routeOf(t *testing.T, sk *sink, id string) model.Route {
	t.Helper()
	o, ok := sk.get(stream.KindRoute, id)
	if !ok {
		t.Fatalf("route %s absente", id)
	}
	return o.(model.Route)
}

func startGateways(t *testing.T) (*fake.Clientset, *sink, context.Context) {
	t.Helper()
	client := fake.NewClientset(netFixtures()...)
	dyn := servedDyn(client, kindsOf(gwGVRs...), gwObjects()...)
	_, sk := startSourceWith(t, client, Options{Dynamic: dyn})
	eventually(t, "objets dynamiques publiés", func() bool {
		_, g := sk.get(stream.KindGateway, "infra/public")
		_, r := sk.get(stream.KindRoute, "IngressRoute/prod/checkout")
		_, tcp := sk.get(stream.KindRoute, "IngressRouteTCP/prod/pg")
		return g && r && tcp
	})
	return client, sk, context.Background()
}

func TestGatewayAPIAndTraefikInSource(t *testing.T) {
	client, sk, ctx := startGateways(t)
	dyn := lastSource.opts.Dynamic

	g, _ := sk.get(stream.KindGateway, "infra/public")
	if g.(model.Gateway).Programmed != model.CondTrue || g.(model.Gateway).Class != "eg" {
		t.Errorf("gateway = %+v", g)
	}
	shop := routeOf(t, sk, "HTTPRoute/prod/shop")
	if shop.Gate != "infra/public" || w(shop.Rules[0].Backend) != 900 || shop.Rules[0].Backend.State != model.BackendOK ||
		w(shop.Rules[1].Backend) != 100 || shop.Rules[1].Backend.State != model.BackendMissing {
		t.Errorf("HTTPRoute = %+v", shop)
	}
	co := routeOf(t, sk, "IngressRoute/prod/checkout")
	if len(co.Rules) != 2 || co.Rules[0].Backend.Service != "api" || w(co.Rules[0].Backend) != 750 || co.Rules[0].Backend.Via != "prod/split" ||
		w(co.Rules[1].Backend) != 250 || co.Rules[1].Backend.State != model.BackendMissing {
		t.Errorf("IngressRoute via TraefikService = %+v", co.Rules)
	}
	if pg := routeOf(t, sk, "IngressRouteTCP/prod/pg"); pg.Gate != "traefik" || pg.Rules[0].Host != "" || pg.Rules[0].Backend.Port != "5432" {
		t.Errorf("IngressRouteTCP = %+v", pg)
	}

	// Le Service canary apparaît : la HTTPRoute et l'IngressRoute (par le TraefikService) le trouvent.
	if _, err := client.CoreV1().Services("prod").Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "canary", Namespace: "prod"},
		Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "canary"}}}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "canary résolu", func() bool {
		return routeOf(t, sk, "HTTPRoute/prod/shop").Rules[1].Backend.State == model.BackendOK &&
			routeOf(t, sk, "IngressRoute/prod/checkout").Rules[1].Backend.State == model.BackendOK
	})

	// Le TraefikService passe à 1:1 : l'IngressRoute suit.
	tsGVR := gvrTraefikService("traefik.io")
	ts, err := dyn.Resource(tsGVR).Namespace("prod").Get(ctx, "split", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(ts.Object, []any{map[string]any{"name": "api", "port": int64(80)},
		map[string]any{"name": "canary", "port": int64(80)}}, "spec", "weighted", "services"); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(tsGVR).Namespace("prod").Update(ctx, ts, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "poids 500/500", func() bool {
		r := routeOf(t, sk, "IngressRoute/prod/checkout")
		return w(r.Rules[0].Backend) == 500 && w(r.Rules[1].Backend) == 500
	})

	// Le Gateway disparaît : retiré du flux ; la route garde sa porte.
	if err := dyn.Resource(gvrGateway).Namespace("infra").Delete(ctx, "public", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Gateway retiré", func() bool { _, ok := sk.get(stream.KindGateway, "infra/public"); return !ok })
	if r := routeOf(t, sk, "HTTPRoute/prod/shop"); r.Gate != "infra/public" {
		t.Errorf("porte de la route = %q", r.Gate)
	}
}

func TestTraefikServiceKindStoppedTurnsRouteMissing(t *testing.T) {
	_, sk, _ := startGateways(t)
	lastSource.stopDyn(gvrTraefikService("traefik.io"))
	eventually(t, "TraefikService manquant", func() bool {
		r := routeOf(t, sk, "IngressRoute/prod/checkout")
		return len(r.Rules) == 1 && r.Rules[0].Backend.Kind == "TraefikService" && r.Rules[0].Backend.State == model.BackendMissing
	})
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'TestGatewayAPIAndTraefikInSource|TestTraefikServiceKindStopped'`
Expected: FAIL à la compilation (`undefined: gvrTraefikService`, `undefined: gvrGateway`).

- [ ] **Step 3 : entrées Gateway API**

Créer `internal/kube/gateway.go` :

```go
package kube

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

// Gateway API dans la source : Gateways (portes), HTTPRoute et GRPCRoute, par le registre dynamique.

var (
	gvrGateway   = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "gateways"}
	gvrHTTPRoute = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "httproutes"}
	gvrGRPCRoute = schema.GroupVersionResource{Group: gatewayGroup, Version: "v1", Resource: "grpcroutes"}
)

const indexByGateway = "gateway" // routes par Gateway parent : « ns/name »

var gatewayRouteSources = []string{model.SourceHTTPRoute, model.SourceGRPCRoute}

func gatewayRouteGVR(source string) schema.GroupVersionResource {
	if source == model.SourceGRPCRoute {
		return gvrGRPCRoute
	}
	return gvrHTTPRoute
}

func gatewayKind() dynKind { return dynKind{gvr: gvrGateway, on: (*Source).onGateway} }

func gatewayRouteKind(source string) dynKind {
	return dynKind{gvr: gatewayRouteGVR(source), on: markRoute(source), indexers: func(*Source) cache.Indexers {
		return cache.Indexers{
			indexByBackend: func(o any) ([]string, error) {
				u, ok := o.(*unstructured.Unstructured)
				if !ok {
					return nil, nil
				}
				return routeBackends(ConvertGatewayRoute(u, source, nil)), nil
			},
			indexByGateway: func(o any) ([]string, error) {
				u, ok := o.(*unstructured.Unstructured)
				if !ok {
					return nil, nil
				}
				return parentGateways(u), nil
			},
		}
	}}
}

// markRoute : marqueur des routes d'une source, clé « Source/ns/name ».
func markRoute(source string) func(*Source, any) {
	return func(s *Source, o any) {
		if u, ok := o.(*unstructured.Unstructured); ok {
			s.mark(ref{stream.KindRoute, source + "/" + u.GetNamespace() + "/" + u.GetName()})
		}
	}
}

// onGateway : le Gateway, et les routes qui s'y rattachent.
func (s *Source) onGateway(o any) {
	u, ok := o.(*unstructured.Unstructured)
	if !ok {
		return
	}
	id := u.GetNamespace() + "/" + u.GetName()
	s.mark(ref{stream.KindGateway, id})
	for _, source := range gatewayRouteSources {
		s.markIndexed(gatewayRouteGVR(source), indexByGateway, id, markRoute(source))
	}
}

// markIndexed applique on aux objets d'un type démarré qui ont cette clé d'index.
func (s *Source) markIndexed(gvr schema.GroupVersionResource, index, key string, on func(*Source, any)) {
	idx := s.dynIndexer(gvr)
	if idx == nil {
		return
	}
	objs, err := idx.ByIndex(index, key)
	if err != nil {
		return // type sans cet index
	}
	for _, o := range objs {
		on(s, o)
	}
}

// dynGet : objet « ns/name » d'un type du registre ; nil si le type n'est pas
// démarré ou si l'objet n'existe pas.
func (s *Source) dynGet(gvr schema.GroupVersionResource, id string) (*unstructured.Unstructured, error) {
	idx := s.dynIndexer(gvr)
	if idx == nil {
		return nil, nil
	}
	o, ok, err := idx.GetByKey(id)
	if err != nil || !ok {
		return nil, err
	}
	u, _ := o.(*unstructured.Unstructured)
	return u, nil
}

func (s *Source) buildGateway(id string) (any, string, error) {
	u, err := s.dynGet(gvrGateway, id)
	if err != nil || u == nil {
		return nil, "", err
	}
	return ConvertGateway(u), id, nil
}
```

- [ ] **Step 4 : entrées Traefik**

Créer `internal/kube/traefik.go` :

```go
package kube

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"

	"github.com/no-inspi/atlas-k8s/internal/model"
)

// Traefik dans la source : IngressRoute (dynkinds.go), IngressRouteTCP et
// IngressRouteUDP, TraefikService résolus jusqu'aux Services.

func gvrIngressRouteTCP(g string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "ingressroutetcps"}
}

func gvrIngressRouteUDP(g string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "ingressrouteudps"}
}

func gvrTraefikService(g string) schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: g, Version: "v1alpha1", Resource: "traefikservices"}
}

const indexByTraefikService = "traefikservice" // routes et TraefikService par TraefikService visé : « ns/name »

var traefikRouteSources = []string{model.SourceIngressRoute, model.SourceIngressRouteTCP, model.SourceIngressRouteUDP}

func traefikRouteGVR(g, source string) schema.GroupVersionResource {
	switch source {
	case model.SourceIngressRouteTCP:
		return gvrIngressRouteTCP(g)
	case model.SourceIngressRouteUDP:
		return gvrIngressRouteUDP(g)
	}
	return gvrIngressRoute(g)
}

// traefikIndexers : Services et TraefikService visés directement, pour les
// routes Traefik comme pour les TraefikService.
func traefikIndexers(*Source) cache.Indexers {
	return cache.Indexers{
		indexByBackend: func(o any) ([]string, error) {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return nil, nil
			}
			svcs, _ := traefikRefs(u)
			return svcs, nil
		},
		indexByTraefikService: func(o any) ([]string, error) {
			u, ok := o.(*unstructured.Unstructured)
			if !ok {
				return nil, nil
			}
			_, ts := traefikRefs(u)
			return ts, nil
		},
	}
}

func traefikRouteKind(g, source string) dynKind {
	return dynKind{gvr: traefikRouteGVR(g, source), on: markRoute(source), indexers: traefikIndexers}
}

func traefikServiceKind(g string) dynKind {
	return dynKind{gvr: gvrTraefikService(g), on: (*Source).onTraefikService, indexers: traefikIndexers}
}

// onTraefikService : routes qui atteignent ce TraefikService (sa résolution a changé).
func (s *Source) onTraefikService(o any) {
	if u, ok := o.(*unstructured.Unstructured); ok {
		s.markTraefikUsers(u.GetNamespace()+"/"+u.GetName(), 1)
	}
}

// markTraefikUsers marque les routes qui visent le TraefikService key,
// directement ou par d'autres TraefikService, sur traefikMaxDepth niveaux au
// plus (ce qui borne aussi les cycles).
func (s *Source) markTraefikUsers(key string, depth int) {
	if depth > traefikMaxDepth {
		return
	}
	for _, g := range traefikGroups {
		for _, source := range traefikRouteSources {
			s.markIndexed(traefikRouteGVR(g, source), indexByTraefikService, key, markRoute(source))
		}
		s.markIndexed(gvrTraefikService(g), indexByTraefikService, key, func(s *Source, o any) {
			u := o.(*unstructured.Unstructured)
			s.markTraefikUsers(u.GetNamespace()+"/"+u.GetName(), depth+1)
		})
	}
}

// traefikLookup : TraefikService du cache, traefik.io d'abord.
func (s *Source) traefikLookup() TraefikLookup {
	return func(ns, name string) (*unstructured.Unstructured, bool) {
		for _, g := range traefikGroups {
			if u, _ := s.dynGet(gvrTraefikService(g), ns+"/"+name); u != nil {
				return u, true
			}
		}
		return nil, false
	}
}
```

- [ ] **Step 5 : registre et construction**

Dans `internal/kube/dynkinds.go`, ajouter l'import `"github.com/no-inspi/atlas-k8s/internal/model"`, puis remplacer la déclaration de `dynKinds` et la fonction `ingressRouteKind` par :

```go
// dynKinds : registre complet, dans l'ordre de démarrage. TraefikService et
// Gateways passent avant les routes : au premier calcul, les routes trouvent ce
// qu'elles visent. Ajouter un type, c'est ajouter une entrée.
var dynKinds = []dynKind{
	traefikServiceKind("traefik.io"),
	traefikServiceKind("traefik.containo.us"),
	ingressRouteKind("traefik.io"),
	ingressRouteKind("traefik.containo.us"),
	traefikRouteKind("traefik.io", model.SourceIngressRouteTCP),
	traefikRouteKind("traefik.containo.us", model.SourceIngressRouteTCP),
	traefikRouteKind("traefik.io", model.SourceIngressRouteUDP),
	traefikRouteKind("traefik.containo.us", model.SourceIngressRouteUDP),
	gatewayKind(),
	gatewayRouteKind(model.SourceHTTPRoute),
	gatewayRouteKind(model.SourceGRPCRoute),
}

func ingressRouteKind(g string) dynKind {
	return dynKind{gvr: gvrIngressRoute(g), on: (*Source).onIngressRoute, indexers: traefikIndexers}
}
```

Retirer de `dynkinds.go` l'import `"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"` s'il n'y sert plus.

Dans `internal/kube/source_net.go`, `buildRoute`, remplacer le cas `case model.SourceIngressRoute:` (jusqu'à son `return nil, "", nil`) par :

```go
	case model.SourceIngressRoute, model.SourceIngressRouteTCP, model.SourceIngressRouteUDP:
		// traefik.io avant traefik.containo.us (ordre de traefikGroups).
		for _, g := range traefikGroups {
			u, err := s.dynGet(traefikRouteGVR(g, source), ns+"/"+name)
			if err != nil {
				return nil, "", err
			}
			if u != nil {
				return ConvertTraefikRoute(u, source, s.serviceExists(), s.traefikLookup()), id, nil
			}
		}
		return nil, "", nil
	case model.SourceHTTPRoute, model.SourceGRPCRoute:
		u, err := s.dynGet(gatewayRouteGVR(source), ns+"/"+name)
		if err != nil || u == nil {
			return nil, "", err
		}
		return ConvertGatewayRoute(u, source, s.serviceExists()), id, nil
```

Dans `internal/kube/source.go`, `build`, ajouter avant `case stream.KindService:` :

```go
	case stream.KindGateway:
		return s.buildGateway(r.id)
```

- [ ] **Step 6 : vérifier**

Run: `go test ./internal/kube/ -run 'TestGatewayAPIAndTraefikInSource|TestTraefikServiceKindStopped'`
Expected: PASS.

Run: `go test -race ./internal/kube/`
Expected: PASS (dont les tests du registre et des CRD de la phase A).

- [ ] **Step 7 : commit**

```bash
git add internal/kube
git commit -m "feat(kube): Gateways, HTTPRoute, GRPCRoute, routes Traefik TCP/UDP et TraefikService dans la source

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10 : PersistentVolumes orphelins dans la source

Informer typé (les PV sont un type du cœur), sondé comme au jalon 8 ; index des PV par PVC réclamé.

**Files:**
- Modify: `internal/kube/source.go`, `internal/kube/source_net.go`
- Test: `internal/kube/source_pv_test.go`

- [ ] **Step 1 : tests qui échouent**

Créer `internal/kube/source_pv_test.go` :

```go
package kube

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/no-inspi/atlas-k8s/internal/model"
	"github.com/no-inspi/atlas-k8s/internal/stream"
)

func pvFixtures() []runtime.Object {
	return append(netFixtures(),
		pvObj("pv-bound", corev1.VolumeBound, "prod/data"), // PVC data : netFixtures
		pvObj("pv-released", corev1.VolumeReleased, "prod/old"),
		pvObj("pv-free", corev1.VolumeAvailable, ""),
	)
}

func TestOrphanPersistentVolumes(t *testing.T) {
	client, sk := startSource(t, pvFixtures()...)
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-bound"); ok {
		t.Error("PV lié à un PVC existant publié")
	}
	o, ok := sk.get(stream.KindPersistentVolume, "pv-released")
	if pv, _ := o.(model.PersistentVolume); !ok || pv.ClaimRef != "prod/old" || pv.Capacity != 10<<30 || pv.Phase != "Released" {
		t.Errorf("pv-released = %v %+v", ok, o)
	}
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-free"); !ok {
		t.Error("PV Available absent")
	}

	// Le PVC disparaît : son PV, resté Bound, devient orphelin.
	if err := client.CoreV1().PersistentVolumeClaims("prod").Delete(context.Background(), "data", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "PV orphelin publié", func() bool { _, ok := sk.get(stream.KindPersistentVolume, "pv-bound"); return ok })

	// Le PV est supprimé : retiré du flux.
	if err := client.CoreV1().PersistentVolumes().Delete(context.Background(), "pv-free", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "PV retiré", func() bool { _, ok := sk.get(stream.KindPersistentVolume, "pv-free"); return !ok })
}

func TestForbiddenPersistentVolumesAreDisabled(t *testing.T) {
	client := fake.NewClientset(pvFixtures()...)
	client.PrependReactor("list", "persistentvolumes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "persistentvolumes"}, "", errors.New("refusé"))
	})
	_, sk := startSourceWith(t, client, Options{})
	if _, ok := sk.get(stream.KindPersistentVolume, "pv-released"); ok {
		t.Error("PV publié malgré le refus")
	}
	if _, ok := sk.get(stream.KindVolume, "prod/data"); !ok {
		t.Error("les PVC doivent rester publiés")
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/kube/ -run 'PersistentVolumes'`
Expected: FAIL (`pv-released = false`).

- [ ] **Step 3 : informer, déclenchements, construction**

Dans `internal/kube/source.go`, struct `Source`, après `pvcs       corelisters.PersistentVolumeClaimLister` :

```go
	pvs        corelisters.PersistentVolumeLister // jalon 9 : PV orphelins
	pvIdx      cache.Indexer
```

et dans `build`, avant `case stream.KindService:` :

```go
	case stream.KindPersistentVolume:
		return s.buildPersistentVolume(r.id)
```

Dans `internal/kube/source_net.go`, `startNetwork`, après le bloc des PVC :

```go
	if s.probe(ctx, "persistentvolumes", func(ctx context.Context) error {
		_, err := c.CoreV1().PersistentVolumes().List(ctx, probeOpts)
		return err
	}) {
		inf := f.Core().V1().PersistentVolumes()
		_ = inf.Informer().AddIndexers(cache.Indexers{indexByClaim: func(o any) ([]string, error) {
			if r := o.(*corev1.PersistentVolume).Spec.ClaimRef; r != nil && r.Name != "" {
				return []string{r.Namespace + "/" + r.Name}, nil
			}
			return nil, nil
		}})
		s.pvs, s.pvIdx = inf.Lister(), inf.Informer().GetIndexer()
		s.watch(inf.Informer(), s.onPV)
	}
```

Remplacer `onPVC` par :

```go
// onPVC : le volume, et les PV qu'il réclame (ils deviennent orphelins ou non).
func (s *Source) onPVC(o any) {
	p, ok := o.(*corev1.PersistentVolumeClaim)
	if !ok {
		return
	}
	id := p.Namespace + "/" + p.Name
	s.mark(ref{stream.KindVolume, id})
	if s.pvIdx == nil {
		return
	}
	pvs, _ := s.pvIdx.ByIndex(indexByClaim, id)
	for _, pv := range pvs {
		s.onPV(pv)
	}
	if p.Spec.VolumeName != "" {
		s.mark(ref{stream.KindPersistentVolume, p.Spec.VolumeName})
	}
}

func (s *Source) onPV(o any) {
	if pv, ok := o.(*corev1.PersistentVolume); ok {
		s.mark(ref{stream.KindPersistentVolume, pv.Name})
	}
}
```

Dans `markNetwork`, après le bloc des PVC :

```go
	if s.pvs != nil {
		all, _ := s.pvs.List(sel)
		for _, o := range all {
			s.onPV(o)
		}
	}
```

Ajouter à la fin du fichier :

```go
func (s *Source) buildPersistentVolume(name string) (any, string, error) {
	if s.pvs == nil {
		return nil, "", nil
	}
	pv, err := s.pvs.Get(name)
	if err != nil {
		return nil, "", err
	}
	var exists ClaimExists
	if s.pvcs != nil {
		exists = func(ns, claim string) bool {
			_, err := s.pvcs.PersistentVolumeClaims(ns).Get(claim)
			return err == nil
		}
	}
	if !PublishPV(pv, exists) {
		return nil, "", nil
	}
	return ConvertPV(pv), name, nil
}
```

- [ ] **Step 4 : vérifier**

Run: `go test -race ./internal/kube/`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/kube
git commit -m "feat(kube): PersistentVolumes sans PVC publiés dans le flux

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11 : droits du flux pour les Gateways, les nouvelles routes et les PV

**Files:**
- Modify: `internal/access/filter.go`
- Test: `internal/access/access_test.go`

- [ ] **Step 1 : tests qui échouent**

Dans `internal/access/access_test.go`, `TestNetworkAttributes`, ajouter aux `cases` :

```go
		{model.Route{Source: model.SourceIngressRouteTCP, Group: "traefik.io", Namespace: "a"}, Attributes{Verb: "list", Group: "traefik.io", Resource: "ingressroutetcps", Namespace: "a"}},
		{model.Route{Source: model.SourceIngressRouteUDP, Group: "traefik.containo.us", Namespace: "a"}, Attributes{Verb: "list", Group: "traefik.containo.us", Resource: "ingressrouteudps", Namespace: "a"}},
		{model.Route{Source: model.SourceHTTPRoute, Group: "gateway.networking.k8s.io", Namespace: "a"}, Attributes{Verb: "list", Group: "gateway.networking.k8s.io", Resource: "httproutes", Namespace: "a"}},
		{model.Route{Source: model.SourceGRPCRoute, Group: "gateway.networking.k8s.io", Namespace: "a"}, Attributes{Verb: "list", Group: "gateway.networking.k8s.io", Resource: "grpcroutes", Namespace: "a"}},
		{model.Gateway{Namespace: "a"}, Attributes{Verb: "list", Group: "gateway.networking.k8s.io", Resource: "gateways", Namespace: "a"}},
		{model.PersistentVolume{Name: "pv"}, Attributes{Verb: "list", Resource: "persistentvolumes"}},
```

et, après la boucle :

```go
	if _, ok := attributes("", model.Route{Source: "Mystère", Namespace: "a"}); ok {
		t.Error("route de source inconnue acceptée")
	}
```

Ajouter :

```go
func TestStreamFilterGatewaysAndVolumes(t *testing.T) {
	var calls atomic.Int32
	f := NewStreamFilter(NewReviewer(fakeClient(&calls, false)), bob)
	ctx := context.Background()
	if f.Allow(ctx, stream.KindPersistentVolume, model.PersistentVolume{Name: "pv-1"}) {
		t.Error("bob, sans droit sur le cluster, ne doit voir aucun PV")
	}
	if f.Allow(ctx, stream.KindGateway, model.Gateway{Namespace: "kube-system", Name: "gw"}) {
		t.Error("bob ne doit voir aucun Gateway de kube-system")
	}
	if !f.Allow(ctx, stream.KindGateway, model.Gateway{Namespace: "production", Name: "gw"}) ||
		!f.Allow(ctx, stream.KindRoute, model.Route{Source: model.SourceHTTPRoute, Group: "gateway.networking.k8s.io", Namespace: "production"}) {
		t.Error("bob doit voir les Gateways et les HTTPRoute de production")
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/access/ -run 'TestNetworkAttributes|TestStreamFilterGatewaysAndVolumes'`
Expected: FAIL (`ingressroutes` au lieu de `ingressroutetcps`, Gateway et PV refusés).

- [ ] **Step 3 : implémentation**

Dans `internal/access/filter.go`, ajouter après `workloadResources` :

```go
// routeResources : ressource à lister pour recevoir une route, selon sa source
// (le groupe est celui de la route).
var routeResources = map[string]string{
	model.SourceIngress:         "ingresses",
	model.SourceIngressRoute:    "ingressroutes",
	model.SourceIngressRouteTCP: "ingressroutetcps",
	model.SourceIngressRouteUDP: "ingressrouteudps",
	model.SourceHTTPRoute:       "httproutes",
	model.SourceGRPCRoute:       "grpcroutes",
}
```

Dans `attributes`, remplacer le cas `case model.Route:` par les cas suivants, et compléter le commentaire de la fonction (« … Services, routes, Gateways et PVC suivent le droit de les lister dans leur namespace ; un PV, objet du cluster, celui de lister les PV. ») :

```go
	case model.Route:
		res, ok := routeResources[o.Source]
		return Attributes{Verb: "list", Group: o.Group, Resource: res, Namespace: o.Namespace}, ok
	case model.Gateway:
		return Attributes{Verb: "list", Group: "gateway.networking.k8s.io", Resource: "gateways", Namespace: o.Namespace}, true
	case model.PersistentVolume:
		return Attributes{Verb: "list", Resource: "persistentvolumes"}, true
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/access/`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add internal/access
git commit -m "feat(access): droits du flux pour les Gateways, les nouvelles routes et les PV

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12 : YAML et événements des nouveaux objets, objets sans namespace

L'inspecteur lit le YAML de chaque nouvel objet (dont TraefikService et GatewayClass, liés depuis une route ou un Gateway) par le client impersonné. Dans les URL, le segment de namespace `_` désigne un objet sans namespace (décision 10).

**Files:**
- Modify: `internal/inspect/inspect.go`, `internal/server/server.go`
- Test: `internal/inspect/inspect_test.go`

- [ ] **Step 1 : test qui échoue**

Dans `internal/inspect/inspect_test.go`, `TestNetworkKinds`, compléter la liste de la boucle :

```go
		{"gateway.networking.k8s.io", "v1", "Gateway"}, {"gateway.networking.k8s.io", "v1", "GatewayClass"},
		{"gateway.networking.k8s.io", "v1", "HTTPRoute"}, {"gateway.networking.k8s.io", "v1", "GRPCRoute"},
		{"traefik.io", "v1alpha1", "IngressRouteTCP"}, {"traefik.containo.us", "v1alpha1", "IngressRouteTCP"},
		{"traefik.io", "v1alpha1", "IngressRouteUDP"}, {"traefik.containo.us", "v1alpha1", "IngressRouteUDP"},
		{"traefik.io", "v1alpha1", "TraefikService"}, {"traefik.containo.us", "v1alpha1", "TraefikService"},
		{"", "v1", "PersistentVolume"},
```

et ajouter à la fin de la fonction :

```go
	for res, want := range map[string]string{"persistentvolumes": "PersistentVolume", "gateways": "Gateway", "httproutes": "HTTPRoute",
		"grpcroutes": "GRPCRoute", "ingressroutetcps": "IngressRouteTCP", "ingressrouteudps": "IngressRouteUDP", "traefikservices": "TraefikService"} {
		if k, ok := KindForResource(res); !ok || k != want {
			t.Errorf("KindForResource(%s) = %s %v", res, k, ok)
		}
	}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/inspect/`
Expected: FAIL (`type d'objet non pris en charge par l'inspecteur`).

- [ ] **Step 3 : implémentation**

Dans `internal/inspect/inspect.go`, ajouter à la fin de `Kinds` :

```go
	{"gateway.networking.k8s.io", "v1", "Gateway", "gateways"},
	{"gateway.networking.k8s.io", "v1", "GatewayClass", "gatewayclasses"},
	{"gateway.networking.k8s.io", "v1", "HTTPRoute", "httproutes"},
	{"gateway.networking.k8s.io", "v1", "GRPCRoute", "grpcroutes"},
	{"traefik.io", "v1alpha1", "IngressRouteTCP", "ingressroutetcps"},
	{"traefik.containo.us", "v1alpha1", "IngressRouteTCP", "ingressroutetcps"},
	{"traefik.io", "v1alpha1", "IngressRouteUDP", "ingressrouteudps"},
	{"traefik.containo.us", "v1alpha1", "IngressRouteUDP", "ingressrouteudps"},
	{"traefik.io", "v1alpha1", "TraefikService", "traefikservices"},
	{"traefik.containo.us", "v1alpha1", "TraefikService", "traefikservices"},
	{"", "v1", "PersistentVolume", "persistentvolumes"},
```

Dans `internal/server/server.go`, ajouter avant `events` :

```go
// nsParam : namespace de l'URL ; « _ » désigne un objet sans namespace
// (PersistentVolume, GatewayClass).
func nsParam(r *http.Request) string {
	if ns := chi.URLParam(r, "ns"); ns != "_" {
		return ns
	}
	return ""
}
```

puis, dans `events` et dans `yaml`, remplacer `chi.URLParam(r, "ns")` par `nsParam(r)`. Les événements d'un objet sans namespace exigent alors `list events` sur tout le cluster (`Inspector.Events`), et le cache les trouve sous `involvedObject.namespace` vide.

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/inspect/ ./internal/server/ ./internal/kube/`
Expected: PASS. Le test de bout en bout (PV et Gateway par le simulateur) arrive à la Task 14.

- [ ] **Step 5 : commit**

```bash
git add internal/inspect internal/server/server.go
git commit -m "feat(inspect): YAML et événements des Gateways, nouvelles routes, TraefikService et PV

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 13 : Gateway API, Traefik complet et PV simulés

Le catalogue porte des routes déjà résolues (poids, miroirs, `via`) ; seuls les états des backends sont calculés à la publication. Un Gateway compte ses routes acceptées dans `attachedRoutes`.

Objets du catalogue de base :

| Objet | Clé | Ce qu'il montre |
| --- | --- | --- |
| Gateway | `infra/public` (classe `eg`) | programmé, adresse 34.120.5.10, listeners `http` 80 et `https` 443 (`*.example.com`), 1 route attachée |
| Gateway | `infra/internal` (classe `eg`) | `Programmed=False`, raison `AddressNotAssigned`, listener `https` non ready, 1 route attachée |
| HTTPRoute | `production/storefront` → `infra/public` | canary 900/100 ‰ vers `frontend` et `frontend-canary` (nouveau Service derrière le workload `frontend`) |
| GRPCRoute | `production/orders-grpc` → `infra/internal` | `orders.v1.Orders/PlaceOrder` → `orders-service` |
| HTTPRoute | `staging/preview` → `infra/public` | refusée (`NotAllowedByListeners`), backend `checkout-preview` |
| IngressRoute | `production/checkout` | TraefikService `production/checkout-split` (weighted : `api-gateway` 3, TraefikService `checkout-mirror` 1) ; `checkout-mirror` : mirroring `orders-service`, miroir `payment-worker` 10 %. Règles : 750, 250, miroir 10, `via` = `production/checkout-split` |
| IngressRouteTCP | `production/postgres` | `HostSNI(*)` → `postgres-payments:5432` |
| IngressRouteUDP | `monitoring/statsd` | → `prometheus:9125` |
| PV | `pv-old-uploads` | Released, `standard-rwo`, 5Gi, ancien claim `staging/old-uploads` |
| PV | `pv-archive-2025` | Released, `standard-rwo`, 100Gi, ancien claim `production/archive-2025` |
| PV | `pv-spare-01` | Available, `premium-rwo`, 50Gi |

`--demo-scale` : un Gateway `team-NNN/edge` pour dix équipes (équipes 1, 11, 21…), une HTTPRoute `web-http` une équipe sur trois (équipes 1, 4, 7…), un PV Released `pv-team-NNN-archive` une équipe sur cinq (1 PV pour 10 PVC).

**Files:**
- Modify: `internal/demo/network.go`, `internal/demo/scale.go`
- Test: `internal/demo/sim_test.go`

- [ ] **Step 1 : tests qui échouent**

Dans `internal/demo/sim_test.go`, `fakeSink.Upsert`, le cas réseau devient :

```go
	case model.Service, model.Route, model.Volume, model.Gateway, model.PersistentVolume:
		f.net[string(kind)+"|"+key] = o
```

et dans `fakeSink.Delete` :

```go
	if kind == stream.KindService || kind == stream.KindRoute || kind == stream.KindVolume ||
		kind == stream.KindGateway || kind == stream.KindPersistentVolume {
```

Ajouter :

```go
// w : poids publié d'un backend, -1 s'il n'en a pas.
func w(b model.Backend) int {
	if b.Weight == nil {
		return -1
	}
	return *b.Weight
}

func TestDemoGatewayAPITraefikAndPV(t *testing.T) {
	_, sink := start(5)
	pub := sink.net["gateway|infra/public"].(model.Gateway)
	if pub.Programmed != model.CondTrue || pub.Class != "eg" || pub.Addresses[0] != "34.120.5.10" || len(pub.Listeners) != 2 || pub.Listeners[0].AttachedRoutes != 1 {
		t.Errorf("infra/public = %+v", pub)
	}
	if in := sink.net["gateway|infra/internal"].(model.Gateway); in.Programmed != model.CondFalse || in.Reason != "AddressNotAssigned" || in.Listeners[0].Name != "https" || in.Listeners[0].Ready != model.CondFalse || in.Listeners[0].AttachedRoutes != 1 {
		t.Errorf("infra/internal = %+v", in)
	}
	shop := sink.net["route|HTTPRoute/production/storefront"].(model.Route)
	if shop.Gate != "infra/public" || len(shop.Gates) != 1 || len(shop.Rules) != 2 || w(shop.Rules[0].Backend) != 900 ||
		w(shop.Rules[1].Backend) != 100 || shop.Rules[1].Backend.Service != "frontend-canary" || shop.Rules[1].Backend.State != model.BackendOK {
		t.Errorf("storefront = %+v", shop)
	}
	if r := sink.net["route|HTTPRoute/staging/preview"].(model.Route); r.Rules[0].Backend.Service != "checkout-preview" ||
		r.Rules[0].Backend.State != model.BackendRefused || r.Parents[0].Reason != "NotAllowedByListeners" {
		t.Errorf("preview = %+v", r)
	}
	if r := sink.net["route|GRPCRoute/production/orders-grpc"].(model.Route); r.Gate != "infra/internal" || r.Rules[0].Match != "orders.v1.Orders/PlaceOrder" || r.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("orders-grpc = %+v", r)
	}
	co := sink.net["route|IngressRoute/production/checkout"].(model.Route)
	if len(co.Rules) != 3 || w(co.Rules[0].Backend) != 750 || w(co.Rules[1].Backend) != 250 || co.Rules[2].Backend.Weight != nil || co.Rules[0].Backend.Via != "production/checkout-split" ||
		!co.Rules[2].Backend.Mirror || co.Rules[2].Backend.Percent != 10 || co.Rules[2].Backend.State != model.BackendOK {
		t.Errorf("checkout = %+v", co.Rules)
	}
	if r := sink.net["route|IngressRouteTCP/production/postgres"].(model.Route); r.Gate != "traefik" || r.Rules[0].Backend.Port != "5432" || r.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("postgres = %+v", r)
	}
	if r := sink.net["route|IngressRouteUDP/monitoring/statsd"].(model.Route); r.Rules[0].Backend.Service != "prometheus" || r.Rules[0].Backend.Port != "9125" || r.Rules[0].Backend.State != model.BackendOK {
		t.Errorf("statsd = %+v", r)
	}
	if pv := sink.net["persistentVolume|pv-old-uploads"].(model.PersistentVolume); pv.Phase != "Released" || pv.ClaimRef != "staging/old-uploads" || pv.Capacity != 5*gi || pv.ReclaimPolicy != "Retain" {
		t.Errorf("pv-old-uploads = %+v", pv)
	}
	if pv := sink.net["persistentVolume|pv-archive-2025"].(model.PersistentVolume); pv.Phase != "Released" || pv.ClaimRef != "production/archive-2025" || pv.Capacity != 100*gi {
		t.Errorf("pv-archive-2025 = %+v", pv)
	}
	if pv, ok := sink.net["persistentVolume|pv-spare-01"].(model.PersistentVolume); !ok || pv.Phase != "Available" || pv.StorageClass != "premium-rwo" || pv.Capacity != 50*gi {
		t.Errorf("pv-spare-01 = %v %+v", ok, pv)
	}
}
```

Dans `TestScaledCatalogHasNetwork`, remplacer le comptage final par :

```go
	bySource := map[string]int{}
	for _, r := range c.routes {
		bySource[r.Source]++
	}
	if bySource[model.SourceIngressRoute] != 4+3 { // grafana, admin, argocd, checkout, puis les équipes 3, 6 et 9
		t.Errorf("IngressRoute = %d", bySource[model.SourceIngressRoute])
	}
	if bySource[model.SourceHTTPRoute] != 2+4 { // storefront, preview, puis les équipes 1, 4, 7 et 10
		t.Errorf("HTTPRoute = %d", bySource[model.SourceHTTPRoute])
	}
	if len(c.gateways) != 2+1 || c.gateways[2].Namespace != "team-001" || len(c.pvs) != 3+2 { // PV : équipes 5 et 10
		t.Errorf("gateways = %d, pvs = %d", len(c.gateways), len(c.pvs))
	}
	for _, r := range c.routes {
		if r.Source == model.SourceHTTPRoute && r.Namespace == "team-007" && r.Gate != "team-001/edge" {
			t.Errorf("team-007/web-http : porte %q", r.Gate)
		}
	}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/demo/ -run 'TestDemoGatewayAPITraefikAndPV|TestScaledCatalogHasNetwork'`
Expected: FAIL à la compilation (`c.gateways undefined`).

- [ ] **Step 3 : catalogue**

Dans `internal/demo/network.go` :

1. Dans `services`, après la ligne `frontend` :

```go
	{NS: "production", Name: "frontend-canary", Workload: "frontend", Port: 80},
```

2. Après `traefikRule`, ajouter :

```go
const (
	gatewayAPI      = "gateway.networking.k8s.io"
	envoyController = "gateway.envoyproxy.io/gatewayclass-controller"
)

func accepted(gw string) model.RouteParent {
	return model.RouteParent{Gateway: gw, Accepted: model.CondTrue, ResolvedRefs: model.CondTrue}
}

func weighted(r model.Rule, weight int) model.Rule {
	r.Backend.Weight = model.Weight(weight)
	return r
}

// checkoutRule : feuille du TraefikService production/checkout-split, déjà
// résolue ; un miroir n'a pas de poids.
func checkoutRule(svc, port string, weight int, mirror bool, percent int) model.Rule {
	r := traefikRule("checkout.example.com", "", "production", svc, port)
	r.Backend.Mirror, r.Backend.Percent, r.Backend.Via = mirror, percent, "production/checkout-split"
	if !mirror {
		r.Backend.Weight = model.Weight(weight)
	}
	return r
}
```

3. À la fin de `baseRoutes`, ajouter :

```go
	{Source: model.SourceHTTPRoute, Group: gatewayAPI, Namespace: "production", Name: "storefront", Gate: "infra/public", Gates: []string{"infra/public"},
		Parents: []model.RouteParent{accepted("infra/public")},
		Rules: []model.Rule{weighted(rule("shop.example.com", "/", "production", "frontend", "80"), 900),
			weighted(rule("shop.example.com", "/", "production", "frontend-canary", "80"), 100)}},
	{Source: model.SourceGRPCRoute, Group: gatewayAPI, Namespace: "production", Name: "orders-grpc", Gate: "infra/internal", Gates: []string{"infra/internal"},
		Parents: []model.RouteParent{accepted("infra/internal")},
		Rules: []model.Rule{{Host: "grpc.example.com", Match: "orders.v1.Orders/PlaceOrder",
			Backend: model.Backend{Namespace: "production", Service: "orders-service", Port: "8080", Kind: "Service"}}}},
	{Source: model.SourceHTTPRoute, Group: gatewayAPI, Namespace: "staging", Name: "preview", Gate: "infra/public", Gates: []string{"infra/public"},
		Parents: []model.RouteParent{{Gateway: "infra/public", Accepted: model.CondFalse, ResolvedRefs: model.CondTrue, Reason: "NotAllowedByListeners"}},
		Rules: []model.Rule{{Host: "preview.example.com", Path: "/",
			Backend: model.Backend{Namespace: "staging", Service: "checkout-preview", Port: "80", Kind: "Service", State: model.BackendRefused}}}},
	{Source: model.SourceIngressRoute, Group: "traefik.io", Namespace: "production", Name: "checkout", Gate: "traefik",
		Rules: []model.Rule{checkoutRule("api-gateway", "80", 750, false, 0), checkoutRule("orders-service", "8080", 250, false, 0),
			checkoutRule("payment-worker", "8080", 0, true, 10)}},
	{Source: model.SourceIngressRouteTCP, Group: "traefik.io", Namespace: "production", Name: "postgres", Gate: "traefik",
		Rules: []model.Rule{{Match: "HostSNI(`*`)", Backend: model.Backend{Namespace: "production", Service: "postgres-payments", Port: "5432", Kind: "Service"}}}},
	{Source: model.SourceIngressRouteUDP, Group: "traefik.io", Namespace: "monitoring", Name: "statsd", Gate: "traefik",
		Rules: []model.Rule{{Backend: model.Backend{Namespace: "monitoring", Service: "prometheus", Port: "9125", Kind: "Service"}}}},
```

4. Après `volumes`, ajouter :

```go
// gateways : attachedRoutes est calculé à la publication.
var gateways = []model.Gateway{
	{Namespace: "infra", Name: "public", Class: "eg", Accepted: model.CondTrue, Programmed: model.CondTrue, Reason: "Programmed",
		Addresses: []string{"34.120.5.10"}, Listeners: []model.Listener{
			{Name: "http", Protocol: "HTTP", Port: 80, Hostname: "*.example.com", Ready: model.CondTrue},
			{Name: "https", Protocol: "HTTPS", Port: 443, Hostname: "*.example.com", Ready: model.CondTrue},
		}},
	{Namespace: "infra", Name: "internal", Class: "eg", Accepted: model.CondTrue, Programmed: model.CondFalse, Reason: "AddressNotAssigned",
		Message: "No addresses have been assigned to the Gateway", Listeners: []model.Listener{
			{Name: "https", Protocol: "HTTPS", Port: 443, Ready: model.CondFalse},
		}},
}

// traefikServices : TraefikService simulés, pour le YAML de l'inspecteur ; les
// routes qui les visent portent déjà leurs Services résolus.
var traefikServices = map[string]map[string]any{
	"production/checkout-split": {"weighted": map[string]any{"services": []any{
		map[string]any{"name": "api-gateway", "port": 80, "weight": 3},
		map[string]any{"name": "checkout-mirror", "kind": "TraefikService", "weight": 1},
	}}},
	"production/checkout-mirror": {"mirroring": map[string]any{"name": "orders-service", "port": 8080,
		"mirrors": []any{map[string]any{"name": "payment-worker", "port": 8080, "percent": 10}}}},
}

var orphanPVs = []model.PersistentVolume{
	{Name: "pv-old-uploads", StorageClass: "standard-rwo", Capacity: 5 * gi, AccessModes: []string{"ReadWriteOnce"},
		ReclaimPolicy: "Retain", Phase: "Released", ClaimRef: "staging/old-uploads"},
	{Name: "pv-archive-2025", StorageClass: "standard-rwo", Capacity: 100 * gi, AccessModes: []string{"ReadWriteOnce"},
		ReclaimPolicy: "Retain", Phase: "Released", ClaimRef: "production/archive-2025"},
	{Name: "pv-spare-01", StorageClass: "premium-rwo", Capacity: 50 * gi, AccessModes: []string{"ReadWriteOnce"},
		ReclaimPolicy: "Retain", Phase: "Available"},
}
```

5. Dans `teamNetwork`, avant `vols := …` :

```go
	if i%3 == 1 {
		gw := teamGateway(i)
		routes = append(routes, model.Route{Source: model.SourceHTTPRoute, Group: gatewayAPI, Namespace: ns, Name: "web-http",
			Gate: gw, Gates: []string{gw}, Parents: []model.RouteParent{accepted(gw)},
			Rules: []model.Rule{rule("app."+host, "/", ns, "web", "80")}})
	}
```

et après `teamNetwork` :

```go
// teamGateway : Gateway partagé par dix équipes, porté par la première.
func teamGateway(i int) string { return fmt.Sprintf("team-%03d/edge", (i-1)/10*10+1) }

func teamGateways(i int, ns string) []model.Gateway {
	if (i-1)%10 != 0 {
		return nil
	}
	return []model.Gateway{{Namespace: ns, Name: "edge", Class: "eg", Accepted: model.CondTrue, Programmed: model.CondTrue, Reason: "Programmed",
		Addresses: []string{fmt.Sprintf("34.120.9.%d", i%250)},
		Listeners: []model.Listener{{Name: "http", Protocol: "HTTP", Port: 80, Ready: model.CondTrue}}}}
}

// teamPVs : un PV Released une équipe sur cinq (1 PV pour 10 PVC).
func teamPVs(i int, ns string) []model.PersistentVolume {
	if i%5 != 0 {
		return nil
	}
	return []model.PersistentVolume{{Name: "pv-" + ns + "-archive", StorageClass: "standard-rwo", Capacity: 10 * gi,
		AccessModes: []string{"ReadWriteOnce"}, ReclaimPolicy: "Retain", Phase: "Released", ClaimRef: ns + "/data-db-2"}}
}
```

6. `withBackendStates` devient (l'état `indirect` n'est plus produit ; un backend refusé l'est par le catalogue) :

```go
func withBackendStates(r model.Route, exists map[string]bool) model.Route {
	out := r
	if len(out.Gates) == 0 {
		out.Gates = []string{r.Gate}
	}
	out.Rules = make([]model.Rule, len(r.Rules))
	for i, rule := range r.Rules {
		b := rule.Backend
		switch {
		case b.State == model.BackendRefused:
		case b.Kind == "Service" && exists[b.Namespace+"/"+b.Service]:
			b.State = model.BackendOK
		default:
			b.State = model.BackendMissing
		}
		rule.Backend = b
		out.Rules[i] = rule
	}
	return out
}
```

7. Dans `flushNetwork`, après la boucle des volumes :

```go
	for _, g := range s.catalog.gateways {
		m := gatewayModel(g, s.catalog.routes)
		publish(stream.KindGateway, model.GatewayKey(m), m)
	}
	for _, p := range s.catalog.pvs {
		publish(stream.KindPersistentVolume, model.PersistentVolumeKey(p), p)
	}
```

et après `withBackendStates` :

```go
// gatewayModel : chaque listener compte les routes acceptées par le Gateway.
func gatewayModel(g model.Gateway, routes []model.Route) model.Gateway {
	key := model.GatewayKey(g)
	var n int32
	for _, r := range routes {
		for _, p := range r.Parents {
			if p.Gateway == key && p.Accepted != model.CondFalse {
				n++
			}
		}
	}
	out := g
	out.Listeners = make([]model.Listener, len(g.Listeners))
	for i, l := range g.Listeners {
		l.AttachedRoutes = n
		out.Listeners[i] = l
	}
	return out
}
```

Dans `internal/demo/scale.go`, `catalog` gagne :

```go
	gateways  []model.Gateway
	pvs       []model.PersistentVolume
```

le catalogue de base devient :

```go
		return catalog{pools: pools, workloads: workloads, services: services, routes: baseRoutes, volumes: volumes,
			gateways: gateways, pvs: orphanPVs}
```

dans le catalogue agrandi, après `c.volumes = append(c.volumes, volumes...)` :

```go
	c.gateways = append(c.gateways, gateways...)
	c.pvs = append(c.pvs, orphanPVs...)
```

et dans la boucle des équipes, après `c.volumes = append(c.volumes, vols...)` :

```go
		c.gateways = append(c.gateways, teamGateways(i, ns)...)
		c.pvs = append(c.pvs, teamPVs(i, ns)...)
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/demo/`
Expected: PASS (dont `TestDemoNetworkAndStorage` : toutes les routes ont `Gates[0] == Gate`).

- [ ] **Step 5 : commit**

```bash
git add internal/demo
git commit -m "feat(demo): Gateways, HTTPRoute pondérée, GRPCRoute, TraefikService, routes TCP/UDP et PV orphelins simulés

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14 : YAML et événements simulés des nouveaux objets

**Files:**
- Modify: `internal/demo/network.go`, `internal/demo/inspect.go`
- Test: `internal/demo/inspect_test.go`, `internal/server/inspect_test.go`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `internal/demo/inspect_test.go` :

```go
func TestDemoGatewayInspector(t *testing.T) {
	s, _ := start(5)
	ctx := context.Background()
	gw := func(kind, ns, name string) inspect.Ref {
		return inspect.Ref{Group: "gateway.networking.k8s.io", Version: "v1", Kind: kind, Namespace: ns, Name: name}
	}
	tr := func(kind, ns, name string) inspect.Ref {
		return inspect.Ref{Group: "traefik.io", Version: "v1alpha1", Kind: kind, Namespace: ns, Name: name}
	}
	cases := []struct {
		ref  inspect.Ref
		want []string
	}{
		{gw("Gateway", "infra", "internal"), []string{"kind: Gateway", "gatewayClassName: eg", "AddressNotAssigned"}},
		{gw("GatewayClass", "", "eg"), []string{"controllerName: gateway.envoyproxy.io/gatewayclass-controller"}},
		{gw("HTTPRoute", "production", "storefront"), []string{"weight: 900", "name: frontend-canary", "namespace: infra"}},
		{gw("GRPCRoute", "production", "orders-grpc"), []string{"service: orders.v1.Orders", "method: PlaceOrder", "name: internal"}},
		{gw("HTTPRoute", "staging", "preview"), []string{"NotAllowedByListeners", "name: checkout-preview"}},
		{tr("IngressRoute", "production", "checkout"), []string{"kind: TraefikService", "name: checkout-split"}},
		{tr("TraefikService", "production", "checkout-mirror"), []string{"mirrors:", "percent: 10", "name: payment-worker"}},
		{tr("IngressRouteTCP", "production", "postgres"), []string{"HostSNI(`*`)", "port: 5432"}},
		{tr("IngressRouteUDP", "monitoring", "statsd"), []string{"name: prometheus", "port: 9125"}},
		{inspect.Ref{Version: "v1", Kind: "PersistentVolume", Name: "pv-old-uploads"},
			[]string{"phase: Released", "persistentVolumeReclaimPolicy: Retain", "name: old-uploads", "storage: 5Gi"}},
	}
	for _, c := range cases {
		doc, err := s.YAML(ctx, anyone, c.ref)
		if err != nil {
			t.Errorf("%+v : %v", c.ref, err)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(doc.YAML, w) {
				t.Errorf("%s %s/%s : %q absent de\n%s", c.ref.Kind, c.ref.Namespace, c.ref.Name, w, doc.YAML)
			}
		}
	}
	if _, err := s.YAML(ctx, anyone, tr("TraefikService", "production", "absent")); err == nil {
		t.Error("TraefikService absent : erreur attendue")
	}
	evs, _ := s.Events(ctx, anyone, "Gateway", "infra", "internal")
	if len(evs) != 1 || evs[0].Reason != "AddressNotAssigned" || evs[0].Type != "Warning" {
		t.Errorf("événements du Gateway non programmé = %+v", evs)
	}
	if evs, _ := s.Events(ctx, anyone, "PersistentVolume", "", "pv-old-uploads"); len(evs) != 0 {
		t.Errorf("événements du PV = %+v", evs)
	}
}
```

Ajouter à `internal/server/inspect_test.go` :

```go
func TestInspectorClusterScopedAndGatewayObjects(t *testing.T) {
	h, _ := demoServer(t)
	if rec := get(h, "/api/yaml/core/v1/PersistentVolume/_/pv-old-uploads"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "kind: PersistentVolume") {
		t.Errorf("yaml du PV = %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/api/namespaces/_/persistentvolumes/pv-old-uploads/events"); rec.Code != 200 {
		t.Errorf("événements du PV = %d", rec.Code)
	}
	if rec := get(h, "/api/yaml/gateway.networking.k8s.io/v1/Gateway/infra/public"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "kind: Gateway") {
		t.Errorf("yaml du Gateway = %d", rec.Code)
	}
	if rec := get(h, "/api/namespaces/infra/gateways/internal/events"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "AddressNotAssigned") {
		t.Errorf("événements du Gateway = %d %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test ./internal/demo/ ./internal/server/ -run 'TestDemoGatewayInspector|TestInspectorClusterScopedAndGatewayObjects'`
Expected: FAIL (`deployments "internal" not found` : les nouveaux kinds tombent dans la recherche des workloads).

- [ ] **Step 3 : implémentation**

Dans `internal/demo/inspect.go`, `YAML`, le cas réseau devient :

```go
	case "Service", "PersistentVolumeClaim", "Ingress", "IngressRoute", "IngressRouteTCP", "IngressRouteUDP",
		"HTTPRoute", "GRPCRoute", "Gateway", "GatewayClass", "TraefikService", "PersistentVolume":
		obj = s.netObject(ref)
```

Dans `internal/demo/network.go`, ajouter `"strconv"` aux imports. Dans `netObject`, remplacer tout le cas `case "Ingress", "IngressRoute":` (jusqu'à la fin du `switch`) par :

```go
	case "Ingress":
		r, ok := s.netLast["route|Ingress/"+ref.Namespace+"/"+ref.Name].(model.Route)
		if !ok {
			return nil
		}
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
	case "IngressRoute", "IngressRouteTCP", "IngressRouteUDP":
		r, ok := s.netLast["route|"+ref.Kind+"/"+ref.Namespace+"/"+ref.Name].(model.Route)
		if !ok || ref.Group != r.Group {
			return nil
		}
		return traefikObject(r, meta)
	case "TraefikService":
		spec, ok := traefikServices[ref.Namespace+"/"+ref.Name]
		if !ok || ref.Group != "traefik.io" {
			return nil
		}
		return map[string]any{"apiVersion": "traefik.io/v1alpha1", "kind": "TraefikService", "metadata": meta, "spec": spec}
	case "HTTPRoute", "GRPCRoute":
		r, ok := s.netLast["route|"+ref.Kind+"/"+ref.Namespace+"/"+ref.Name].(model.Route)
		if !ok {
			return nil
		}
		return gatewayRouteObject(r, meta)
	case "Gateway":
		g, ok := s.netLast["gateway|"+ref.Namespace+"/"+ref.Name].(model.Gateway)
		if !ok {
			return nil
		}
		return gatewayObject(g, meta)
	case "GatewayClass":
		for _, g := range s.catalog.gateways {
			if g.Class == ref.Name {
				return map[string]any{"apiVersion": gatewayAPI + "/v1", "kind": "GatewayClass", "metadata": map[string]any{"name": ref.Name},
					"spec":   map[string]any{"controllerName": envoyController},
					"status": map[string]any{"conditions": []any{condObj("Accepted", model.CondTrue, "", "")}}}
			}
		}
	case "PersistentVolume":
		p, ok := s.netLast["persistentVolume|"+ref.Name].(model.PersistentVolume)
		if !ok {
			return nil
		}
		return pvObject(p)
	}
	return nil
}

// portValue : un port numérique en nombre, un port nommé tel quel.
func portValue(p string) any {
	if n, err := strconv.Atoi(p); err == nil {
		return n
	}
	return p
}

// condObj : condition de statut Kubernetes depuis un tri-état.
func condObj(typ, tri, reason, message string) map[string]any {
	st := "Unknown"
	switch tri {
	case model.CondTrue:
		st = "True"
	case model.CondFalse:
		st = "False"
	}
	if reason == "" {
		reason = typ
	}
	c := map[string]any{"type": typ, "status": st, "reason": reason}
	if message != "" {
		c["message"] = message
	}
	return c
}

// traefikObject : une route Traefik ; les feuilles d'un TraefikService
// redeviennent une seule référence à lui.
func traefikObject(r model.Route, meta map[string]any) map[string]any {
	routes := []any{}
	byMatch := map[string]int{}
	for _, rule := range r.Rules {
		svc := map[string]any{"name": rule.Backend.Service, "port": portValue(rule.Backend.Port)}
		if rule.Backend.Via != "" {
			_, name, _ := strings.Cut(rule.Backend.Via, "/")
			svc = map[string]any{"name": name, "kind": "TraefikService"}
		}
		i, ok := byMatch[rule.Match]
		if !ok {
			i = len(routes)
			byMatch[rule.Match] = i
			ro := map[string]any{"services": []any{}}
			if r.Source == model.SourceIngressRoute {
				ro["kind"] = "Rule"
			}
			if rule.Match != "" {
				ro["match"] = rule.Match
			}
			routes = append(routes, ro)
		}
		ro := routes[i].(map[string]any)
		svcs := ro["services"].([]any)
		dup := false
		for _, x := range svcs {
			dup = dup || reflect.DeepEqual(x, svc)
		}
		if !dup {
			ro["services"] = append(svcs, svc)
		}
	}
	entry := map[string]string{model.SourceIngressRoute: "websecure", model.SourceIngressRouteTCP: "postgres", model.SourceIngressRouteUDP: "statsd"}[r.Source]
	return map[string]any{"apiVersion": r.Group + "/v1alpha1", "kind": r.Source, "metadata": meta,
		"spec": map[string]any{"entryPoints": []any{entry}, "routes": routes}}
}

// gatewayRouteObject : une HTTPRoute ou une GRPCRoute, règles regroupées par chemin ou méthode.
func gatewayRouteObject(r model.Route, meta map[string]any) map[string]any {
	parents := []any{}
	for _, g := range r.Gates {
		ns, name, _ := strings.Cut(g, "/")
		parents = append(parents, map[string]any{"name": name, "namespace": ns})
	}
	spec := map[string]any{"parentRefs": parents}
	if len(r.Rules) > 0 && r.Rules[0].Host != "" {
		spec["hostnames"] = []any{r.Rules[0].Host}
	}
	rules := []any{}
	idx := map[string]int{}
	for _, rule := range r.Rules {
		k := rule.Path + "|" + rule.Match
		i, ok := idx[k]
		if !ok {
			i = len(rules)
			idx[k] = i
			ro := map[string]any{"backendRefs": []any{}}
			if rule.Path != "" {
				ro["matches"] = []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": rule.Path}}}
			}
			if rule.Match != "" {
				svc, meth, _ := strings.Cut(rule.Match, "/")
				m := map[string]any{"service": svc}
				if meth != "" {
					m["method"] = meth
				}
				ro["matches"] = []any{map[string]any{"method": m}}
			}
			rules = append(rules, ro)
		}
		ro := rules[i].(map[string]any)
		ref := map[string]any{"name": rule.Backend.Service, "port": portValue(rule.Backend.Port)}
		if rule.Backend.Namespace != r.Namespace {
			ref["namespace"] = rule.Backend.Namespace
		}
		if rule.Backend.Weight != nil {
			ref["weight"] = *rule.Backend.Weight
		}
		ro["backendRefs"] = append(ro["backendRefs"].([]any), ref)
	}
	spec["rules"] = rules
	status := []any{}
	for _, p := range r.Parents {
		ns, name, _ := strings.Cut(p.Gateway, "/")
		reason := ""
		if p.Accepted == model.CondFalse {
			reason = p.Reason
		}
		status = append(status, map[string]any{"parentRef": map[string]any{"name": name, "namespace": ns}, "controllerName": envoyController,
			"conditions": []any{condObj("Accepted", p.Accepted, reason, ""), condObj("ResolvedRefs", p.ResolvedRefs, "", "")}})
	}
	return map[string]any{"apiVersion": gatewayAPI + "/v1", "kind": r.Source, "metadata": meta, "spec": spec,
		"status": map[string]any{"parents": status}}
}

func gatewayObject(g model.Gateway, meta map[string]any) map[string]any {
	listeners, lstatus := []any{}, []any{}
	for _, l := range g.Listeners {
		spec := map[string]any{"name": l.Name, "protocol": l.Protocol, "port": l.Port}
		if l.Hostname != "" {
			spec["hostname"] = l.Hostname
		}
		listeners = append(listeners, spec)
		lstatus = append(lstatus, map[string]any{"name": l.Name, "attachedRoutes": l.AttachedRoutes,
			"conditions": []any{condObj("Programmed", l.Ready, "", "")}})
	}
	status := map[string]any{"listeners": lstatus, "conditions": []any{
		condObj("Accepted", g.Accepted, "", ""), condObj("Programmed", g.Programmed, g.Reason, g.Message)}}
	if len(g.Addresses) > 0 {
		addrs := []any{}
		for _, a := range g.Addresses {
			addrs = append(addrs, map[string]any{"type": "IPAddress", "value": a})
		}
		status["addresses"] = addrs
	}
	return map[string]any{"apiVersion": gatewayAPI + "/v1", "kind": "Gateway", "metadata": meta,
		"spec": map[string]any{"gatewayClassName": g.Class, "listeners": listeners}, "status": status}
}

func pvObject(p model.PersistentVolume) map[string]any {
	spec := map[string]any{"capacity": map[string]any{"storage": fmt.Sprintf("%dGi", p.Capacity/gi)}, "accessModes": p.AccessModes,
		"persistentVolumeReclaimPolicy": p.ReclaimPolicy, "storageClassName": p.StorageClass}
	if ns, name, ok := strings.Cut(p.ClaimRef, "/"); ok {
		spec["claimRef"] = map[string]any{"kind": "PersistentVolumeClaim", "namespace": ns, "name": name}
	}
	return map[string]any{"apiVersion": "v1", "kind": "PersistentVolume", "metadata": map[string]any{"name": p.Name},
		"spec": spec, "status": map[string]any{"phase": p.Phase}}
}
```

Remplacer `netEvents` par :

```go
// netEvents : un PVC en attente attend son premier consommateur (StorageClass
// en WaitForFirstConsumer), comme sur GKE ou kind ; un Gateway non programmé
// répète son avertissement.
func (s *Sim) netEvents(kind, ns, name string) []model.Event {
	switch kind {
	case "PersistentVolumeClaim":
		if v, ok := s.netLast["volume|"+ns+"/"+name].(model.Volume); ok && v.Phase == "Pending" {
			return []model.Event{{Type: "Normal", Reason: "WaitForFirstConsumer", Count: 12, Source: "persistentvolume-controller",
				Message: "waiting for first consumer to be created before binding", FirstSeen: s.now.Add(-time.Hour), LastSeen: s.now}}
		}
	case "Gateway":
		if g, ok := s.netLast["gateway|"+ns+"/"+name].(model.Gateway); ok && g.Programmed == model.CondFalse {
			return []model.Event{{Type: "Warning", Reason: g.Reason, Count: 30, Source: "envoy-gateway",
				Message: g.Message, FirstSeen: s.now.Add(-2 * time.Hour), LastSeen: s.now}}
		}
	}
	return []model.Event{}
}
```

- [ ] **Step 4 : vérifier**

Run: `go test ./internal/demo/ ./internal/server/`
Expected: PASS (dont `TestDemoNetworkInspector` du jalon 8 : l'IngressRoute `grafana` garde son `Host(...)`).

- [ ] **Step 5 : commit**

```bash
git add internal/demo internal/server/inspect_test.go
git commit -m "feat(demo): YAML et événements des Gateways, nouvelles routes, TraefikService et PV

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 15 : droits du ServiceAccount dans le chart

`get` en plus de `list` et `watch` sur chaque type dont l'inspecteur montre le YAML (le client du ServiceAccount sert en `auth.mode=none`) ; donc aussi sur `gatewayclasses` et `traefikservices`, que l'inspecteur ouvre depuis un Gateway ou une route. Toujours aucune écriture.

**Files:**
- Modify: `deploy/helm/cluster-atlas/templates/clusterrole.yaml`
- Test: `deploy/helm/chart_test.go`

- [ ] **Step 1 : test qui échoue**

Ajouter à `deploy/helm/chart_test.go` (et `"slices"` aux imports) :

```go
func TestClusterRoleReadsJalon9Types(t *testing.T) {
	role := find(mustRender(t, oidc...), "ClusterRole")
	allowed := func(group, resource, verb string) bool {
		for _, r := range get(role, "rules").([]any) {
			if slices.Contains(toStrings(get(r, "apiGroups")), group) && slices.Contains(toStrings(get(r, "resources")), resource) &&
				slices.Contains(toStrings(get(r, "verbs")), verb) {
				return true
			}
		}
		return false
	}
	for _, c := range []struct{ group, resource string }{
		{"apiextensions.k8s.io", "customresourcedefinitions"},
		{"gateway.networking.k8s.io", "gateways"}, {"gateway.networking.k8s.io", "gatewayclasses"},
		{"gateway.networking.k8s.io", "httproutes"}, {"gateway.networking.k8s.io", "grpcroutes"},
		{"traefik.io", "ingressroutetcps"}, {"traefik.containo.us", "ingressrouteudps"}, {"traefik.io", "traefikservices"},
		{"", "persistentvolumes"},
	} {
		for _, verb := range []string{"list", "watch"} {
			if !allowed(c.group, c.resource, verb) {
				t.Errorf("%s %s.%s absent du ClusterRole", verb, c.resource, c.group)
			}
		}
	}
	for _, c := range []struct{ group, resource string }{
		{"gateway.networking.k8s.io", "gateways"}, {"gateway.networking.k8s.io", "httproutes"}, {"traefik.io", "ingressroutetcps"}, {"", "persistentvolumes"},
	} {
		if !allowed(c.group, c.resource, "get") {
			t.Errorf("get %s.%s absent (onglet YAML en auth.mode=none)", c.resource, c.group)
		}
	}
	if allowed("apiextensions.k8s.io", "customresourcedefinitions", "get") {
		t.Error("les CRD ne sont pas inspectables : list et watch suffisent")
	}
}
```

- [ ] **Step 2 : vérifier l'échec**

Run: `go test -count=1 ./deploy/helm -run TestClusterRole`
Expected: FAIL (`list customresourcedefinitions.apiextensions.k8s.io absent du ClusterRole`, …).

- [ ] **Step 3 : ClusterRole**

Dans `deploy/helm/cluster-atlas/templates/clusterrole.yaml`, remplacer la règle :

```yaml
  - apiGroups: [traefik.io, traefik.containo.us]
    resources: [ingressroutes]
    verbs: [get, list, watch]
```

par :

```yaml
  - apiGroups: [traefik.io, traefik.containo.us]
    resources: [ingressroutes, ingressroutetcps, ingressrouteudps, traefikservices]
    verbs: [get, list, watch]
  # Jalon 9 : Gateway API, PV sans PVC, CRD suivies à chaud (lecture seule ;
  # un type refusé est désactivé, sans droit sur les CRD : découverte au démarrage).
  - apiGroups: [gateway.networking.k8s.io]
    resources: [gatewayclasses, gateways, httproutes, grpcroutes]
    verbs: [get, list, watch]
  - apiGroups: [""]
    resources: [persistentvolumes]
    verbs: [get, list, watch]
  - apiGroups: [apiextensions.k8s.io]
    resources: [customresourcedefinitions]
    verbs: [list, watch]
```

- [ ] **Step 4 : vérifier**

Run: `go test -count=1 ./deploy/helm`
Expected: PASS (dont `TestClusterRoleMatchesSpec` : aucun verbe d'écriture).

Run: `go test ./... && go vet ./...`
Expected: PASS, aucune sortie de `go vet`.

- [ ] **Step 5 : commit**

```bash
git add deploy/helm
git commit -m "feat(chart): lecture des Gateways, routes Traefik, TraefikService, PV et CRD

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Phase C — Front : données et scène

Le front reçoit les contrats du backend (voir « Contrats partagés ») et les dessine. Les fonctions pures (`net.ts`, `health.ts`, `netLayout.ts`, `links.ts`) sont testées par Vitest ; la scène (`Network.tsx`, `GroundLinks.tsx`) est vérifiée par `tsc` et, en phase E, par l'e2e démo. Les consommateurs d'interface (`tree.ts`, `searchRank.ts`, `Inspector.tsx`, `NetOverview.tsx`, `PathSummary.tsx`, `route.ts`) continuent de compiler sans changement : `gatesOf` garde son premier paramètre, `route.gate` reste ; la phase D les met à jour.

### Task 16 : types, fixtures et store (Gateways, PV)

**Files:**
- Modify: `web/src/api/types.ts`
- Modify: `web/src/store/fixtures.ts`
- Modify: `web/src/store/cluster.ts`
- Test: `web/src/store/cluster.test.ts`

- [ ] **Step 1 : test qui échoue**

Dans `web/src/store/cluster.test.ts`, remplacer l'import des fixtures :

```ts
import { gateway, node, pod, pv, route, service, volume, workload } from './fixtures'
```

et ajouter à la fin du `describe('réseau et stockage', …)` :

```ts
  it('suit Gateways et PersistentVolumes', () => {
    s().applyMessages([{ type: 'snapshot', rev: 1, gateways: [gateway()], persistentVolumes: [pv()] }])
    expect([...s().gateways.keys()]).toEqual(['infra/public'])
    expect([...s().persistentVolumes.keys()]).toEqual(['pv-1'])
    s().applyMessages([
      { type: 'upsert', kind: 'gateway', rev: 2, obj: gateway({ name: 'internal', programmed: 'false' }) },
      { type: 'delete', kind: 'persistentVolume', rev: 3, obj: pv() },
    ])
    expect([...s().gateways.keys()].sort()).toEqual(['infra/internal', 'infra/public'])
    expect(s().gateways.get('infra/internal')?.programmed).toBe('false')
    expect(s().persistentVolumes.size).toBe(0)
    expect(s().rev).toBe(3)
  })

  it('sélectionne un Gateway ou un PV', () => {
    s().applyMessages([{ type: 'snapshot', rev: 1, gateways: [gateway()], persistentVolumes: [pv()] }])
    s().select({ type: 'gateway', key: 'infra/public' })
    expect(s().selection).toEqual({ type: 'gateway', key: 'infra/public', name: 'public' })
    s().select({ type: 'pv', key: 'pv-1' })
    expect(s().selection?.name).toBe('pv-1')
    s().select({ type: 'pv', key: 'disparu' })
    expect(s().selection?.name).toBe('disparu')
  })

  it('donne des portes à une route même sans gates explicites (fixture)', () => {
    expect(route({ gate: 'traefik' }).gates).toEqual(['traefik'])
    expect(route({ gate: 'infra/public', gates: ['infra/public', 'infra/internal'] }).gates).toHaveLength(2)
  })
```

- [ ] **Step 2 : vérifier l'échec**

Run : `cd web && npx vitest run src/store/cluster.test.ts`
Attendu : FAIL (`gateway` et `pv` absents de `./fixtures`).

- [ ] **Step 3 : types**

Dans `web/src/api/types.ts`, remplacer les interfaces `Backend` et `Route` par :

```ts
export type Tri = 'true' | 'false' | 'unknown'
export type RouteSource = 'Ingress' | 'IngressRoute' | 'IngressRouteTCP' | 'IngressRouteUDP' | 'HTTPRoute' | 'GRPCRoute'

export interface Backend {
  namespace: string
  service: string
  port?: string
  kind: string // Service | TraefikService | autre kind Gateway API
  state: 'ok' | 'missing' | 'refused' | 'indirect'
  /** Part du trafic de la règle, en pour mille (seulement si la règle a plusieurs backends). */
  weight?: number
  /** Backend miroir (Traefik mirroring) : copie du trafic, sans réponse. */
  mirror?: boolean
  /** Miroir : pourcentage du trafic copié. */
  percent?: number
  /** TraefikService racine traversé, « ns/name ». */
  via?: string
}

export interface Rule {
  host?: string
  path?: string
  match?: string
  backend: Backend
}

/** État d'une route Gateway API vis-à-vis d'un de ses Gateways. */
export interface RouteParent {
  gateway: string // « ns/name »
  accepted: Tri
  resolvedRefs: Tri
  reason?: string
}

export interface Route {
  source: RouteSource
  group: string
  namespace: string
  name: string
  /** Première porte (compatibilité) : gates[0]. */
  gate: string
  /** Portes qui servent la route ; une porte Gateway s'appelle « ns/name ». */
  gates: string[]
  rules: Rule[]
  addresses?: string[]
  parents?: RouteParent[]
}

export interface Listener {
  name: string
  protocol: string
  port: number
  hostname?: string
  attachedRoutes: number
  ready: Tri
}

export interface Gateway {
  namespace: string
  name: string
  class: string
  accepted: Tri
  programmed: Tri
  reason?: string
  message?: string
  addresses?: string[]
  listeners: Listener[]
}

/** PersistentVolume sans PVC existant (citerne vide). */
export interface PersistentVolume {
  name: string
  storageClass: string
  capacity: number
  accessModes: string[]
  reclaimPolicy: string
  phase: 'Available' | 'Released' | 'Failed' | 'Bound'
  /** Ancien PVC, « ns/name ». */
  claimRef?: string
}
```

Remplacer `Kind` et `Message` par :

```ts
export type Kind = 'node' | 'pod' | 'workload' | 'namespace' | 'service' | 'route' | 'volume' | 'gateway' | 'persistentVolume'

export type Message =
  | {
      type: 'snapshot'; rev: number; nodes?: Node[]; pods?: Pod[]; workloads?: Workload[]; namespaces?: Namespace[]
      services?: Service[]; routes?: Route[]; volumes?: Volume[]; gateways?: Gateway[]; persistentVolumes?: PersistentVolume[]
    }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'node'; obj: Node }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'pod'; obj: Pod }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'workload'; obj: Workload }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'namespace'; obj: Namespace }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'service'; obj: Service }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'route'; obj: Route }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'volume'; obj: Volume }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'gateway'; obj: Gateway }
  | { type: 'upsert' | 'delete'; rev: number; kind: 'persistentVolume'; obj: PersistentVolume }
  | { type: 'metrics'; metrics: Metrics }
```

Ajouter à la fin du fichier :

```ts
export const gatewayKey = (g: Pick<Gateway, 'namespace' | 'name'>) => `${g.namespace}/${g.name}`
export const pvKey = (p: Pick<PersistentVolume, 'name'>) => p.name
```

- [ ] **Step 4 : fixtures**

Dans `web/src/store/fixtures.ts`, remplacer l'import et la fonction `route` :

```ts
import type { Gateway, Node, PersistentVolume, Pod, Route, Service, Volume, Workload } from '../api/types'
```

```ts
export function route(over: Partial<Route> = {}): Route {
  const gate = over.gate ?? 'nginx'
  return {
    source: 'Ingress', group: 'networking.k8s.io', namespace: 'production', name: 'storefront', gate, gates: [gate],
    rules: [{ host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'api', port: '80', kind: 'Service', state: 'ok' } }],
    ...over,
  }
}
```

et ajouter :

```ts
export function gateway(over: Partial<Gateway> = {}): Gateway {
  return {
    namespace: 'infra', name: 'public', class: 'eg', accepted: 'true', programmed: 'true', addresses: ['203.0.113.10'],
    listeners: [{ name: 'https', protocol: 'HTTPS', port: 443, hostname: '*.example.com', attachedRoutes: 1, ready: 'true' }],
    ...over,
  }
}

export function pv(over: Partial<PersistentVolume> = {}): PersistentVolume {
  return {
    name: 'pv-1', storageClass: 'standard-rwo', capacity: 20 * 2 ** 30, accessModes: ['ReadWriteOnce'],
    reclaimPolicy: 'Retain', phase: 'Released', claimRef: 'production/old-data',
    ...over,
  }
}
```

- [ ] **Step 5 : store**

Dans `web/src/store/cluster.ts` :

Import :

```ts
import {
  gatewayKey, pvKey, routeKey, serviceKey, volumeKey, workloadKey,
  type Gateway, type Me, type Message, type Metrics, type Namespace, type Node, type PersistentVolume, type Pod, type Route,
  type Service, type Volume, type Workload,
} from '../api/types'
```

Type de sélection :

```ts
export type SelectionType = 'pod' | 'node' | 'service' | 'route' | 'volume' | 'gate' | 'gateway' | 'pv'
```

Dans `ClusterState`, après `volumes` :

```ts
  gateways: Map<string, Gateway>
  /** PV sans PVC existant, par nom. */
  persistentVolumes: Map<string, PersistentVolume>
```

Dans `initial()`, après `volumes` :

```ts
  gateways: new Map<string, Gateway>(),
  persistentVolumes: new Map<string, PersistentVolume>(),
```

Dans `applyMessages`, la déstructuration devient :

```ts
      let { rev, metrics, nodes, pods, workloads, namespaces, services, routes, volumes, gateways, persistentVolumes } = st
```

dans `case 'snapshot':`, après `volumes = …` :

```ts
            gateways = new Map((m.gateways ?? []).map((x) => [gatewayKey(x), x]))
            persistentVolumes = new Map((m.persistentVolumes ?? []).map((x) => [pvKey(x), x]))
```

dans la branche upsert/delete, après la branche `volume` :

```ts
            } else if (m.kind === 'gateway') {
              if (del) gateways.delete(gatewayKey(m.obj))
              else gateways.set(gatewayKey(m.obj), m.obj)
            } else if (m.kind === 'persistentVolume') {
              if (del) persistentVolumes.delete(pvKey(m.obj))
              else persistentVolumes.set(pvKey(m.obj), m.obj)
```

et le `set` final :

```ts
      set({
        rev, metrics, nodes, pods, workloads, namespaces, services, routes, volumes, gateways, persistentVolumes, feed,
        version: changed ? st.version + 1 : st.version,
        connection: 'live',
      })
```

Dans `select`, la table des noms gagne :

```ts
        gateway: () => st.gateways.get(sel.key)?.name,
        pv: () => st.persistentVolumes.get(sel.key)?.name,
```

- [ ] **Step 6 : vérifier**

Run : `cd web && npx vitest run src/store/cluster.test.ts && npx tsc --noEmit`
Attendu : PASS ; `tsc` sans erreur (les routes construites ailleurs passent toutes par la fixture ou le flux).

- [ ] **Step 7 : commit**

```bash
git add web/src/api/types.ts web/src/store/fixtures.ts web/src/store/cluster.ts web/src/store/cluster.test.ts
git commit -m "feat(web): Gateways, PersistentVolumes et nouveaux champs de route dans le store

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 17 : portes multi-routes et Gateways

**Files:**
- Modify: `web/src/store/net.ts`
- Test: `web/src/store/net.test.ts`

- [ ] **Step 1 : test qui échoue**

Dans `web/src/store/net.test.ts`, remplacer les imports :

```ts
import { describe, expect, it } from 'vitest'
import { gatewayKey } from '../api/types'
import { gateway, route, service, volume } from './fixtures'
import { gatesOf, gatesOfRoute, isGatewayGate, readyCount, routeBroken, routeRefused, routesTo, servicesOfPod, volumesOfPod } from './net'
```

et ajouter :

```ts
describe('portes Gateway API', () => {
  const gw = gateway()
  const canary = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'canary', gate: 'infra/public', gates: ['infra/public', 'infra/internal'] })
  const legacy = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'legacy', gate: 'infra/public', gates: ['infra/public'],
    rules: [{ backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'refused' } }] })

  it('range une route sous chacune de ses portes et joint le Gateway visible', () => {
    const gates = gatesOf([canary, legacy, route()], new Map([[gatewayKey(gw), gw]]))
    expect(gates.map((g) => [g.name, g.routes.map((r) => r.name), g.refused, !!g.gateway])).toEqual([
      ['infra/internal', ['canary'], 0, false],
      ['infra/public', ['canary', 'legacy'], 1, true],
      ['nginx', ['storefront'], 0, false],
    ])
  })

  it('garde une porte pour un Gateway sans route', () => {
    expect(gatesOf([], new Map([[gatewayKey(gw), gw]])).map((g) => [g.name, g.routes.length])).toEqual([['infra/public', 0]])
  })

  it('se replie sur gate quand gates est vide', () => {
    const old = { ...route(), gates: [] as string[] }
    expect(gatesOfRoute(old)).toEqual(['nginx'])
    expect(gatesOf([old]).map((g) => g.name)).toEqual(['nginx'])
  })

  it('distingue refus et backend introuvable, et les portes Gateway', () => {
    expect(routeRefused(legacy)).toBe(true)
    expect(routeBroken(legacy)).toBe(false)
    expect(isGatewayGate('infra/public')).toBe(true)
    expect(isGatewayGate('nginx')).toBe(false)
    expect(isGatewayGate('(sans gateway)')).toBe(false)
  })
})
```

- [ ] **Step 2 : vérifier l'échec**

Run : `cd web && npx vitest run src/store/net.test.ts`
Attendu : FAIL (`gatesOfRoute`, `isGatewayGate`, `routeRefused` non exportés).

- [ ] **Step 3 : implémentation**

Dans `web/src/store/net.ts`, remplacer l'en-tête jusqu'à `gatesOf` inclus par :

```ts
import type { Gateway, Route, Service, Volume } from '../api/types'

// Relations dérivées du flux : portes (déduites des routes, enrichies des
// Gateways reçus), routes d'un Service, Services et volumes d'un pod.

export interface Gate {
  name: string
  routes: Route[]
  /** Routes dont un backend est introuvable. */
  broken: number
  /** Routes refusées par leur Gateway. */
  refused: number
  /** Gateway représenté par la porte (Gateway API), s'il est visible. */
  gateway?: Gateway
}

export const routeBroken = (r: Route) => r.rules.some((x) => x.backend.state === 'missing')
export const routeRefused = (r: Route) => r.rules.some((x) => x.backend.state === 'refused')

/** Une porte Gateway s'appelle « ns/name » ; une IngressClass ne contient jamais de « / ». */
export const isGatewayGate = (name: string) => name.includes('/')

/** Portes d'une route ; repli sur gate pour un objet antérieur au jalon 9. */
export const gatesOfRoute = (r: Route): string[] => (r.gates?.length ? r.gates : [r.gate])

/** Ordre des noms indépendant de la locale du navigateur. */
const byName = (a: string, b: string) => (a < b ? -1 : a > b ? 1 : 0)
const byNsName = (a: { namespace: string; name: string }, b: { namespace: string; name: string }) =>
  byName(a.namespace, b.namespace) || byName(a.name, b.name)

/**
 * Portes triées par nom : une par porte citée par une route (une route à
 * plusieurs portes apparaît sous chacune), plus une par Gateway visible, même
 * sans route. Routes de chaque porte triées par namespace puis nom.
 */
export function gatesOf(routes: Iterable<Route>, gateways?: ReadonlyMap<string, Gateway>): Gate[] {
  const by = new Map<string, Route[]>()
  for (const k of gateways?.keys() ?? []) by.set(k, [])
  for (const r of routes)
    for (const g of new Set(gatesOfRoute(r))) {
      const list = by.get(g)
      if (list) list.push(r)
      else by.set(g, [r])
    }
  return [...by]
    .sort(([a], [b]) => byName(a, b))
    .map(([name, rs]) => {
      const sorted = [...rs].sort(byNsName)
      const gw = gateways?.get(name)
      return {
        name, routes: sorted, broken: sorted.filter(routeBroken).length, refused: sorted.filter(routeRefused).length,
        ...(gw ? { gateway: gw } : {}),
      }
    })
}
```

Le reste du fichier (`routesTo`, `servicesOfPod`, `volumesOfPod`, `readyCount`) est inchangé.

- [ ] **Step 4 : vérifier**

Run : `cd web && npx vitest run src/store/net.test.ts && npx tsc --noEmit`
Attendu : PASS (l'ancien test « regroupe les routes par porte » passe toujours).

- [ ] **Step 5 : commit**

```bash
git add web/src/store/net.ts web/src/store/net.test.ts
git commit -m "feat(web): portes multiples par route et portes Gateway

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 18 : voyants des portes Gateway et des PV

**Files:**
- Modify: `web/src/scene/health.ts`
- Test: `web/src/scene/health.test.ts`

- [ ] **Step 1 : test qui échoue**

Remplacer `web/src/scene/health.test.ts` par :

```ts
import { describe, expect, it } from 'vitest'
import { gateway } from '../store/fixtures'
import { gateSignal, healthSignal, pvSignal, volumeSignal, worst } from './health'

describe('signaux', () => {
  it('traduit santé, phase et routes cassées en couleur de voyant', () => {
    expect(['ok', 'degraded', 'down', 'external'].map((h) => healthSignal(h as never))).toEqual(['ok', 'warn', 'err', 'mute'])
    expect(['Bound', 'Pending', 'Lost'].map((p) => volumeSignal({ phase: p as never }))).toEqual(['ok', 'warn', 'err'])
    expect(gateSignal({ name: 'nginx', broken: 0 })).toBe('ok')
    expect(gateSignal({ name: 'nginx', broken: 2 })).toBe('warn')
    expect(gateSignal({ name: '(sans gateway)', broken: 0, refused: 1 })).toBe('warn')
  })

  it('colore une porte Gateway selon son état', () => {
    const g = (over: Parameters<typeof gateway>[0] = {}, broken = 0, refused = 0) =>
      gateSignal({ name: 'infra/public', broken, refused, gateway: gateway(over) })
    expect(g()).toBe('ok')
    expect(g({ programmed: 'false' }, 1)).toBe('err')
    expect(g({}, 0, 1)).toBe('warn')
    expect(g({}, 1)).toBe('warn')
    expect(g({ listeners: [{ name: 'h', protocol: 'HTTP', port: 80, attachedRoutes: 0, ready: 'false' }] })).toBe('warn')
    expect(g({ programmed: 'unknown' })).toBe('mute')
    expect(gateSignal({ name: 'infra/public', broken: 0, refused: 0 })).toBe('mute') // Gateway invisible
    expect(gateSignal({ name: 'infra/public', broken: 1, refused: 0 })).toBe('warn')
  })

  it('ne signale que les PV en échec', () => {
    expect(['Available', 'Released', 'Bound', 'Failed'].map((p) => pvSignal({ phase: p as never }))).toEqual(['mute', 'mute', 'mute', 'err'])
  })

  it('garde le pire signal d’un groupe', () => {
    expect(worst(['ok', 'warn', 'ok'])).toBe('warn')
    expect(worst(['mute', 'err', 'warn'])).toBe('err')
    expect(worst([])).toBe('mute')
  })
})
```

- [ ] **Step 2 : vérifier l'échec**

Run : `cd web && npx vitest run src/scene/health.test.ts`
Attendu : FAIL (`pvSignal` absent ; `gateSignal` ne connaît pas les Gateways).

- [ ] **Step 3 : implémentation**

Remplacer `web/src/scene/health.ts` par :

```ts
import type { Health, PersistentVolume, Volume } from '../api/types'
import { isGatewayGate, type Gate } from '../store/net'

// Couleur des voyants (relais, portes, citernes), dans le vocabulaire des badges.

export type Signal = 'ok' | 'warn' | 'err' | 'mute'

const RANK: Record<Signal, number> = { mute: 0, ok: 1, warn: 2, err: 3 }

export const healthSignal = (h: Health): Signal => (h === 'ok' ? 'ok' : h === 'degraded' ? 'warn' : h === 'down' ? 'err' : 'mute')
export const volumeSignal = (v: Pick<Volume, 'phase'>): Signal => (v.phase === 'Bound' ? 'ok' : v.phase === 'Pending' ? 'warn' : 'err')
/** Citerne vide : seule une PV en échec est signalée. */
export const pvSignal = (p: Pick<PersistentVolume, 'phase'>): Signal => (p.phase === 'Failed' ? 'err' : 'mute')

/**
 * Porte déduite (IngressClass, traefik) : orange si une route est cassée ou
 * refusée. Porte Gateway : rouge si le Gateway n'est pas programmé ; orange si
 * un listener n'est pas prêt ou une route cassée ou refusée ; grise sans statut
 * écrit ou sans Gateway visible ; verte sinon.
 */
export function gateSignal(g: Pick<Gate, 'name' | 'broken'> & Partial<Pick<Gate, 'refused' | 'gateway'>>): Signal {
  const troubled = g.broken > 0 || (g.refused ?? 0) > 0
  if (!isGatewayGate(g.name)) return troubled ? 'warn' : 'ok'
  const gw = g.gateway
  if (!gw) return troubled ? 'warn' : 'mute'
  if (gw.programmed === 'false') return 'err'
  if (troubled || gw.listeners.some((l) => l.ready === 'false')) return 'warn'
  return gw.programmed === 'unknown' ? 'mute' : 'ok'
}

export const worst = (xs: Signal[]): Signal => xs.reduce<Signal>((w, x) => (RANK[x] > RANK[w] ? x : w), 'mute')

export const HEALTH_LABEL: Record<Health, string> = {
  ok: 'Sain', degraded: 'Dégradé', down: 'Aucun endpoint ready', external: 'ExternalName',
}
```

- [ ] **Step 4 : vérifier**

Run : `cd web && npx vitest run src/scene/health.test.ts && npx tsc --noEmit`
Attendu : PASS.

- [ ] **Step 5 : commit**

```bash
git add web/src/scene/health.ts web/src/scene/health.test.ts
git commit -m "feat(web): voyants des portes Gateway et des PV orphelins

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 19 : citernes vides dans la disposition

**Files:**
- Modify: `web/src/scene/netLayout.ts`
- Test: `web/src/scene/netLayout.test.ts`

- [ ] **Step 1 : test qui échoue**

Dans `web/src/scene/netLayout.test.ts`, compléter l'import :

```ts
import { layoutNetwork, RELAY_PITCH, RELAY_PITCH_MIN, tankRadius, type OrphanInput, type RelayInput, type TankInput } from './netLayout'
```

et ajouter :

```ts
describe('citernes vides (PV sans PVC)', () => {
  const orphan = (name: string, storageClass: string, gib = 5): OrphanInput => ({ key: name, name, storageClass, capacity: gib * GiB })
  const tanks = [tank('production', 'data-0', 'fast'), tank('production', 'data-1', 'fast')]
  const orphans = [orphan('pv-b', 'fast'), orphan('pv-a', 'slow', 50), orphan('pv-c', 'fast')]

  it('range les PV orphelins après les PVC, dans l’îlot de leur classe', () => {
    const net = layoutNetwork(city, [], [], tanks, orphans)
    expect(net.islands.map((i) => i.storageClass)).toEqual(['fast', 'slow'])
    expect(net.orphans.map((o) => o.key)).toEqual(['pv-b', 'pv-c', 'pv-a'])
    const fast = net.islands[0]
    for (const o of net.orphans.slice(0, 2)) {
      expect(Math.abs(o.x - fast.x)).toBeLessThan(fast.width / 2)
      expect(Math.abs(o.z - fast.z)).toBeLessThan(fast.depth / 2)
    }
    expect(net.orphans[2].r).toBeCloseTo(tankRadius(50 * GiB))
    const all = [...net.tanks.values(), ...net.orphans].map((t) => `${t.x.toFixed(3)},${t.z.toFixed(3)}`)
    expect(new Set(all).size).toBe(all.length)
  })

  it('ne dépend pas de l’ordre d’arrivée', () => {
    const a = layoutNetwork(city, [], [], tanks, orphans)
    const b = layoutNetwork(city, [], [], [...tanks].reverse(), [...orphans].reverse())
    expect(a.orphans).toEqual(b.orphans)
    expect([...a.tanks.entries()].sort()).toEqual([...b.tanks.entries()].sort())
  })

  it('sans PV orphelin, la disposition des PVC est inchangée', () => {
    const a = layoutNetwork(city, [], [], tanks)
    const b = layoutNetwork(city, [], [], tanks, [])
    expect(a.orphans).toEqual([])
    expect([...a.tanks.entries()]).toEqual([...b.tanks.entries()])
  })
})
```

- [ ] **Step 2 : vérifier l'échec**

Run : `cd web && npx vitest run src/scene/netLayout.test.ts`
Attendu : FAIL (`OrphanInput` et `net.orphans` inexistants).

- [ ] **Step 3 : implémentation**

Dans `web/src/scene/netLayout.ts` :

Après `TankInput` :

```ts
/** PV sans PVC : citerne vide dans l'îlot de sa classe, après les PVC. */
export interface OrphanInput { key: string; name: string; storageClass: string; capacity: number }
```

Dans `NetLayout`, après `tanks` :

```ts
  /** Citernes vides (PV sans PVC), îlot par îlot. */
  orphans: TankSlot[]
```

Dans `emptyNetwork`, ajouter `orphans: [],` après `tanks: new Map(),`.

Signature :

```ts
export function layoutNetwork(city: CityLayout, relays: RelayInput[], gates: string[], tanks: TankInput[], orphans: OrphanInput[] = []): NetLayout {
```

Remplacer tout le bloc « Entrepôts » (de `const byClass = …` jusqu'à `const warehouse = …` inclus) par :

```ts
  // Entrepôts : un îlot par StorageClass, empilés du nord au sud, assez de
  // colonnes pour rester à peu près aussi profonds que la ville. Dans un îlot,
  // les PVC (par namespace puis nom), puis les PV sans PVC (par nom).
  const byClass = new Map<string, { tanks: TankInput[]; orphans: OrphanInput[] }>()
  const entry = (c: string) => {
    let e = byClass.get(c)
    if (!e) byClass.set(c, (e = { tanks: [], orphans: [] }))
    return e
  }
  for (const t of tanks) entry(t.storageClass || UNCLASSED).tanks.push(t)
  for (const o of orphans) entry(o.storageClass || UNCLASSED).orphans.push(o)
  const size = (c: string) => byClass.get(c)!.tanks.length + byClass.get(c)!.orphans.length
  const classes = [...byClass.keys()].sort(byName)
  const islandDepth = (n: number, cols: number) => ISLAND_LABEL + Math.ceil(n / cols) * TANK_PITCH + 0.4
  const depthWith = (cols: number) =>
    classes.reduce((d, c) => d + islandDepth(size(c), cols), 0) + ISLAND_GAP * Math.max(0, classes.length - 1)
  const largest = Math.max(0, ...classes.map(size))
  const count = tanks.length + orphans.length
  let cols = Math.max(TANK_COLS, Math.ceil(count / Math.max(1, Math.floor((bottom - top) / TANK_PITCH))))
  while (cols < largest && depthWith(cols) > bottom - top) cols++

  const q = city.queue
  const wx = Math.max(right, q.x + q.width / 2) + WAREHOUSE_GAP
  const width = cols * TANK_PITCH + 0.8
  const islands: Island[] = []
  const tankSlots = new Map<string, TankSlot>()
  const orphanSlots: TankSlot[] = []
  let z = top
  for (const c of classes) {
    const e = byClass.get(c)!
    const ts = e.tanks.sort((a, b) => byName(a.namespace, b.namespace) || byName(a.name, b.name))
    const os = e.orphans.sort((a, b) => byName(a.name, b.name))
    const depth = islandDepth(ts.length + os.length, cols)
    islands.push({ storageClass: c, x: wx + width / 2, z: z + depth / 2, width, depth })
    const slot = (k: number, key: string, bytes: number): TankSlot => ({
      key,
      x: wx + 0.4 + TANK_PITCH * ((k % cols) + 0.5),
      z: z + ISLAND_LABEL + TANK_PITCH * (Math.floor(k / cols) + 0.5),
      r: tankRadius(bytes),
    })
    ts.forEach((t, k) => tankSlots.set(t.key, slot(k, t.key, t.requested)))
    os.forEach((o, j) => orphanSlots.push(slot(ts.length + j, o.key, o.capacity)))
    z += depth + ISLAND_GAP
  }
  const zEnd = z - ISLAND_GAP
  const warehouse = islands.length ? { x: wx + width / 2, z: (top + zEnd) / 2, width: width + 1, depth: zEnd - top + 1 } : null
```

Le `return` final devient :

```ts
  return { segments, relays: out, groups, gates: gateSlots, islands, tanks: tankSlots, orphans: orphanSlots, warehouse, lanes, westX, eastX, bounds }
```

Note : `e.tanks.sort` trie le tableau local construit plus haut, jamais l'entrée de l'appelant.

- [ ] **Step 4 : vérifier**

Run : `cd web && npx vitest run src/scene/netLayout.test.ts src/scene/links.test.ts && npx tsc --noEmit`
Attendu : PASS (tests du jalon 8 compris, dont « à l'échelle »).

- [ ] **Step 5 : commit**

```bash
git add web/src/scene/netLayout.ts web/src/scene/netLayout.test.ts
git commit -m "feat(web): citernes vides des PV orphelins dans les entrepôts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 20 : liens multi-portes, refus, miroirs et poids

**Files:**
- Modify: `web/src/scene/links.ts`
- Test: `web/src/scene/links.test.ts`

- [ ] **Step 1 : test qui échoue**

Dans `web/src/scene/links.test.ts`, ajouter à la fin :

```ts
describe('Gateway API et Traefik complet', () => {
  const canary = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'canary',
    gate: 'infra/public', gates: ['infra/public', 'infra/internal'], rules: [
      { host: 'shop', path: '/', backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'ok', weight: 900 } },
      { host: 'shop', path: '/', backend: { namespace: 'production', service: 'db', kind: 'Service', state: 'ok', weight: 0 } },
    ] })
  const shadow = route({ source: 'IngressRoute', group: 'traefik.io', name: 'shadow', gate: 'traefik', rules: [
    { match: 'Host(`s`)', backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'ok', via: 'production/split' } },
    { match: 'Host(`s`)', backend: { namespace: 'production', service: 'db', kind: 'Service', state: 'ok', mirror: true, percent: 10, via: 'production/split' } },
  ] })
  const legacy = route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', name: 'legacy', gate: 'infra/public', rules: [
    { backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'refused' } },
    { backend: { namespace: 'production', service: 'db', kind: 'Service', state: 'refused' } },
  ] })
  const lost = route({ source: 'IngressRoute', group: 'traefik.io', name: 'lost', gate: 'traefik', rules: [
    { match: 'Host(`l`)', backend: { namespace: 'production', service: 'nowhere', kind: 'TraefikService', state: 'missing' } },
  ] })
  const gnet = layoutNetwork(city, [api, db].map((s) => ({ key: serviceKey(s), namespace: s.namespace, name: s.name })),
    ['infra/internal', 'infra/public', 'traefik'], [])
  const ls = buildLinks({ city, net: gnet, services: [api, db], routes: [canary, shadow, legacy, lost], volumes: [],
    pods: new Map(pods.map((p) => [p.uid, p])), targets })
  const of = (f: Link['family']) => ls.filter((l) => l.family === f)
  const main = (gate: string, svc: string) => of('main').find((l) => l.keys[0] === `gate:${gate}` && l.keys[1] === `service:${svc}`)

  it('trace une ligne principale depuis chaque porte de la route', () => {
    expect(of('main').filter((l) => l.keys.includes(`route:${routeKey(canary)}`)).map((l) => l.keys.slice(0, 2)).sort()).toEqual([
      ['gate:infra/internal', 'service:production/api'], ['gate:infra/internal', 'service:production/db'],
      ['gate:infra/public', 'service:production/api'], ['gate:infra/public', 'service:production/db'],
    ])
  })

  it('porte le poids et éteint les paquets d’une ligne de poids 0', () => {
    expect([main('infra/public', 'production/api')!.weight, main('infra/public', 'production/api')!.live]).toEqual([900, true])
    expect([main('infra/public', 'production/db')!.weight, main('infra/public', 'production/db')!.live]).toEqual([0, false])
    expect(main('traefik', 'production/api')!.weight).toBeUndefined()
  })

  it('dessine les miroirs à part, sans paquets', () => {
    expect(of('mirror').map((l) => [l.keys[0], l.keys[1], l.live])).toEqual([['gate:traefik', 'service:production/db', false]])
    expect(main('traefik', 'production/db')).toBeUndefined()
  })

  it('signale une route refusée une fois par porte et namespace, avec un panneau', () => {
    expect(of('refused').map((l) => l.keys)).toEqual([['gate:infra/public', `route:${routeKey(legacy)}`]])
    expect(of('refused')[0].sign).toBeDefined()
    expect(of('main').some((l) => l.keys.includes(`route:${routeKey(legacy)}`))).toBe(false)
  })

  it('signale aussi un TraefikService introuvable', () => {
    expect(of('broken').map((l) => l.keys)).toEqual([['gate:traefik', `route:${routeKey(lost)}`]])
  })

  it('chemin d’un Gateway : sa porte, ses routes, ses Services et leurs pods ; d’un PV : lui seul', () => {
    const p = pathOf('gateway:infra/public', ls)
    expect([...p]).toEqual(expect.arrayContaining([
      'gateway:infra/public', 'gate:infra/public', `route:${routeKey(legacy)}`, 'service:production/api', 'service:production/db', 'pod:a', 'pod:db',
    ]))
    expect(p.has('gate:traefik')).toBe(false)
    expect([...pathOf('pv:pv-1', ls)]).toEqual(['pv:pv-1'])
    expect([...pathOf('service:production/db', ls)]).toEqual(expect.arrayContaining(['gate:traefik', 'gate:infra/public', 'gate:infra/internal']))
    expect([...pathOf('pod:db', ls)]).toEqual(expect.arrayContaining(['service:production/db', 'gate:traefik']))
  })
})
```

- [ ] **Step 2 : vérifier l'échec**

Run : `cd web && npx vitest run src/scene/links.test.ts`
Attendu : FAIL (pas de famille `mirror` ni `refused`, une seule porte par route, pas de `weight`).

- [ ] **Step 3 : implémentation**

Dans `web/src/scene/links.ts` :

Imports :

```ts
import { routeKey, serviceKey, volumeKey, type Pod, type Route, type Service, type Volume } from '../api/types'
import { gatesOfRoute } from '../store/net'
import { ALLEY, type CityLayout } from './layout'
import { TANK_PITCH, type NetLayout } from './netLayout'
```

Types :

```ts
export type Family = 'main' | 'broken' | 'refused' | 'mirror' | 'data' | 'fibre'
export type Pt = [number, number]

export interface Link {
  family: Family
  points: Pt[]
  /** Objets reliés, « type:clé » : gate:, route:, service:, pod:, volume:. */
  keys: string[]
  /** Paquets ou gouttes qui circulent (endpoint ready, pod Running, ligne de poids non nul). */
  live: boolean
  /** Namespace du lien (Service, backend de la route ou PVC) : les chips de namespace l'estompent. */
  ns: string
  /** Route cassée ou refusée : position du panneau « ? » ou « ⊘ ». */
  sign?: Pt
  /** Ligne principale : plus forte part du trafic (pour mille) parmi ses règles ; absent si l'une n'est pas pondérée. */
  weight?: number
}
```

Après `toPod`, ajouter :

```ts
/** Poids d'une ligne partagée : un backend non pondéré reçoit tout le trafic de sa règle. */
const mergeWeight = (a: number | undefined, b: number | undefined) => (a === undefined || b === undefined ? undefined : Math.max(a, b))

/** Familles qui partent d'une porte. */
const FROM_GATE: Family[] = ['main', 'broken', 'refused', 'mirror']
/** Familles qui arrivent à un relais depuis une porte. */
const TO_SERVICE: Family[] = ['main', 'mirror']
```

Dans `buildLinks`, remplacer la boucle des lignes principales (de `// Lignes principales` jusqu'à `out.push(...mains.values())` inclus) par :

```ts
  // Lignes principales : une par (porte, Service), toutes routes confondues ;
  // une route à plusieurs portes en trace une depuis chacune. Miroirs à part.
  // Route cassée (backend introuvable) ou refusée (Gateway) : une ligne vers le
  // tronçon du namespace visé, avec un panneau, par (porte, route, namespace).
  const mains = new Map<string, Link>()
  const signSeen = new Set<string>()
  for (const r of i.routes) {
    const rk = `route:${routeKey(r)}`
    for (const gateName of new Set(gatesOfRoute(r))) {
      const gate = net.gates.get(gateName)
      if (!gate) continue
      const gk = `gate:${gateName}`
      for (const rule of r.rules) {
        const b = rule.backend
        if (b.state === 'missing' || b.state === 'refused') {
          const family: Family = b.state === 'missing' ? 'broken' : 'refused'
          const id = `${family}|${gateName}|${rk}|${b.namespace}`
          if (signSeen.has(id)) continue
          signSeen.add(id)
          const seg = net.segments.find((s) => s.ns === b.namespace)
          const lane = net.lanes[seg?.avenue ?? 0]
          const x = seg ? seg.x0 : net.westX + 1.2
          out.push({ family, ns: b.namespace, keys: [gk, rk], live: false, sign: [x, lane.main],
            points: dedupe([[gate.x + 0.3, gate.z], [net.westX, gate.z], [net.westX, lane.main], [x, lane.main]]) })
          continue
        }
        if (b.kind !== 'Service') continue
        const sk = `${b.namespace}/${b.service}`
        const relay = net.relays.get(sk)
        if (!relay) continue
        const family: Family = b.mirror ? 'mirror' : 'main'
        const w = b.mirror ? undefined : b.weight
        const id = `${family}|${gateName}|${sk}`
        const prev = mains.get(id)
        if (prev) {
          if (!prev.keys.includes(rk)) prev.keys.push(rk)
          prev.weight = mergeWeight(prev.weight, w)
          if (prev.weight === undefined) delete prev.weight
          prev.live = family === 'main' && prev.weight !== 0
          continue
        }
        const lane = net.lanes[relay.avenue]
        mains.set(id, {
          family, ns: relay.ns, live: family === 'main' && w !== 0, keys: [gk, `service:${sk}`, rk],
          ...(w !== undefined ? { weight: w } : {}),
          points: dedupe([[gate.x + 0.3, gate.z], [net.westX, gate.z], [net.westX, lane.main], [relay.x, lane.main], [relay.x, relay.z]]),
        })
      }
    }
  }
  out.push(...mains.values())
```

Remplacer `pathOf` par :

```ts
/**
 * Chemin d'un objet (« type:clé ») : ce qui s'allume quand on le sélectionne.
 * Porte, Gateway ou route → Services → pods → volumes (une route n'entraîne
 * pas les autres routes de ses lignes) ; Service → portes, pods → volumes ;
 * pod → Services → portes, et volumes ; volume → pods → Services ; PV
 * orphelin → lui seul. Un Gateway allume sa porte (« gate:ns/name »).
 */
export function pathOf(sel: string, links: Link[]): Set<string> {
  const out = new Set([sel])
  const via = (from: Set<string>, fams: Family[], keep = (_k: string) => true) => {
    const added = new Set<string>()
    for (const l of links)
      if (fams.includes(l.family) && l.keys.some((k) => from.has(k)))
        for (const k of l.keys) if (keep(k) && !out.has(k)) { out.add(k); added.add(k) }
    return added
  }
  const only = (s: Set<string>, type: string) => new Set([...s].filter((k) => typeOf(k) === type))
  const fromGate = (start: Set<string>, isRoute: boolean) => {
    // Une ligne principale est partagée par les routes vers un même Service :
    // d'une route, on ne prend que ses objets, pas les routes sœurs.
    const first = via(start, FROM_GATE, (k) => !isRoute || typeOf(k) !== 'route')
    via(only(via(only(first, 'service'), ['fibre']), 'pod'), ['data'])
  }
  const self = new Set([sel])
  switch (typeOf(sel)) {
    case 'gate':
      fromGate(self, false)
      break
    case 'route':
      fromGate(self, true)
      break
    case 'gateway': {
      const gate = `gate:${sel.slice(sel.indexOf(':') + 1)}`
      out.add(gate)
      fromGate(new Set([gate]), false)
      break
    }
    case 'service':
      via(self, TO_SERVICE)
      via(only(via(self, ['fibre']), 'pod'), ['data'])
      break
    case 'pod':
      via(only(via(self, ['fibre']), 'service'), TO_SERVICE)
      via(self, ['data'])
      break
    case 'volume':
      via(only(via(self, ['data']), 'pod'), ['fibre'])
      break
    // pv : citerne vide, sans lien.
  }
  return out
}
```

`isLit` est inchangé.

- [ ] **Step 4 : vérifier**

Run : `cd web && npx vitest run src/scene/links.test.ts src/scene/world.test.ts && npx tsc --noEmit`
Attendu : PASS (tests du jalon 8 compris : clés des lignes principales dans le même ordre, une route cassée par porte, route et namespace).

- [ ] **Step 5 : commit**

```bash
git add web/src/scene/links.ts web/src/scene/links.test.ts
git commit -m "feat(web): lignes par porte, routes refusées, miroirs et poids

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 21 : le monde dérivé connaît Gateways et PV

**Files:**
- Modify: `web/src/scene/world.ts`
- Test: `web/src/scene/world.test.ts`

- [ ] **Step 1 : test qui échoue**

Dans `web/src/scene/world.test.ts`, compléter l'import des fixtures :

```ts
import { gateway, node, pod, pv, route, service, volume } from '../store/fixtures'
```

et ajouter dans `describe('réseau et stockage', …)` :

```ts
  it('ajoute les portes Gateway et les citernes vides', () => {
    const w = new World()
    w.update({
      ...st(1),
      gateways: new Map([['infra/public', gateway()], ['kube-system/sys', gateway({ namespace: 'kube-system', name: 'sys' })]]),
      persistentVolumes: new Map([['pv-1', pv()]]),
    })
    expect([...w.net!.gates.keys()]).toEqual(['infra/public', 'nginx']) // kube-system masqué
    expect(w.gates.find((g) => g.name === 'infra/public')!.gateway?.class).toBe('eg')
    expect(w.net!.orphans.map((o) => o.key)).toEqual(['pv-1'])
    expect(w.positionOf('gateway', 'infra/public')).toEqual(w.positionOf('gate', 'infra/public'))
    expect(w.positionOf('pv', 'pv-1')).toEqual(expect.objectContaining({ x: expect.any(Number) }))
    expect(w.positionOf('pv', 'absent')).toBeNull()
    const f = w.focusFor({ type: 'gateway', key: 'infra/public', name: 'public' }, null, null)
    expect(f.dim).toBe(true)
    expect(f.path!.has('gate:infra/public')).toBe(true)
    expect(w.focusFor({ type: 'pv', key: 'pv-1', name: 'pv-1' }, null, null).dim).toBe(true)
  })

  it('redispose quand un PV orphelin apparaît', () => {
    const w = new World()
    w.update(st(1))
    const before = w.net
    w.update({ ...st(2), persistentVolumes: new Map([['pv-1', pv()]]) })
    expect(w.net).not.toBe(before)
    expect(w.net!.orphans).toHaveLength(1)
  })
```

- [ ] **Step 2 : vérifier l'échec**

Run : `cd web && npx vitest run src/scene/world.test.ts`
Attendu : FAIL (`gateways`, `persistentVolumes` ignorés ; `positionOf('gateway', …)` renvoie `undefined`).

- [ ] **Step 3 : implémentation**

Dans `web/src/scene/world.ts` :

Import :

```ts
import {
  gatewayKey, pvKey, routeKey, serviceKey, volumeKey,
  type Gateway, type Node, type PersistentVolume, type Pod, type Route, type Service, type Volume,
} from '../api/types'
```

`WorldInput` :

```ts
type WorldInput = Pick<ClusterState, 'version' | 'nodes' | 'pods' | 'namespaces'>
  & Partial<Pick<ClusterState, 'services' | 'routes' | 'volumes' | 'gateways' | 'persistentVolumes'>>
  & { podView?: PodView; nsFilter?: string | null }
```

Sélections réseau :

```ts
const NET_SELECTIONS: ReadonlySet<SelectionType> = new Set(['service', 'route', 'volume', 'gate', 'gateway', 'pv'])
```

Dans `World`, après `volumes: Volume[] = []` :

```ts
  /** PV sans PVC (cluster-scoped : jamais filtrés par namespace). */
  persistentVolumes: PersistentVolume[] = []
```

Dans `placeNetwork`, remplacer les lignes de `this.services = …` à `this.gates = …` par :

```ts
    this.services = [...(st.services?.values() ?? [])].filter((s) => shown(s.namespace))
    this.routes = [...(st.routes?.values() ?? [])].filter((r) => shown(r.namespace))
    this.volumes = [...(st.volumes?.values() ?? [])].filter((v) => shown(v.namespace))
    this.persistentVolumes = [...(st.persistentVolumes?.values() ?? [])]
    const gateways = new Map<string, Gateway>()
    for (const g of st.gateways?.values() ?? []) if (shown(g.namespace)) gateways.set(gatewayKey(g), g)
    this.gates = gatesOf(this.routes, gateways)
```

La clé de disposition et l'appel à `layoutNetwork` deviennent :

```ts
    const key = [
      this.services.map(serviceKey).sort().join(','),
      this.gates.map((g) => g.name).join(','),
      this.volumes.map((v) => `${volumeKey(v)}@${v.storageClass}@${v.requested}`).sort().join(','),
      this.persistentVolumes.map((p) => `${pvKey(p)}@${p.storageClass}@${p.capacity}`).sort().join(','),
    ].join('|')
    if (key !== this.netKey) {
      this.netKey = key
      this.net = layoutNetwork(city,
        this.services.map((s) => ({ key: serviceKey(s), namespace: s.namespace, name: s.name })),
        this.gates.map((g) => g.name),
        this.volumes.map((v) => ({ key: volumeKey(v), namespace: v.namespace, name: v.name, storageClass: v.storageClass, requested: v.requested })),
        this.persistentVolumes.map((p) => ({ key: pvKey(p), name: p.name, storageClass: p.storageClass, capacity: p.capacity })))
      // La ville s'agrandit des entrepôts (cadrage de la caméra, sol, arbres).
      this.layout = { ...city, bounds: this.net.bounds }
    }
```

Dans `positionOf`, ajouter avant `case 'node'` :

```ts
      case 'gateway': return at(net?.gates.get(key))
      case 'pv': return at(net?.orphans.find((o) => o.key === key))
```

- [ ] **Step 4 : vérifier**

Run : `cd web && npx vitest run src/scene && npx tsc --noEmit`
Attendu : PASS.

- [ ] **Step 5 : commit**

```bash
git add web/src/scene/world.ts web/src/scene/world.test.ts
git commit -m "feat(web): le monde dispose les portes Gateway et les citernes vides

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 22 : portes Gateway et citernes vides dans la scène

**Files:**
- Modify: `web/src/scene/Network.tsx`
- Modify: `web/src/scene/Selection.tsx`

Pas de test unitaire (rendu three.js) : la vérification passe par `tsc`, le mode démo et l'e2e de la phase E.

- [ ] **Step 1 : pièces et matériaux**

Dans `web/src/scene/Network.tsx` :

Imports :

```ts
import { pvKey, serviceKey, volumeKey, type PersistentVolume, type Service } from '../api/types'
import { useCluster } from '../store/cluster'
import { gateSignal, healthSignal, pvSignal, volumeSignal, worst, type Signal } from './health'
```

Commentaire d'en-tête :

```ts
// Infrastructure de la ville : relais (Services) sur les avenues, portes
// (contrôleurs d'entrée et Gateways) à l'ouest, citernes (PVC) et citernes
// vides (PV sans PVC) dans les entrepôts. Une InstancedMesh par pièce :
// quelques draw calls quel que soit le nombre d'objets. Vu de loin, un relais
// par tronçon (pire voyant du groupe) avec un compteur.
```

Dans `GEOMETRY`, après `tankCap` :

```ts
  // Citerne vide : cylindre ouvert dessiné en fil de fer.
  orphanBody: up(new THREE.CylinderGeometry(1, 1, 1, 16, 2, true), 1),
```

Après `GLOW` :

```ts
/** Pièces en fil de fer (citernes vides). */
const WIRE: ReadonlySet<PartName> = new Set(['orphanBody'])
const PICKABLE: PartName[] = ['relayRing', 'relayBase', 'signPanel', 'gatePost', 'gateLintel', 'tankBody', 'orphanBody']
```

(supprimer l'ancienne déclaration de `PICKABLE`).

Dans `Network`, les matériaux :

```ts
  const materials = useMemo(() => ({
    solid: opacityMaterial({ color: '#ffffff', roughness: 0.55, metalness: 0.1 }),
    glow: opacityBasicMaterial({ color: '#ffffff' }),
    wire: opacityBasicMaterial({ color: '#ffffff', wireframe: true }),
  }), [])
```

et leur libération :

```ts
  useEffect(() => () => {
    parts.current.forEach((p) => p.dispose())
    materials.solid.dispose()
    materials.glow.dispose()
    materials.wire.dispose()
  }, [materials])
```

Dans `ensure`, le choix du matériau devient :

```ts
    const materialOf = (name: PartName) => (WIRE.has(name) ? materials.wire : GLOW.has(name) ? materials.glow : materials.solid)
    parts.current = new Map(NAMES.map((name) => [name, new Part(GEOMETRY[name], materialOf(name),
      capacity.current, { opacity: true, name, castShadow: !GLOW.has(name) && !WIRE.has(name) })]))
```

Dans `useFrame`, la capacité :

```ts
    ensure(Math.max(1, (net?.relays.size ?? 0) + 2 * (net?.gates.size ?? 0) + (net?.tanks.size ?? 0) + (net?.orphans.length ?? 0)))
```

(les panneaux des PV Released partagent `signPost` et `signPanel` avec les ExternalName : au plus relais + orphelins, sous la capacité.)

- [ ] **Step 2 : portes Gateway**

Remplacer la boucle des portes dans `rebuild` par :

```ts
      // Portes : deux piliers et un linteau. Porte déduite : accent, orange si
      // une route est cassée ou refusée. Porte Gateway : son voyant (vert →
      // accent, orange, rouge, gris sans statut ou Gateway invisible).
      for (const g of world.gates) {
        const slot = net.gates.get(g.name)
        if (!slot) continue
        const key = `gate:${g.name}`
        // Un Gateway visible s'ouvre dans son propre inspecteur.
        const pick = g.gateway ? `gateway:${g.name}` : key
        const a = alpha(key, g.gateway?.namespace)
        const sig = gateSignal(g)
        c.copy(sig === 'ok' ? colors.accent : colors.signal[sig])
        if (key === selKey || pick === selKey) c.lerp(WHITE, 0.35)
        put('gatePost', slot.x, 0, slot.z - GATE_SPAN / 2, 1, 1, 1, c, a, pick)
        put('gatePost', slot.x, 0, slot.z + GATE_SPAN / 2, 1, 1, 1, c, a, pick)
        put('gateLintel', slot.x, 2.1, slot.z, 1, 1, GATE_SPAN + 0.35, c, a, pick)
      }
```

`alpha` teste `key` (« gate:… ») : le chemin d'un Service ou d'un Gateway sélectionné contient la porte sous cette forme (Task 20).

- [ ] **Step 3 : citernes vides**

Après la boucle des citernes, ajouter :

```ts
      // Citernes vides (PV sans PVC) : fil de fer gris, rouge si Failed ;
      // panneau gris devant une PV Released (données conservées, à réclamer).
      const pvs = new Map<string, PersistentVolume>(world.persistentVolumes.map((x) => [pvKey(x), x]))
      for (const o of net.orphans) {
        const pv = pvs.get(o.key)
        if (!pv) continue
        const key = `pv:${o.key}`
        const a = alpha(key, pv.claimRef?.split('/')[0])
        const color = key === selKey ? colors.accent : pvSignal(pv) === 'err' ? colors.signal.err : colors.muted
        put('orphanBody', o.x, 0, o.z, o.r, TANK_H, o.r, color, a, key)
        if (pv.phase === 'Released') {
          const z = o.z + o.r + 0.25
          put('signPost', o.x, 0, z, 1, 1, 1, colors.muted, a)
          put('signPanel', o.x, 0.9, z, 1, 1, 1, colors.muted, a, key)
        }
      }
```

Un PV sans `claimRef` n'a pas de namespace : `alpha` ne l'estompe pas sous un filtre de namespace, mais l'estompe hors du chemin d'une sélection.

- [ ] **Step 4 : marqueur de sélection**

Dans `web/src/scene/Selection.tsx`, la hauteur du marqueur devient :

```ts
        const h = sel.type === 'gate' || sel.type === 'gateway' || sel.type === 'route' ? 3.4
          : sel.type === 'volume' || sel.type === 'pv' ? 2.3 : 1.3
```

- [ ] **Step 5 : vérifier**

Run : `cd web && npx tsc --noEmit && npx vitest run`
Attendu : aucune erreur, tous les tests PASS.

Vérification visuelle (le simulateur de la phase B produit `infra/public`, `infra/internal` et trois PV) :
Run : `make demo` puis ouvrir http://localhost:8080
Attendu : deux portes libellées `infra/internal` (rouge) et `infra/public` (accent) parmi `nginx` et `traefik` ; dans les entrepôts, des citernes en fil de fer après les PVC, dont deux avec un panneau ; un clic sur `infra/public` pose le marqueur et allume ses lignes, un clic sur une citerne vide l'entoure du marqueur et estompe le reste.

- [ ] **Step 6 : commit**

```bash
git add web/src/scene/Network.tsx web/src/scene/Selection.tsx
git commit -m "feat(web): portes Gateway et citernes vides dans la ville

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 23 : miroirs, routes refusées et poids nuls au sol

**Files:**
- Modify: `web/src/scene/GroundLinks.tsx`
- Test: `web/src/scene/ribbon.test.ts`

- [ ] **Step 1 : test qui échoue**

Dans `web/src/scene/ribbon.test.ts`, remplacer l'import de `./GroundLinks` :

```ts
import { linkAlpha, ribbon, writeAlpha } from './GroundLinks'
import type { Link } from './links'
```

et ajouter :

```ts
describe('linkAlpha', () => {
  const l = (over: Partial<Link> = {}): Link => ({ family: 'main', points: [[0, 0], [1, 0]], keys: ['gate:nginx', 'service:production/api'], live: true, ns: 'production', ...over })
  const none = { path: null, hover: null, dim: false }

  it('estompe hors namespace filtré puis hors chemin', () => {
    expect(linkAlpha(l(), null, none)).toBe(1)
    expect(linkAlpha(l(), 'staging', none)).toBe(0.1)
    expect(linkAlpha(l(), null, { path: new Set(['gate:nginx']), hover: null, dim: true })).toBe(0.2)
    expect(linkAlpha(l(), null, { path: new Set(['gate:nginx', 'service:production/api']), hover: null, dim: true })).toBe(1)
  })

  it('atténue une ligne de poids 0 et les miroirs', () => {
    expect(linkAlpha(l({ weight: 0 }), null, none)).toBeCloseTo(0.35)
    expect(linkAlpha(l({ weight: 100 }), null, none)).toBe(1)
    expect(linkAlpha(l({ family: 'mirror', live: false }), null, none)).toBeCloseTo(0.6)
    expect(linkAlpha(l({ weight: 0 }), 'staging', none)).toBeCloseTo(0.035)
  })
})
```

- [ ] **Step 2 : vérifier l'échec**

Run : `cd web && npx vitest run src/scene/ribbon.test.ts`
Attendu : FAIL (`linkAlpha` non exporté).

- [ ] **Step 3 : implémentation**

Dans `web/src/scene/GroundLinks.tsx` :

Imports :

```ts
import { isLit, type Family, type Link, type Pt } from './links'
import { LOD_PX, questionTexture } from './Pods'
import type { Theme } from './theme'
import { tick } from './tick'
import { world, type Focus } from './world'
```

Constantes par famille :

```ts
const WIDTH: Record<Family, number> = { main: 0.12, broken: 0.1, refused: 0.1, mirror: 0.07, fibre: 0.08, data: 0.16 }
const Y: Record<Family, number> = { main: 0.03, broken: 0.031, refused: 0.031, mirror: 0.0305, fibre: 0.032, data: 0.033 }
/** Motif : période (unités monde), part allumée, intensité entre deux paquets, vitesse. */
const DASH: Record<Family, { period: number; duty: number; base: number; speed: number }> = {
  main: { period: 1.2, duty: 0.3, base: 0.45, speed: 1.6 },
  fibre: { period: 0.9, duty: 0.35, base: 0.5, speed: 1.4 },
  data: { period: 0.7, duty: 0.25, base: 0.2, speed: 0.8 },
  broken: { period: 0.5, duty: 0.55, base: 0, speed: 0 },
  refused: { period: 0.5, duty: 0.55, base: 0, speed: 0 },
  // Miroir : pointillé fin et immobile, jamais de paquets.
  mirror: { period: 0.35, duty: 0.5, base: 0, speed: 0 },
}
const FAMILIES: Family[] = ['main', 'mirror', 'broken', 'refused', 'fibre', 'data']
/** Familles toujours dessinées en motif plein (pointillés), sans dépendre de live. */
const DASHED: ReadonlySet<Family> = new Set(['broken', 'refused', 'mirror'])
```

Après `writeAlpha`, ajouter :

```ts
/**
 * Opacité d'un lien : 10 % hors du namespace filtré, 20 % hors du chemin d'une
 * sélection réseau ; un miroir est discret (60 %), une ligne de poids 0 pâle (35 %).
 */
export function linkAlpha(l: Link, nsFilter: string | null, focus: Focus): number {
  const base = nsFilter && l.ns !== nsFilter ? 0.1 : focus.dim && focus.path && !isLit(l, focus.path) ? 0.2 : 1
  return base * (l.family === 'mirror' ? 0.6 : l.weight === 0 ? 0.35 : 1)
}

/** Panneau « ⊘ » d'une route refusée par son Gateway. */
function refusedTexture(): THREE.Texture {
  const c = document.createElement('canvas')
  c.width = c.height = 64
  const g = c.getContext('2d')!
  g.fillStyle = '#C23E28'
  g.beginPath()
  g.arc(32, 32, 30, 0, Math.PI * 2)
  g.fill()
  g.strokeStyle = '#fff'
  g.lineWidth = 6
  g.beginPath()
  g.arc(32, 32, 16, 0, Math.PI * 2)
  g.moveTo(20.7, 43.3)
  g.lineTo(43.3, 20.7)
  g.stroke()
  const t = new THREE.CanvasTexture(c)
  t.colorSpace = THREE.SRGBColorSpace
  return t
}
```

Dans `Links`, la table des couleurs :

```ts
    const color: Record<Family, string> = {
      main: theme.fibre, mirror: theme.fibre, fibre: theme.fibre, data: theme.data, broken: theme.err, refused: theme.err,
    }
```

Les textures de panneaux :

```ts
  const question = useMemo(questionTexture, [])
  const refused = useMemo(refusedTexture, [])
  useEffect(() => () => { question.dispose(); refused.dispose() }, [question, refused])
```

Dans `useFrame`, remplacer la définition de `alphaOf` et de `setGeometry` par :

```ts
    const alphaOf = (l: Link) => linkAlpha(l, nsFilter, focus)
    const setGeometry = (f: Family, ls: Link[]) => {
      const mesh = meshes.get(f)!
      mesh.geometry.dispose()
      mesh.geometry = geometryOf(ribbon(ls.map((l) => ({ points: l.points, alpha: alphaOf(l), live: DASHED.has(l.family) || l.live })), WIDTH[f], Y[f]))
    }
```

Le rendu des panneaux :

```tsx
      {signs.map((l, i) => (
        <sprite key={`sign-${i}`} position={[l.sign![0], 0.6, l.sign![1]]} scale={[0.45, 0.45, 1]} raycast={() => null} renderOrder={10}>
          <spriteMaterial map={l.family === 'refused' ? refused : question} depthTest={false} transparent opacity={nsFilter && l.ns !== nsFilter ? 0.1 : 1} />
        </sprite>
      ))}
```

Les lignes de poids 0 ont `live: false` (Task 20) : le shader les dessine en trait plein et pâle, sans paquets ; `linkAlpha` les atténue en plus.

- [ ] **Step 4 : vérifier**

Run : `cd web && npx vitest run && npx tsc --noEmit`
Attendu : PASS, aucune erreur de type.

Vérification visuelle : `make demo`, http://localhost:8080. Attendu : depuis `infra/public`, deux lignes vers les relais de la canary `storefront` (celle à 10 % avec des paquets, comme celle à 90 %) ; la HTTPRoute refusée finit sur un panneau rouge « ⊘ » ; le miroir du TraefikService est un pointillé fin et immobile vers son relais ; avec `prefers-reduced-motion`, aucun paquet.

- [ ] **Step 5 : commit**

```bash
git add web/src/scene/GroundLinks.tsx web/src/scene/ribbon.test.ts
git commit -m "feat(web): miroirs, routes refusées et lignes de poids nul au sol

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

## Phase D — Interface

> **Noms de démo supposés** (à produire par le simulateur, phase B ; les tests e2e de la tâche 30 en dépendent) :
>
> | Objet | Clé | Détails |
> | --- | --- | --- |
> | Gateway | `infra/public` | classe `gke-l7-global-external-managed`, `accepted`/`programmed` = `true`, adresse `34.120.5.10`, listeners `http` (HTTP 80, ready `true`) et `https` (HTTPS 443, hôte `*.example.com`, ready `true`) |
> | Gateway | `infra/internal` | classe `gke-l7-rilb`, `accepted` = `true`, `programmed` = `false`, `reason` = `AddressNotAssigned`, `message` = `No address has been assigned to the Gateway`, listener `https` (HTTPS 443, ready `false`) |
> | HTTPRoute | `production/storefront` | gates `["infra/public"]`, hôte `shop.example.com`, chemin `/`, backends `frontend` (`weight` 900) et `frontend-canary` (`weight` 100, nouveau Service de la démo derrière le workload `frontend`) ; parent `infra/public` accepté |
> | GRPCRoute | `production/orders-grpc` | gates `["infra/internal"]`, `match` `orders.v1.Orders/PlaceOrder`, backend `orders-service` |
> | HTTPRoute (refusée) | `staging/preview` | gates `["infra/public"]`, hôte `preview.example.com`, backend `checkout-preview` à l'état `refused` ; parent `infra/public` `accepted` = `false`, `reason` = `NotAllowedByListeners` |
> | IngressRoute via TraefikService | `production/checkout` | porte `traefik`, `match` ``Host(`checkout.example.com`)`` ; TraefikService `production/checkout-split` (weighted : `api-gateway` poids 3, TraefikService `checkout-mirror` poids 1) ; `checkout-mirror` (mirroring : `orders-service`, miroir `payment-worker` à 10 %) ; règles publiées : `api-gateway` 750, `orders-service` 250, `payment-worker` `mirror` `percent` 10, toutes avec `via` = `production/checkout-split` |
> | IngressRouteTCP | `production/postgres` | porte `traefik`, `match` ``HostSNI(`*`)``, backend `postgres-payments` port 5432 |
> | IngressRouteUDP | `monitoring/statsd` | porte `traefik`, backend `prometheus` port 9125 |
> | PV | `pv-old-uploads` | `Released`, `standard-rwo`, 5 Gi, `Retain`, `claimRef` = `staging/old-uploads` |
> | PV | `pv-archive-2025` | `Released`, `standard-rwo`, 100 Gi, `Retain`, `claimRef` = `production/archive-2025` |
> | PV | `pv-spare-01` | `Available`, `premium-rwo`, 50 Gi, `Delete` |
>
> Le simulateur sert aussi le YAML des Gateways, des nouvelles routes et des PV (onglet YAML, `kind: PersistentVolume` attendu).

**Hypothèses sur les phases A à C** (contrats de l'en-tête) : `useCluster` porte `gateways` et `persistentVolumes` et sait sélectionner `'gateway'` et `'pv'` (nom affiché : `name` de l'objet) ; `gatesOf(routes, gateways)`, `routeBroken`, `routeRefused`, `isGatewayGate` existent dans `web/src/store/net.ts` ; `gateSignal(g: Gate)` et `pvSignal(p)` dans `web/src/scene/health.ts` ; `world.positionOf` accepte `'pv'` (citernes vides) et `world.pathFor` les clés `gate:<ns/nom>` ; la fabrique `route()` de `web/src/store/fixtures.ts` porte `gates: ['nginx']` ; le backend traduit le segment de namespace `_` en namespace vide (décision 10).

### Task 24 : appels de l'inspecteur et part du trafic

**Files:**
- Modify: `web/src/api/inspect.ts`, `web/src/ui/format.ts`
- Test: `web/src/api/inspect.test.ts`, `web/src/ui/format.test.ts`

- [ ] **Step 1 : fabriques de test**

Les fabriques `gateway()` et `pv()` existent depuis la Task 16 (phase C) dans `web/src/store/fixtures.ts`. Les tests de cette phase passent explicitement chaque champ qu'ils vérifient (le nom en particulier), ils ne dépendent donc pas des valeurs par défaut.

- [ ] **Step 2 : tests qui échouent**

Dans `web/src/api/inspect.test.ts`, étendre l'import (`gatewayRef, nsSeg, pvRef` depuis `./inspect`, `gateway, pv` depuis `../store/fixtures`) et ajouter :

```ts
describe('références du jalon 9', () => {
  it('désigne les routes Gateway API et Traefik TCP/UDP, les Gateways et les PV', () => {
    expect(routeRef(route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io' })))
      .toEqual({ group: 'gateway.networking.k8s.io', version: 'v1', kind: 'HTTPRoute', namespace: 'production', name: 'storefront' })
    expect(routeRef(route({ source: 'GRPCRoute', group: 'gateway.networking.k8s.io' })).kind).toBe('GRPCRoute')
    expect(routeRef(route({ source: 'IngressRouteTCP', group: 'traefik.containo.us' })))
      .toEqual(expect.objectContaining({ group: 'traefik.containo.us', version: 'v1alpha1', kind: 'IngressRouteTCP' }))
    expect(routeRef(route({ source: 'IngressRouteUDP', group: 'traefik.io' })).kind).toBe('IngressRouteUDP')
    expect(gatewayRef(gateway({ namespace: 'infra', name: 'public' })))
      .toEqual({ group: 'gateway.networking.k8s.io', version: 'v1', kind: 'Gateway', namespace: 'infra', name: 'public' })
    expect(pvRef(pv({ name: 'pv-1' }))).toEqual({ group: '', version: 'v1', kind: 'PersistentVolume', namespace: '', name: 'pv-1' })
    expect(['Gateway', 'HTTPRoute', 'GRPCRoute', 'IngressRouteTCP', 'IngressRouteUDP', 'PersistentVolume'].map(eventsResource))
      .toEqual(['gateways', 'httproutes', 'grpcroutes', 'ingressroutetcps', 'ingressrouteudps', 'persistentvolumes'])
  })

  it('remplace un namespace vide par « _ » dans les URL (objet cluster-scoped)', () => {
    expect(nsSeg('')).toBe('_')
    expect(nsSeg('production')).toBe('production')
    expect(nsSeg('a b')).toBe('a%20b')
  })
})
```

Dans `web/src/ui/format.test.ts` (import `fmtShare`) :

```ts
  it('formate une part du trafic en pour mille', () => {
    expect(fmtShare(900)).toBe('90 %')
    expect(fmtShare(1000)).toBe('100 %')
    expect(fmtShare(333)).toBe('33.3 %')
    expect(fmtShare(0)).toBe('0 %')
  })
```

(dans le `describe('format')` existant). Run (depuis `web/`) : `npx vitest run src/api/inspect.test.ts src/ui/format.test.ts`
Expected: FAIL (`gatewayRef`, `nsSeg`, `pvRef`, `fmtShare` introuvables).

- [ ] **Step 3 : implémentation**

Dans `web/src/api/inspect.ts`, import des types : `import type { ArgoInfo, Gateway, PersistentVolume, Route, Service, Volume } from './types'`. Remplacer `getEvents`, `routeRef` et `getYaml`, et ajouter `nsSeg`, `gatewayRef`, `pvRef` :

```ts
/** Segment de namespace d'une URL : « _ » pour un objet cluster-scoped (PV). */
export const nsSeg = (ns: string) => seg(ns || '_')

export const getEvents = (ns: string, name: string, resource = 'pods') =>
  getJSON<{ events: KubeEvent[] }>(`/api/namespaces/${nsSeg(ns)}/${seg(resource)}/${seg(name)}/events`).then((r) => r.events)

export const routeRef = (r: Pick<Route, 'source' | 'group' | 'namespace' | 'name'>): Ref => {
  switch (r.source) {
    case 'Ingress':
      return { group: 'networking.k8s.io', version: 'v1', kind: 'Ingress', namespace: r.namespace, name: r.name }
    case 'HTTPRoute':
    case 'GRPCRoute':
      return { group: 'gateway.networking.k8s.io', version: 'v1', kind: r.source, namespace: r.namespace, name: r.name }
    default: // IngressRoute, IngressRouteTCP, IngressRouteUDP : groupe Traefik de l'objet
      return { group: r.group, version: 'v1alpha1', kind: r.source, namespace: r.namespace, name: r.name }
  }
}

export const gatewayRef = (g: Pick<Gateway, 'namespace' | 'name'>): Ref =>
  ({ group: 'gateway.networking.k8s.io', version: 'v1', kind: 'Gateway', namespace: g.namespace, name: g.name })

export const pvRef = (p: Pick<PersistentVolume, 'name'>): Ref =>
  ({ group: '', version: 'v1', kind: 'PersistentVolume', namespace: '', name: p.name })

export const getYaml = (r: Ref) =>
  getJSON<YamlDoc>(`/api/yaml/${seg(r.group || 'core')}/${seg(r.version)}/${seg(r.kind)}/${nsSeg(r.namespace)}/${seg(r.name)}`)
```

`RESOURCES` et `eventsResource` restent inchangés : la règle par défaut (`kind` en minuscules + `s`) donne déjà `gateways`, `httproutes`, `grpcroutes`, `ingressroutetcps`, `ingressrouteudps` et `persistentvolumes`.

Dans `web/src/ui/format.ts` :

```ts
/** Part du trafic, en pour mille → « 90 % », « 33.3 % ». */
export function fmtShare(permille: number): string {
  return `${Math.round(permille) / 10} %`
}
```

- [ ] **Step 4 : vérifier**

Run (depuis `web/`) : `npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add web/src
git commit -m "feat(web): références des Gateways, routes Gateway API et PV ; part du trafic 

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 25 : inspecteur des Gateways, des nouvelles routes et des PV

**Files:**
- Modify: `web/src/inspector/NetOverview.tsx`, `web/src/inspector/Inspector.tsx`

- [ ] **Step 1 : aperçus**

Dans `web/src/inspector/NetOverview.tsx` :

1. Imports :

```tsx
import { useEffect, useState, type ReactNode } from 'react'
import { getEvents, type KubeEvent } from '../api/inspect'
import { routeKey, type Gateway, type PersistentVolume, type Route, type Service, type Tri, type Volume } from '../api/types'
import { clusterColors } from '../scene/colors'
import { gateSignal } from '../scene/health'
import { postureFor } from '../scene/posture'
import { useCluster } from '../store/cluster'
import { isGatewayGate, readyCount, routeBroken, routeRefused, routesTo, type Gate } from '../store/net'
import { fmtMem, fmtShare } from '../ui/format'
import { BADGE, goPod } from './common'
```

2. Remplacer `goGate` et `STATE`, ajouter `TRI` et `TriText` :

```tsx
/** Porte d'une route : l'inspecteur du Gateway s'il est visible, sinon la porte déduite. */
const goGate = (name: string) => () => {
  const st = useCluster.getState()
  st.select(st.gateways.has(name) ? { type: 'gateway', key: name } : { type: 'gate', key: name })
}

const STATE: Record<string, [string, string]> = {
  ok: ['', ''], missing: ['s-err', 'Service introuvable'], refused: ['s-err', 'Refusée'], indirect: ['s-mute', 'TraefikService'],
}

const TRI: Record<Tri, [string, string]> = { true: ['s-ok', 'oui'], false: ['s-err', 'non'], unknown: ['s-mute', 'inconnu'] }

function TriText({ v }: { v: Tri }) {
  const [cls, label] = TRI[v]
  return <span className={cls}>{label}</span>
}
```

3. Liste de routes partagée par les portes et les Gateways (remplace la liste écrite dans `GateOverview`) :

```tsx
function RouteList({ routes, testid }: { routes: Route[]; testid: string }) {
  if (!routes.length) return <p className="note">Aucune route visible ne s'attache à cette porte.</p>
  return (
    <ul className="podlist" data-testid={testid}>
      {routes.map((r) => (
        <li key={routeKey(r)}>
          <button onClick={() => useCluster.getState().select({ type: 'route', key: routeKey(r) })}>
            <span className="nm">{r.name}</span>
            <span className="later">{r.source} · {r.namespace}</span>
            {routeRefused(r) ? <span className="s-err">Refusée</span> : routeBroken(r) && <span className="s-err">Service introuvable</span>}
          </button>
        </li>
      ))}
    </ul>
  )
}
```

4. Dans `ServiceOverview`, la ligne de chaque route devient `<span className="later">{r.source} · porte {r.gates.join(', ')}</span>` et la note vide « Aucune route (Ingress, IngressRoute, HTTPRoute…) ne vise ce Service. ».

5. Remplacer `RouteOverview` :

```tsx
export function RouteOverview({ r }: { r: Route }) {
  const services = useCluster.getState().services
  const shares = r.rules.some((x) => x.backend.weight !== undefined || x.backend.mirror)
  const via = r.rules.some((x) => x.backend.via)
  return (
    <div className="p-body">
      <dl className="kv">
        <dt>{r.gates.length > 1 ? 'Portes' : 'Porte'}</dt>
        <dd>
          {r.gates.map((g, i) => (
            <span key={g}>{i > 0 && ', '}<button className="link" onClick={goGate(g)}>{g}</button></span>
          ))}
        </dd>
        <dt>Source</dt><dd>{r.source} ({r.group})</dd>
        {r.addresses?.length ? <><dt>Adresses</dt><dd>{r.addresses.join(', ')}</dd></> : null}
      </dl>
      {r.parents?.length ? (
        <>
          <h3>Gateways ({r.parents.length})</h3>
          <table className="evt" data-testid="parents">
            <thead>
              <tr><th scope="col">Gateway</th><th scope="col">Acceptée</th><th scope="col">Références résolues</th><th scope="col">Raison</th></tr>
            </thead>
            <tbody>
              {r.parents.map((p) => {
                const bad = p.accepted === 'false' || p.resolvedRefs === 'false'
                return (
                  <tr key={p.gateway} className={bad ? 'warning' : ''}>
                    <td><button className="link" onClick={goGate(p.gateway)}>{p.gateway}</button></td>
                    <td><TriText v={p.accepted} /></td>
                    <td><TriText v={p.resolvedRefs} /></td>
                    <td>{bad ? p.reason ?? '' : ''}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </>
      ) : null}
      <h3>Règles ({r.rules.length})</h3>
      <table className="evt" data-testid="rules">
        <thead>
          <tr>
            <th scope="col">Hôte · chemin</th><th scope="col">Backend</th>
            {shares && <th scope="col">Part</th>}
            {via && <th scope="col">Via</th>}
          </tr>
        </thead>
        <tbody>
          {r.rules.map((rule, i) => {
            const b = rule.backend
            const [cls, label] = STATE[b.state] ?? STATE.ok
            return (
              <tr key={i} className={b.state === 'missing' || b.state === 'refused' ? 'warning' : ''}>
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
                {shares && <td>{b.mirror ? `miroir ${b.percent ?? 100} %` : b.weight !== undefined ? fmtShare(b.weight) : '—'}</td>}
                {via && <td>{b.via ?? '—'}</td>}
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
```

6. Remplacer `GateOverview` et ajouter `gatewayBadge`, `GatewayOverview`, `PvOverview` :

```tsx
export function GateOverview({ g }: { g: Gate }) {
  return (
    <div className="p-body">
      <p className="note">
        {isGatewayGate(g.name)
          ? `Gateway « ${g.name} » : vous ne pouvez pas le lire, ou il n'existe plus ; son état est inconnu. Les routes ci-dessous s'y rattachent.`
          : `Contrôleur d'entrée « ${g.name} » : chaque route ci-dessous entre dans la ville par cette porte.`}
      </p>
      <RouteList routes={g.routes} testid="gate-routes" />
    </div>
  )
}

/** Badge d'un Gateway : non programmé, sans statut, ou listeners prêts (orange si une route ou un listener pèche). */
export function gatewayBadge(gw: Gateway, g: Gate): [string, string] {
  if (gw.programmed === 'false') return ['Non programmé', 's-err']
  if (gw.programmed === 'unknown') return ['Sans statut', 's-mute']
  const ready = gw.listeners.filter((l) => l.ready === 'true').length
  return [`${ready}/${gw.listeners.length} listeners prêts`, BADGE[gateSignal(g)]]
}

export function GatewayOverview({ gw, g }: { gw: Gateway; g: Gate }) {
  return (
    <div className="p-body">
      {gw.programmed === 'false' && (
        <p className="note s-err" data-testid="gateway-why"><b>{gw.reason || 'Non programmé'}</b>{gw.message ? ` : ${gw.message}` : ''}</p>
      )}
      {gw.programmed === 'unknown' && <p className="note">Aucun contrôleur n'a encore écrit l'état de ce Gateway.</p>}
      <dl className="kv">
        <dt>Classe</dt><dd>{gw.class}</dd>
        <dt>Accepté</dt><dd><TriText v={gw.accepted} /></dd>
        <dt>Programmé</dt><dd><TriText v={gw.programmed} />{gw.reason && gw.programmed !== 'false' ? ` (${gw.reason})` : ''}</dd>
        {gw.addresses?.length ? <><dt>Adresses</dt><dd>{gw.addresses.join(', ')}</dd></> : null}
      </dl>
      <h3>Listeners ({gw.listeners.length})</h3>
      <table className="evt" data-testid="listeners">
        <thead>
          <tr><th scope="col">Listener</th><th scope="col">Protocole · port</th><th scope="col">Hôte</th><th scope="col">Routes</th><th scope="col">Prêt</th></tr>
        </thead>
        <tbody>
          {gw.listeners.map((l) => (
            <tr key={l.name} className={l.ready === 'false' ? 'warning' : ''}>
              <td>{l.name}</td>
              <td>{l.protocol} · {l.port}</td>
              <td>{l.hostname || '*'}</td>
              <td>{l.attachedRoutes}</td>
              <td><TriText v={l.ready} /></td>
            </tr>
          ))}
        </tbody>
      </table>
      <h3>Routes ({g.routes.length})</h3>
      <RouteList routes={g.routes} testid="gateway-routes" />
    </div>
  )
}

const PV_NOTE: Record<PersistentVolume['phase'], string> = {
  Available: 'Disponible : aucun PVC ne le réclame.',
  Released: 'Libéré : son PVC a été supprimé. Avec la reclaim policy Retain, les données restent sur le disque jusqu’à la suppression du volume.',
  Failed: 'En échec : la récupération automatique du volume a échoué (voir les événements).',
  Bound: 'Lié à un PVC qui n’existe plus.',
}

export function PvOverview({ p }: { p: PersistentVolume }) {
  return (
    <div className="p-body">
      <p className={`note ${p.phase === 'Failed' ? 's-err' : ''}`} data-testid="pv-why">{PV_NOTE[p.phase]}</p>
      <dl className="kv">
        <dt>Classe</dt><dd>{p.storageClass || '(aucune)'}</dd>
        <dt>Capacité</dt><dd>{fmtMem(p.capacity)}</dd>
        <dt>Accès</dt><dd>{p.accessModes.join(', ') || '—'}</dd>
        <dt>Reclaim policy</dt><dd>{p.reclaimPolicy}</dd>
        {p.claimRef && <><dt>Ancien PVC</dt><dd>{p.claimRef}</dd></>}
      </dl>
    </div>
  )
}
```

- [ ] **Step 2 : panneau**

Dans `web/src/inspector/Inspector.tsx` :

1. Imports : `eventsResource, gatewayRef, pvRef, routeRef, serviceRef, volumeRef, type Ref` depuis `../api/inspect` ; `HEALTH_LABEL, healthSignal, pvSignal, volumeSignal` depuis `../scene/health` ; `gatesOf, readyCount, routeBroken, routeRefused` depuis `../store/net` ; `GateOverview, GatewayOverview, PvOverview, RouteOverview, ServiceOverview, VolumeOverview, gatewayBadge` depuis `./NetOverview`.
2. Dans `NetPanel`, après la déclaration de `gone`, ajouter :

```tsx
  const gatewayPanel = (key: string) => {
    const gw = st.gateways.get(key)
    if (!gw) return gone('Gateway', 'Ce Gateway a été supprimé.')
    const g = gatesOf(st.routes.values(), st.gateways).find((x) => x.name === key)!
    const [badge, badgeClass] = gatewayBadge(gw, g)
    return (
      <>
        <Head kind={`Gateway · ${gw.namespace}`} name={gw.name} badge={badge} badgeClass={badgeClass}
          color={colors.get(gw.namespace)} tabs={NET_TABS} onClose={onClose} />
        <NetBody target={gatewayRef(gw)}><GatewayOverview gw={gw} g={g} /></NetBody>
      </>
    )
  }
```

3. Remplacer les cas `route` et `gate`, et ajouter `gateway` et `pv` :

```tsx
    case 'route': {
      const r = st.routes.get(selection.key)
      if (!r) return gone('Route', 'Cette route a été supprimée.')
      const [badge, badgeClass] = routeRefused(r) ? ['Refusée', 's-err']
        : routeBroken(r) ? ['Service introuvable', 's-err']
        : [r.gates.length > 1 ? `${r.gates.length} portes` : `Porte ${r.gates[0]}`, 's-ok']
      return (
        <>
          <Head kind={`${r.source} · ${r.namespace}`} name={r.name} badge={badge} badgeClass={badgeClass}
            color={colors.get(r.namespace)} tabs={NET_TABS} onClose={onClose} />
          <NetBody target={routeRef(r)}><RouteOverview r={r} /></NetBody>
        </>
      )
    }
    case 'gateway':
      return gatewayPanel(selection.key)
    case 'gate': {
      // Porte d'un Gateway visible : même panneau que le Gateway lui-même.
      if (st.gateways.has(selection.key)) return gatewayPanel(selection.key)
      const g = gatesOf(st.routes.values(), st.gateways).find((x) => x.name === selection.key)
      if (!g) return gone("Porte d'entrée", 'Plus aucune route ne passe par cette porte.')
      const [badge, badgeClass] = g.refused ? [`${g.refused} route(s) refusée(s)`, 's-err']
        : g.broken ? [`${g.broken} route(s) cassée(s)`, 's-warn']
        : [`${g.routes.length} route(s)`, 's-ok']
      return (
        <>
          <Head kind={g.name.includes('/') ? 'Gateway (non visible)' : "Porte d'entrée"} name={g.name} badge={badge}
            badgeClass={badgeClass} tabs={[['overview', 'Aperçu']]} onClose={onClose} />
          <GateOverview g={g} />
        </>
      )
    }
    case 'pv': {
      const p = st.persistentVolumes.get(selection.key)
      if (!p) return gone('PersistentVolume', 'Ce volume a été supprimé ou lié à un PVC.')
      return (
        <>
          <Head kind="PersistentVolume" name={p.name} badge={p.phase} badgeClass={BADGE[pvSignal(p)]} tabs={NET_TABS} onClose={onClose} />
          <NetBody target={pvRef(p)}><PvOverview p={p} /></NetBody>
        </>
      )
    }
```

Le commentaire de `NetPanel` devient « Panneau d'un Service, d'une route, d'une porte, d'un Gateway, d'un PVC ou d'un PV. » `NetBody` passe déjà `target.namespace` (vide pour un PV) à `EventsTab`, et `getEvents` le traduit en `_`.

- [ ] **Step 3 : vérifier**

Run (depuis `web/`) : `npx tsc --noEmit && npx vitest run`
Expected: PASS. En démo (`make dev-demo`, http://localhost:5173) : `/gateways/infra/public` (après la tâche 28) montre « 2/2 listeners prêts », le tableau des listeners et la route `storefront` ; `/gateways/infra/internal` le badge rouge « Non programmé » et « AddressNotAssigned : No address has been assigned to the Gateway » ; la route `storefront` les parts « 90 % » et « 10 % » ; la route `checkout` les colonnes Part et Via, dont « miroir 10 % » ; le PV `pv-old-uploads` sa classe, « Retain » et « staging/old-uploads ».

- [ ] **Step 4 : commit**

```bash
git add web/src/inspector
git commit -m "feat(web): inspecteur des Gateways, des routes Gateway API et TCP/UDP, des PV ; parts du trafic

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 26 : vue Liste — Gateways et PV

**Files:**
- Modify: `web/src/ui/tree.ts`, `web/src/ui/ListView.tsx`
- Test: `web/src/ui/tree.test.ts`

- [ ] **Step 1 : test qui échoue**

Ajouter à `web/src/ui/tree.test.ts` (imports `gateway, pv` depuis `../store/fixtures`, en plus des existants) :

```ts
describe('Gateway API et PV', () => {
  const withGw = {
    ...st,
    routes: new Map([['HTTPRoute/production/storefront', route({
      source: 'HTTPRoute', group: 'gateway.networking.k8s.io', gate: 'infra/public', gates: ['infra/public', 'infra/internal'],
    })]]),
    gateways: new Map([
      ['infra/public', gateway({ namespace: 'infra', name: 'public', programmed: 'true' })],
      ['infra/internal', gateway({ namespace: 'infra', name: 'internal', programmed: 'false' })],
      ['infra/idle', gateway({ namespace: 'infra', name: 'idle', programmed: 'true' })],
    ]),
    volumes: new Map([['production/data-0', volume()]]),
    persistentVolumes: new Map([['pv-old-uploads', pv({ name: 'pv-old-uploads', storageClass: 'standard-rwo', phase: 'Released' })]]),
  }

  it('range les Gateways parmi les entrées, même sans route, et une route sous chacune de ses portes', () => {
    const gates = buildTree(withGw).find((g) => g.label === 'Entrées')!
    expect(gates.children!.map((g) => [g.label, g.select, g.status])).toEqual([
      ['infra/idle', { type: 'gateway', key: 'infra/idle' }, undefined],
      ['infra/internal', { type: 'gateway', key: 'infra/internal' }, 'non programmé'],
      ['infra/public', { type: 'gateway', key: 'infra/public' }, undefined],
    ])
    const ids = gates.children!.flatMap((g) => (g.children ?? []).map((r) => r.id))
    expect(ids).toHaveLength(2)
    expect(new Set(ids).size).toBe(2)
    expect(gates.children![2].children![0].select).toEqual({ type: 'route', key: 'HTTPRoute/production/storefront' })
  })

  it('range les PV orphelins avec les PVC de leur classe', () => {
    const storage = buildTree(withGw).find((g) => g.label === 'Stockage')!
    expect(storage.detail).toBe('2')
    const cls = storage.children![0]
    expect(cls.detail).toBe('1 PVC · 1 PV')
    expect(cls.children!.map((n) => [n.label, n.select, n.status])).toEqual([
      ['data-0', { type: 'volume', key: 'production/data-0' }, undefined],
      ['pv-old-uploads', { type: 'pv', key: 'pv-old-uploads' }, 'Released'],
    ])
  })
})
```

Run (depuis `web/`) : `npx vitest run src/ui/tree.test.ts`
Expected: FAIL.

- [ ] **Step 2 : implémentation**

Dans `web/src/ui/tree.ts` :

1. Imports : ajouter `type Gateway, type PersistentVolume` à l'import de `../api/types`, et remplacer l'import de `../store/net` par `import { gatesOf, readyCount, routeBroken, routeRefused } from '../store/net'`.
2. Signature : `buildTree(st: { pods; nodes; workloads; services?; routes?; volumes?; gateways?: ReadonlyMap<string, Gateway>; persistentVolumes?: ReadonlyMap<string, PersistentVolume> })` (mêmes types qu'aujourd'hui pour les champs existants).
3. Remplacer le bloc des entrées :

```ts
  const gates = gatesOf(st.routes?.values() ?? [], st.gateways)
  if (gates.length) roots.push({
    id: 'group:gates', label: 'Entrées', detail: `${gates.length}`,
    children: gates.map((g) => ({
      id: `gate:${g.name}`, label: g.name,
      detail: g.gateway ? `Gateway · ${g.gateway.class} · ${g.routes.length} routes` : `${g.routes.length} routes`,
      status: g.gateway?.programmed === 'false' ? 'non programmé' : g.refused ? 'route refusée' : g.broken ? 'route cassée' : undefined,
      select: g.gateway ? { type: 'gateway' as const, key: g.name } : { type: 'gate' as const, key: g.name },
      // Une route à plusieurs portes apparaît sous chacune : l'id porte la porte.
      children: g.routes.map((r) => ({
        id: `route:${routeKey(r)}@${g.name}`, label: r.name, detail: `${r.source} · ${r.namespace}`,
        status: routeRefused(r) ? 'route refusée' : routeBroken(r) ? 'Service introuvable' : undefined,
        select: { type: 'route' as const, key: routeKey(r) },
      })),
    })),
  })
```

4. Remplacer le bloc du stockage :

```ts
  const volumes = [...(st.volumes?.values() ?? [])]
  const pvs = [...(st.persistentVolumes?.values() ?? [])]
  if (volumes.length || pvs.length) {
    const byClass = new Map<string, TreeNode[]>()
    for (const v of volumes) pushTo(byClass, v.storageClass || '(aucune)', {
      id: `volume:${volumeKey(v)}`, label: v.name, detail: `${v.namespace} · ${fmtMem(v.requested)}`,
      status: v.phase !== 'Bound' ? v.phase : undefined, select: { type: 'volume' as const, key: volumeKey(v) },
    })
    for (const p of pvs) pushTo(byClass, p.storageClass || '(aucune)', {
      id: `pv:${p.name}`, label: p.name, detail: `PV · ${fmtMem(p.capacity)}${p.claimRef ? ` · ex-${p.claimRef}` : ''}`,
      status: p.phase, select: { type: 'pv' as const, key: p.name },
    })
    roots.push({
      id: 'group:storage', label: 'Stockage', detail: `${volumes.length + pvs.length}`,
      children: [...byClass].map(([c, items]) => {
        const nPv = items.filter((n) => n.select?.type === 'pv').length
        const nPvc = items.length - nPv
        return {
          id: `class:${c}`, label: c, detail: [nPvc && `${nPvc} PVC`, nPv && `${nPv} PV`].filter(Boolean).join(' · '),
          children: items.sort(byLabel),
        }
      }).sort(byLabel),
    })
  }
```

5. Le commentaire d'en-tête du fichier devient « … puis entrées (portes et Gateways) → routes, Services par namespace, PVC et PV orphelins par classe de stockage. »

Dans `web/src/ui/ListView.tsx`, compléter `NET_STATUS` :

```ts
const NET_STATUS: Record<string, string> = {
  down: 's-err', Lost: 's-err', Failed: 's-err', 'Service introuvable': 's-err', 'non programmé': 's-err', 'route refusée': 's-err',
  degraded: 's-warn', Pending: 's-warn', 'route cassée': 's-warn', Released: 's-mute', Available: 's-mute',
}
```

- [ ] **Step 3 : vérifier**

Run (depuis `web/`) : `npx tsc --noEmit && npx vitest run`
Expected: PASS (dont le test existant « ajoute Entrées, Services et Stockage », dont la classe `standard-rwo` garde le détail « 1 PVC »).

- [ ] **Step 4 : commit**

```bash
git add web/src/ui
git commit -m "feat(web): vue Liste avec les Gateways et les PV orphelins

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 27 : recherche et liens profonds

**Files:**
- Modify: `web/src/ui/searchRank.ts`, `web/src/ui/Search.tsx`, `web/src/ui/route.ts`
- Test: `web/src/ui/searchRank.test.ts`, `web/src/ui/route.test.ts`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `web/src/ui/searchRank.test.ts` (imports `gateway, pv` depuis `../store/fixtures`) :

```ts
describe('Gateway API et PV', () => {
  const gw = {
    pods: new Map(), nodes: new Map(), workloads: new Map(),
    routes: new Map([['HTTPRoute/production/storefront', route({
      source: 'HTTPRoute', group: 'gateway.networking.k8s.io', gate: 'infra/public', gates: ['infra/public'],
    })]]),
    gateways: new Map([['infra/public', gateway({ namespace: 'infra', name: 'public', class: 'gke-l7-global-external-managed' })]]),
    persistentVolumes: new Map([['pv-old-uploads', pv({ name: 'pv-old-uploads', phase: 'Released', storageClass: 'standard-rwo' })]]),
  }

  it('trouve un Gateway par son nom court, un PV, et nomme les portes d’une route', () => {
    expect(search('public', gw)[0]).toEqual(expect.objectContaining({
      type: 'gateway', key: 'infra/public', detail: 'Gateway · gke-l7-global-external-managed · 1 routes',
    }))
    expect(search('pv-old', gw)[0]).toEqual(expect.objectContaining({ type: 'pv', key: 'pv-old-uploads', detail: 'PV · Released · standard-rwo' }))
    expect(search('storefront', gw)[0].detail).toBe('HTTPRoute · production · porte infra/public')
  })
})
```

Dans `web/src/ui/route.test.ts`, remplacer la ligne `['/routes/httproute/a/b', null],` du tableau `it.each` par :

```ts
    ['/routes/httproute/production/storefront', { type: 'route', source: 'HTTPRoute', namespace: 'production', name: 'storefront' }],
    ['/routes/grpcroute/production/orders-grpc', { type: 'route', source: 'GRPCRoute', namespace: 'production', name: 'orders-grpc' }],
    ['/routes/ingressroutetcp/production/postgres', { type: 'route', source: 'IngressRouteTCP', namespace: 'production', name: 'postgres' }],
    ['/routes/ingressrouteudp/monitoring/statsd', { type: 'route', source: 'IngressRouteUDP', namespace: 'monitoring', name: 'statsd' }],
    ['/routes/tlsroute/a/b', null],
    ['/gateways/infra/public', { type: 'gateway', namespace: 'infra', name: 'public' }],
    ['/persistentvolumes/pv-old-uploads', { type: 'pv', name: 'pv-old-uploads' }],
    ['/gates/infra%2Fpublic', { type: 'gate', name: 'infra/public' }],
```

et ajouter dans `describe('syncRoute')` (imports `gateway, pv, route` depuis `../store/fixtures`) :

```ts
  it('un lien /gates/<ns>%2F<nom> vers un Gateway visible ouvre le Gateway et réécrit l’URL', () => {
    const w = fakeWindow('/gates/infra%2Fpublic')
    const stop = syncRoute(w)
    useCluster.getState().applyMessages([{
      type: 'snapshot', rev: 1, gateways: [gateway({ namespace: 'infra', name: 'public' })],
      routes: [route({ source: 'HTTPRoute', group: 'gateway.networking.k8s.io', gate: 'infra/public', gates: ['infra/public'] })],
    }])
    expect(useCluster.getState().selection).toMatchObject({ type: 'gateway', key: 'infra/public' })
    expect(w.location.pathname).toBe('/gateways/infra/public')
    stop()
  })

  it('rétablit un PV partagé par lien', () => {
    const w = fakeWindow('/persistentvolumes/pv-old-uploads')
    const stop = syncRoute(w)
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, persistentVolumes: [pv({ name: 'pv-old-uploads' })] }])
    expect(useCluster.getState().selection).toMatchObject({ type: 'pv', key: 'pv-old-uploads' })
    useCluster.getState().select({ type: 'gate', key: 'traefik' })
    expect(w.location.pathname).toBe('/gates/traefik')
    stop()
  })
```

Run (depuis `web/`) : `npx vitest run src/ui`
Expected: FAIL.

- [ ] **Step 2 : recherche**

Dans `web/src/ui/searchRank.ts` :

1. Imports : ajouter `type Gateway, type PersistentVolume` à l'import de `../api/types` ; remplacer `gatesOf` par `gatesOf` dans l'import de `../store/net`. Le commentaire d'en-tête devient « Recherche (/) parmi les pods, nodes, workloads, Services, routes, portes, Gateways, PVC et PV visibles par l'utilisateur. »
2. `SearchResult.type` : `'pod' | 'node' | 'workload' | 'service' | 'route' | 'volume' | 'gate' | 'gateway' | 'pv'`.
3. Paramètre `st` : ajouter `gateways?: ReadonlyMap<string, Gateway>; persistentVolumes?: ReadonlyMap<string, PersistentVolume>`.
4. Remplacer la boucle des portes et la ligne de détail des routes, puis ajouter les PV avant le tri final :

```ts
  const routes = [...(st.routes?.values() ?? [])]
  for (const g of gatesOf(routes, st.gateways)) {
    const ss = [score(g.name, 0), g.gateway ? score(g.gateway.name, 0) : -1].filter((x) => x >= 0)
    if (!ss.length) continue
    scored.push([Math.min(...ss), g.gateway
      ? { type: 'gateway', key: g.name, label: g.name, detail: `Gateway · ${g.gateway.class} · ${g.routes.length} routes` }
      : { type: 'gate', key: g.name, label: g.name, detail: `Porte · ${g.routes.length} routes` }])
  }
```

```ts
    if (ss.length) scored.push([Math.min(...ss), { type: 'route', key: routeKey(r), label: r.name, detail: `${r.source} · ${r.namespace} · porte ${r.gates.join(', ')}` }])
```

```ts
  for (const p of st.persistentVolumes?.values() ?? []) {
    const s = score(p.name, 1)
    if (s >= 0) scored.push([s, { type: 'pv', key: p.name, label: p.name, detail: `PV · ${p.phase} · ${p.storageClass || '(aucune)'}` }])
  }
```

Dans `web/src/ui/Search.tsx`, `positionOf` :

```tsx
function positionOf(r: SearchResult): { x: number; z: number } | null {
  if (r.type === 'workload') return r.podUid ? world.positionOf('pod', r.podUid) : null
  if (r.type === 'gateway') return world.positionOf('gate', r.key) // un Gateway est dessiné en porte
  return world.positionOf(r.type, r.key)
}
```

(`choose` sélectionne déjà `{ type: r.type, key: r.key }` hors workload : `gateway` et `pv` sont des `SelectionType`.) Libellés : `aria-label="Rechercher un pod, un node, un workload, un Service, une route, un Gateway, un PVC ou un PV"`, `placeholder="Pod, node, Service, route, Gateway, PV…"`.

- [ ] **Step 3 : liens profonds**

Remplacer `web/src/ui/route.ts` :

```ts
// Liens profonds : /pods/{ns}/{nom}, /nodes/{nom}, /services/{ns}/{nom},
// /volumes/{ns}/{nom}, /routes/{source}/{ns}/{nom}, /gates/{nom},
// /gateways/{ns}/{nom}, /persistentvolumes/{nom}. L'URL suit la sélection (sans
// recharger la page) et une sélection partagée par lien est rétablie dès que
// l'objet arrive dans le flux. /gates/{ns}%2F{nom} désigne la porte d'un
// Gateway : visible, il s'ouvre et l'URL devient /gateways/{ns}/{nom}.

import type { RouteSource } from '../api/types'
import { useCluster, type ClusterState, type Selection, type SelectionType } from '../store/cluster'
import { gatesOf, isGatewayGate } from '../store/net'

export type Route =
  | { type: 'pod'; namespace: string; name: string }
  | { type: 'node'; name: string }
  | { type: 'service'; namespace: string; name: string }
  | { type: 'volume'; namespace: string; name: string }
  | { type: 'route'; source: RouteSource; namespace: string; name: string }
  | { type: 'gate'; name: string }
  | { type: 'gateway'; namespace: string; name: string }
  | { type: 'pv'; name: string }
  | null

const SOURCES: Record<string, RouteSource> = {
  ingress: 'Ingress', ingressroute: 'IngressRoute', ingressroutetcp: 'IngressRouteTCP', ingressrouteudp: 'IngressRouteUDP',
  httproute: 'HTTPRoute', grpcroute: 'GRPCRoute',
}

export function parseRoute(pathname: string): Route {
  let parts: string[]
  try {
    parts = pathname.split('/').filter(Boolean).map(decodeURIComponent)
  } catch {
    return null // encodage invalide (« %E0%A4 ») : pas de lien profond
  }
  const [head, ...rest] = parts
  if (head === 'pods' && rest.length === 2) return { type: 'pod', namespace: rest[0], name: rest[1] }
  if (head === 'nodes' && rest.length === 1) return { type: 'node', name: rest[0] }
  if (head === 'services' && rest.length === 2) return { type: 'service', namespace: rest[0], name: rest[1] }
  if (head === 'volumes' && rest.length === 2) return { type: 'volume', namespace: rest[0], name: rest[1] }
  if (head === 'routes' && rest.length === 3 && SOURCES[rest[0]]) return { type: 'route', source: SOURCES[rest[0]], namespace: rest[1], name: rest[2] }
  if (head === 'gates' && rest.length === 1) return { type: 'gate', name: rest[0] }
  if (head === 'gateways' && rest.length === 2) return { type: 'gateway', namespace: rest[0], name: rest[1] }
  if (head === 'persistentvolumes' && rest.length === 1) return { type: 'pv', name: rest[0] }
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
    case 'gateway': return `/gateways/${e(r.namespace)}/${e(r.name)}`
    case 'pv': return `/persistentvolumes/${e(r.name)}`
  }
}

/** Route d'un Gateway désigné par sa clé « ns/nom ». */
function gatewayRoute(key: string): Route {
  const i = key.indexOf('/')
  return { type: 'gateway', namespace: key.slice(0, i), name: key.slice(i + 1) }
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
      if (isGatewayGate(r.name) && st.gateways.has(r.name)) return { type: 'gateway', key: r.name }
      return gatesOf(st.routes.values(), st.gateways).some((g) => g.name === r.name) ? { type: 'gate', key: r.name } : null
    case 'gateway': {
      const key = `${r.namespace}/${r.name}`
      return st.gateways.has(key) ? { type: 'gateway', key } : null
    }
    case 'pv':
      return st.persistentVolumes.has(r.name) ? { type: 'pv', key: r.name } : null
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
    case 'gate': return st.gateways.has(sel.key) ? gatewayRoute(sel.key) : { type: 'gate', name: sel.key }
    case 'gateway': return gatewayRoute(sel.key)
    case 'pv': return { type: 'pv', name: sel.key }
    case 'service':
    case 'volume': {
      const [namespace, name] = sel.key.split('/')
      return { type: sel.type, namespace, name }
    }
    case 'route': {
      const [source, namespace, name] = sel.key.split('/')
      return { type: 'route', source: source as RouteSource, namespace, name }
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
    if (pending) {
      if (!sel) return // lien profond pas encore rétabli : on ne touche pas à l'URL
      pending = null // l'utilisateur a choisi autre chose : l'URL le suit, le lien est abandonné
    }
    const path = pathFor(routeFor(sel, useCluster.getState()))
    if (path !== win.location.pathname) win.history.replaceState(null, '', path)
  })
  tryRestore()
  return () => { unsubVersion(); unsubSel() }
}
```

- [ ] **Step 4 : vérifier**

Run (depuis `web/`) : `npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 5 : commit**

```bash
git add web/src/ui
git commit -m "feat(web): recherche et liens profonds des Gateways, des routes Gateway API et TCP/UDP, des PV

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 28 : résumé du chemin et répartition du trafic

**Files:**
- Modify: `web/src/ui/PathSummary.tsx`
- Test: `web/src/ui/PathSummary.test.ts`

- [ ] **Step 1 : tests qui échouent**

Ajouter à `web/src/ui/PathSummary.test.ts` (imports `gateway, pv` depuis `../store/fixtures`, `splitText` depuis `./PathSummary`) :

```ts
describe('splitText', () => {
  it('dit la répartition pondérée et les miroirs d’une route, une fois par Service', () => {
    const r = route({
      rules: [
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'frontend', kind: 'Service', state: 'ok', weight: 900 } },
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'frontend-canary', kind: 'Service', state: 'ok', weight: 100 } },
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'audit', kind: 'Service', state: 'ok', mirror: true, percent: 10 } },
        { host: 'www.example.com', path: '/', backend: { namespace: 'production', service: 'frontend', kind: 'Service', state: 'ok', weight: 900 } },
      ],
    })
    expect(splitText(r)).toBe('Répartition : frontend 90 %, frontend-canary 10 %, miroir audit 10 %')
    expect(splitText(route())).toBe('')
  })
})

describe('summaryText du jalon 9', () => {
  beforeEach(() => useCluster.getState().reset())

  it('ajoute la répartition au chemin d’une route pondérée', () => {
    const r = route({
      source: 'HTTPRoute', group: 'gateway.networking.k8s.io', gate: 'infra/public', gates: ['infra/public'],
      rules: [
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'api', kind: 'Service', state: 'ok', weight: 900 } },
        { host: 'shop.example.com', path: '/', backend: { namespace: 'production', service: 'api-canary', kind: 'Service', state: 'missing', weight: 100 } },
      ],
    })
    useCluster.getState().applyMessages([{
      type: 'snapshot', rev: 1, nodes: [node()], pods: [pod()], services: [service()], routes: [r],
      gateways: [gateway({ namespace: 'infra', name: 'public' })],
    }])
    const st = useCluster.getState()
    expect(summaryText({ type: 'route', key: 'HTTPRoute/production/storefront', name: 'storefront' }, st))
      .toMatch(/^Chemin : .* · Répartition : api 90 %, api-canary 10 %$/)
    expect(summaryText({ type: 'gateway', key: 'infra/public', name: 'public' }, st)).toMatch(/^Chemin : 1 porte/)
  })

  it('vide pour un PV (aucun lien) ou un Gateway disparu', () => {
    useCluster.getState().applyMessages([{ type: 'snapshot', rev: 1, persistentVolumes: [pv({ name: 'pv-old-uploads' })] }])
    const st = useCluster.getState()
    expect(summaryText({ type: 'pv', key: 'pv-old-uploads', name: 'pv-old-uploads' }, st)).toBe('')
    expect(summaryText({ type: 'gateway', key: 'infra/gone', name: 'gone' }, st)).toBe('')
  })
})
```

Run (depuis `web/`) : `npx vitest run src/ui/PathSummary.test.ts`
Expected: FAIL.

- [ ] **Step 2 : implémentation**

Remplacer `web/src/ui/PathSummary.tsx` :

```tsx
import type { Route } from '../api/types'
import { world } from '../scene/world'
import { useCluster, type ClusterState, type Selection } from '../store/cluster'
import { gatesOf } from '../store/net'
import { fmtShare } from './format'

const LABELS: [string, string, string][] = [['gate', 'porte', 'portes'], ['service', 'Service', 'Services'], ['pod', 'pod', 'pods'], ['volume', 'PVC', 'PVC']]

/** L'objet sélectionné existe-t-il encore dans le flux ? (un PV n'a pas de chemin) */
function exists(sel: NonNullable<Selection>, st: ClusterState): boolean {
  switch (sel.type) {
    case 'service': return st.services.has(sel.key)
    case 'volume': return st.volumes.has(sel.key)
    case 'route': return st.routes.has(sel.key)
    case 'gateway': return st.gateways.has(sel.key)
    case 'gate': return gatesOf(st.routes.values(), st.gateways).some((g) => g.name === sel.key)
    default: return false
  }
}

/** Répartition du trafic d'une route (parts et miroirs), un Service une seule fois ; vide sans poids. */
export function splitText(r: Pick<Route, 'rules'>): string {
  const parts = new Set<string>()
  for (const { backend: b } of r.rules) {
    if (b.mirror) parts.add(`miroir ${b.service} ${b.percent ?? 100} %`)
    else if (b.weight !== undefined) parts.add(`${b.service} ${fmtShare(b.weight)}`)
  }
  return parts.size ? `Répartition : ${[...parts].join(', ')}` : ''
}

/** Texte du résumé, vide sans sélection d'une porte, d'un Gateway, d'une route, d'un Service ou d'un PVC. */
export function summaryText(sel: Selection, st: ClusterState): string {
  if (!sel || sel.type === 'pod' || sel.type === 'node' || sel.type === 'pv' || !exists(sel, st)) return ''
  world.update(st)
  // Un Gateway est dessiné en porte : son chemin part de « gate:ns/nom ».
  const path = world.pathFor(sel.type === 'gateway' ? `gate:${sel.key}` : `${sel.type}:${sel.key}`)
  const parts = LABELS.map(([type, one, many]) => {
    let n = 0
    for (const k of path) if (k.startsWith(`${type}:`)) n++
    return n ? `${n} ${n === 1 ? one : many}` : ''
  }).filter(Boolean)
  const route = sel.type === 'route' ? st.routes.get(sel.key) : undefined
  const split = route ? splitText(route) : ''
  return `Chemin : ${parts.join(' · ') || 'aucun lien'}${split ? ` · ${split}` : ''}`
}

/**
 * Résumé du chemin allumé par la sélection d'une porte, d'un Gateway, d'une
 * route, d'un Service ou d'un PVC : ce que la ville montre, dit aussi en texte
 * (lecteurs d'écran, tests), avec la répartition du trafic d'une route pondérée.
 * La région live reste montée, seul son texte change.
 */
export function PathSummary() {
  const selection = useCluster((s) => s.selection)
  useCluster((s) => s.version)
  const text = summaryText(selection, useCluster.getState())
  return <div className="path-summary" role="status" data-testid="path-summary" data-empty={text ? undefined : ''}>{text}</div>
}
```

- [ ] **Step 3 : vérifier**

Run (depuis `web/`) : `npx tsc --noEmit && npx vitest run`
Expected: PASS.

- [ ] **Step 4 : commit**

```bash
git add web/src/ui
git commit -m "feat(web): répartition du trafic dans le résumé du chemin

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Phase E — Intégration, banc et documentation

### Task 29 : e2e en mode démo

**Files:**
- Create: `web/e2e/gateway.spec.ts`

- [ ] **Step 1 : tests**

Créer `web/e2e/gateway.spec.ts` :

```ts
import { expect, test, type Page } from '@playwright/test'

// Suite du réseau et du stockage en mode démo : Gateways (programmé, non
// programmé), HTTPRoute pondérée, route refusée, TraefikService pondéré avec
// miroir, IngressRouteTCP, PV orphelin, liens profonds et vue Liste.

const panel = (page: Page) => page.locator('aside.panel')
const editorText = (page: Page) =>
  page.getByTestId('yaml-editor').locator('.view-lines').innerText().then((t) => t.replace(/ /g, ' '))

function collectProblems(page: Page) {
  const problems: string[] = []
  page.on('console', (m) => { if (m.type() === 'error') problems.push(m.text()) })
  page.on('pageerror', (e) => problems.push(e.message))
  return problems
}

test('Gateway programmé : listeners, puis sa HTTPRoute pondérée 90/10', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/gateways/infra/public')
  const p = panel(page)
  await expect(p.getByText('Gateway · infra')).toBeVisible()
  await expect(p.locator('.badge')).toHaveText('2/2 listeners prêts')
  await expect(p.getByTestId('listeners')).toContainText('HTTPS · 443')
  await expect(page.getByTestId('path-summary')).toContainText('1 porte')

  await p.getByTestId('gateway-routes').getByRole('button', { name: /storefront/ }).click()
  await expect(page).toHaveURL(/\/routes\/httproute\/production\/storefront$/)
  await expect(p.getByTestId('parents')).toContainText('infra/public')
  await expect(p.getByTestId('rules')).toContainText('90 %')
  await expect(p.getByTestId('rules')).toContainText('10 %')
  await expect(page.getByTestId('path-summary')).toContainText('Répartition : frontend 90 %, frontend-canary 10 %')
  await page.waitForTimeout(1500)
  await page.screenshot({ path: 'e2e/__screenshots__/gateway-route.png' })

  await p.getByRole('tab', { name: 'YAML' }).click()
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toContain('kind: HTTPRoute')
  expect(problems).toEqual([])
})

test('Gateway non programmé : badge rouge et raison', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/gateways/infra/internal')
  const p = panel(page)
  await expect(p.locator('.badge.s-err')).toHaveText('Non programmé')
  await expect(p.getByTestId('gateway-why')).toContainText('AddressNotAssigned')
  await expect(p.getByTestId('gateway-routes')).toContainText('orders-grpc')
  expect(problems).toEqual([])
})

test('lien /gates/infra%2Fpublic : ouvre le Gateway et réécrit l’URL', async ({ page }) => {
  await page.goto('/gates/infra%2Fpublic')
  await expect(panel(page).getByText('Gateway · infra')).toBeVisible()
  await expect(page).toHaveURL(/\/gateways\/infra\/public$/)
})

test('route refusée par son Gateway', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/routes/httproute/staging/preview')
  const p = panel(page)
  await expect(p.locator('.badge.s-err')).toHaveText('Refusée')
  await expect(p.getByTestId('parents')).toContainText('NotAllowedByListeners')
  await expect(p.getByTestId('rules')).toContainText('Refusée')
  expect(problems).toEqual([])
})

test('IngressRoute via un TraefikService pondéré avec miroir', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/routes/ingressroute/production/checkout')
  const rules = panel(page).getByTestId('rules')
  await expect(rules).toContainText('75 %')
  await expect(rules).toContainText('25 %')
  await expect(rules).toContainText('miroir 10 %')
  await expect(rules).toContainText('production/checkout-split')
  expect(problems).toEqual([])
})

test('IngressRouteTCP : porte traefik et match HostSNI', async ({ page }) => {
  await page.goto('/routes/ingressroutetcp/production/postgres')
  const p = panel(page)
  await expect(p.getByText('IngressRouteTCP · production')).toBeVisible()
  await expect(p.getByTestId('rules')).toContainText('HostSNI')
  await expect(p.getByTestId('rules')).toContainText('postgres-payments')
})

test('PV libéré : inspectable, YAML compris', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/persistentvolumes/pv-old-uploads')
  const p = panel(page)
  await expect(p.getByText('PersistentVolume', { exact: true })).toBeVisible()
  await expect(p.locator('.badge')).toHaveText('Released')
  await expect(p.getByTestId('pv-why')).toContainText('Libéré')
  await expect(p).toContainText('staging/old-uploads')
  await p.getByRole('tab', { name: 'YAML' }).click()
  await expect.poll(() => editorText(page), { timeout: 15_000 }).toContain('kind: PersistentVolume')
  expect(problems).toEqual([])
})

test('vue Liste : Gateways dans les entrées, PV dans le stockage', async ({ page }) => {
  const problems = collectProblems(page)
  await page.goto('/')
  await page.getByRole('button', { name: 'Liste' }).click()
  const tree = page.getByRole('tree', { name: 'Cluster' })
  await tree.getByRole('treeitem', { name: /^Entrées/ }).click()
  await expect(tree.getByRole('treeitem', { name: /^infra\/public/ })).toBeVisible()
  await expect(tree.getByRole('treeitem', { name: /^infra\/internal.*non programmé/ })).toBeVisible()
  await tree.getByRole('treeitem', { name: /^Stockage/ }).click()
  await tree.getByRole('treeitem', { name: /^standard-rwo/ }).click()
  await tree.getByRole('treeitem', { name: /^pv-old-uploads/ }).click()
  await expect(panel(page).getByText('PersistentVolume', { exact: true })).toBeVisible()
  expect(problems).toEqual([])
})

test('recherche d’un Gateway par son nom court', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByTestId('pods-running')).toHaveText(/\d+\/\d+/)
  await page.keyboard.press('/')
  await page.getByRole('combobox', { name: /Rechercher/ }).fill('internal')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/gateways\/infra\/internal$/)
})
```

- [ ] **Step 2 : vérifier**

Run (depuis la racine) : `make e2e`
Expected: tous les tests Playwright passent, anciens compris (`demo`, `inspector`, `actions`, `finition`, `network`). Le test « porte traefik » de `network.spec.ts` garde sa route `admin` : la porte `traefik` compte désormais aussi `checkout`, `postgres` et `statsd`.

- [ ] **Step 3 : commit**

```bash
git add web/e2e
git commit -m "test(e2e): Gateways, routes pondérées et refusées, TraefikService, PV orphelin en mode démo

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 30 : CRD épinglées et scénarios kind

Choix : les manifestes de CRD (Gateway API canal standard, environ 600 Ko ; Traefik, environ 400 Ko) ne sont pas versionnés dans le dépôt mais **téléchargés une fois, à une version épinglée dans le `Makefile`, dans `hack/crds/.cache/`** (ignoré par git). Le dépôt reste léger, la version est fixée et revue dans le diff du `Makefile`, et le test d'intégration réapplique un fichier local (pas de réseau pendant le test, CRD identiques d'un passage à l'autre).

**Files:**
- Create: `hack/scenarios/30-storage.yaml`, `hack/scenarios-gateway/gateways.yaml`, `hack/scenarios-gateway/status.sh`
- Modify: `hack/scenarios-traefik/ingressroute.yaml`, `hack/dev-rbac.yaml`, `Makefile`, `.gitignore`, `web/e2e/auth.spec.ts`

- [ ] **Step 1 : CRD épinglées**

Dans `.gitignore`, ajouter `hack/crds/.cache/`.

Dans le `Makefile`, ajouter `crds` à `.PHONY`, puis remplacer la définition de `TRAEFIK_CRD` et la cible `scenarios` :

```make
# CRD tierces des scénarios, téléchargées une fois à une version épinglée
# (hack/crds/.cache/, ignoré par git) ; le test d'intégration les réapplique.
GATEWAY_API_VERSION ?= v1.3.0
TRAEFIK_CRD_VERSION ?= v3.5
GATEWAY_API_CRDS := hack/crds/.cache/gateway-api-$(GATEWAY_API_VERSION)-standard.yaml
TRAEFIK_CRDS := hack/crds/.cache/traefik-$(TRAEFIK_CRD_VERSION)-crds.yaml

$(GATEWAY_API_CRDS):
	mkdir -p $(dir $@)
	curl -fsSL -o $@ https://github.com/kubernetes-sigs/gateway-api/releases/download/$(GATEWAY_API_VERSION)/standard-install.yaml

# https://doc.traefik.io/traefik/reference/install-configuration/providers/kubernetes/kubernetes-crd/
$(TRAEFIK_CRDS):
	mkdir -p $(dir $@)
	curl -fsSL -o $@ https://raw.githubusercontent.com/traefik/traefik/$(TRAEFIK_CRD_VERSION)/docs/content/reference/dynamic-configuration/kubernetes-crd-definition-v1.yml

crds: $(GATEWAY_API_CRDS) $(TRAEFIK_CRDS)

scenarios: crds
	kubectl --context $(KIND_CTX) apply -f hack/scenarios/
	kubectl --context $(KIND_CTX) apply --server-side -f $(TRAEFIK_CRDS)
	kubectl --context $(KIND_CTX) apply --server-side -f $(GATEWAY_API_CRDS)
	kubectl --context $(KIND_CTX) wait --for condition=established --timeout=60s \
		crd/ingressroutes.traefik.io crd/ingressroutetcps.traefik.io crd/ingressrouteudps.traefik.io crd/traefikservices.traefik.io \
		crd/gateways.gateway.networking.k8s.io crd/httproutes.gateway.networking.k8s.io crd/grpcroutes.gateway.networking.k8s.io
	kubectl --context $(KIND_CTX) apply -f hack/scenarios-traefik/
	kubectl --context $(KIND_CTX) apply -f hack/scenarios-gateway/gateways.yaml
	hack/scenarios-gateway/status.sh $(KIND_CTX)
```

Run : `make crds && ls -l hack/crds/.cache/ && git status --short hack`
Expected: deux fichiers YAML non vides ; `git status` ne les montre pas.

- [ ] **Step 2 : PV sans PVC**

Créer `hack/scenarios/30-storage.yaml` :

```yaml
# PersistentVolumes sans PVC (jalon 9) : un disponible, un libéré. Le second
# réclame un PVC qui n'existe pas (claimRef avec un UID) : le contrôleur de PV
# le passe en Released et, avec Retain, le garde. Classe « manual » : le
# provisionneur de kind ne s'en occupe pas.
apiVersion: v1
kind: PersistentVolume
metadata: { name: pv-spare-01 }
spec:
  capacity: { storage: 2Gi }
  accessModes: [ReadWriteOnce]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: manual
  hostPath: { path: /tmp/atlas-pv-spare-01 }
---
apiVersion: v1
kind: PersistentVolume
metadata: { name: pv-old-uploads }
spec:
  capacity: { storage: 5Gi }
  accessModes: [ReadWriteOnce]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: manual
  claimRef: { namespace: staging, name: old-uploads, uid: 00000000-0000-0000-0000-000000000001 }
  hostPath: { path: /tmp/atlas-pv-old-uploads }
```

- [ ] **Step 3 : Traefik complet**

Ajouter à la fin de `hack/scenarios-traefik/ingressroute.yaml` :

```yaml
---
# TraefikService pondéré (3:1) dont une branche est un miroir : la route
# checkout se résout en api-gateway 75 %, payment-worker 25 % et un miroir
# de 10 % vers postgres-payments.
apiVersion: traefik.io/v1alpha1
kind: TraefikService
metadata: { name: checkout-split, namespace: production }
spec:
  weighted:
    services:
      - { name: api-gateway, port: 80, weight: 3 }
      - { name: checkout-mirror, kind: TraefikService, weight: 1 }
---
apiVersion: traefik.io/v1alpha1
kind: TraefikService
metadata: { name: checkout-mirror, namespace: production }
spec:
  mirroring:
    name: payment-worker
    port: 8080
    mirrors:
      - { name: postgres-payments, port: 5432, percent: 10 }
---
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata: { name: checkout, namespace: production }
spec:
  entryPoints: [web]
  routes:
    - match: Host(`checkout.localtest.me`)
      kind: Rule
      services: [{ name: checkout-split, kind: TraefikService }]
---
apiVersion: traefik.io/v1alpha1
kind: IngressRouteTCP
metadata: { name: postgres, namespace: production }
spec:
  entryPoints: [postgres]
  routes:
    - match: HostSNI(`*`)
      services: [{ name: postgres-payments, port: 5432 }]
---
# Dans kube-system : bob ne doit pas la recevoir.
apiVersion: traefik.io/v1alpha1
kind: IngressRouteUDP
metadata: { name: dns, namespace: kube-system }
spec:
  entryPoints: [dns]
  routes:
    - services: [{ name: kube-dns, port: 53 }]
```

Le commentaire d'en-tête du fichier devient « IngressRoute, IngressRouteTCP/UDP et TraefikService (CRD installées par make scenarios, sans contrôleur) : la porte « traefik » apparaît ; la route vers grafana est cassée (pas de Service grafana dans les scénarios). »

- [ ] **Step 4 : Gateway API**

Créer `hack/scenarios-gateway/gateways.yaml` :

```yaml
# Gateway API (CRD du canal standard installées par make scenarios, sans
# contrôleur : status.sh écrit à la main ce qu'un contrôleur publierait).
# infra/public est programmé, infra/internal ne l'est pas, kube-system/platform
# sert à vérifier que bob ne le reçoit pas. storefront est pondérée 90/10,
# preview est refusée.
apiVersion: v1
kind: Namespace
metadata: { name: infra }
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata: { name: atlas-scenarios }
spec: { controllerName: example.com/atlas-scenarios }
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: public, namespace: infra }
spec:
  gatewayClassName: atlas-scenarios
  listeners:
    - { name: http, protocol: HTTP, port: 80, allowedRoutes: { namespaces: { from: All } } }
    - name: https
      protocol: HTTPS
      port: 443
      hostname: "*.localtest.me"
      tls: { mode: Terminate, certificateRefs: [{ name: localtest-tls }] }
      allowedRoutes: { namespaces: { from: All } }
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: internal, namespace: infra }
spec:
  gatewayClassName: atlas-scenarios
  listeners:
    - { name: http, protocol: HTTP, port: 8080, allowedRoutes: { namespaces: { from: All } } }
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: platform, namespace: kube-system }
spec:
  gatewayClassName: atlas-scenarios
  listeners:
    - { name: http, protocol: HTTP, port: 80 }
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: storefront, namespace: production }
spec:
  parentRefs: [{ name: public, namespace: infra }]
  hostnames: [shop.localtest.me]
  rules:
    - matches: [{ path: { type: PathPrefix, value: / } }]
      backendRefs:
        - { name: api-gateway, port: 80, weight: 90 }
        - { name: payment-worker, port: 8080, weight: 10 }
---
apiVersion: gateway.networking.k8s.io/v1
kind: GRPCRoute
metadata: { name: orders-grpc, namespace: production }
spec:
  parentRefs: [{ name: internal, namespace: infra }]
  rules:
    - matches: [{ method: { service: orders.v1.Orders, method: PlaceOrder } }]
      backendRefs: [{ name: payment-worker, port: 8080 }]
---
apiVersion: v1
kind: Service
metadata: { name: preview, namespace: staging }
spec:
  selector: { app: preview }
  ports: [{ name: http, port: 80 }]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: preview, namespace: staging }
spec:
  parentRefs: [{ name: public, namespace: infra, sectionName: https }]
  hostnames: [preview.example.com]
  rules:
    - backendRefs: [{ name: preview, port: 80 }]
```

Créer `hack/scenarios-gateway/status.sh` (exécutable : `chmod +x`) :

```sh
#!/usr/bin/env sh
# Statut des objets Gateway API des scénarios : kind n'a pas de contrôleur
# Gateway, on écrit à la main ce qu'il publierait (sous-ressource status).
set -eu
CTX="${1:-kind-atlas}"
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
k() { kubectl --context "$CTX" "$@"; }

# cond TYPE STATUS RAISON [MESSAGE]
cond() {
  printf '{"type":"%s","status":"%s","reason":"%s","message":"%s","lastTransitionTime":"%s","observedGeneration":1}' "$1" "$2" "$3" "${4:-}" "$NOW"
}
# listener NOM ROUTES STATUS RAISON
listener() {
  printf '{"name":"%s","supportedKinds":[{"group":"gateway.networking.k8s.io","kind":"HTTPRoute"},{"group":"gateway.networking.k8s.io","kind":"GRPCRoute"}],"attachedRoutes":%s,"conditions":[%s]}' \
    "$1" "$2" "$(cond Programmed "$3" "$4")"
}
# parent NS NOM ACCEPTED RAISON
parent() {
  printf '{"parentRef":{"group":"gateway.networking.k8s.io","kind":"Gateway","namespace":"%s","name":"%s"},"controllerName":"example.com/atlas-scenarios","conditions":[%s,%s]}' \
    "$1" "$2" "$(cond Accepted "$3" "$4")" "$(cond ResolvedRefs True ResolvedRefs)"
}

k -n infra patch gateway public --subresource=status --type=merge -p "{\"status\":{
  \"addresses\":[{\"type\":\"IPAddress\",\"value\":\"172.18.0.100\"}],
  \"conditions\":[$(cond Accepted True Accepted),$(cond Programmed True Programmed)],
  \"listeners\":[$(listener http 1 True Programmed),$(listener https 0 True Programmed)]}}"
k -n infra patch gateway internal --subresource=status --type=merge -p "{\"status\":{
  \"conditions\":[$(cond Accepted True Accepted),$(cond Programmed False AddressNotAssigned 'No address has been assigned to the Gateway')],
  \"listeners\":[$(listener http 1 False Pending)]}}"
k -n kube-system patch gateway platform --subresource=status --type=merge -p "{\"status\":{
  \"conditions\":[$(cond Accepted True Accepted),$(cond Programmed True Programmed)],
  \"listeners\":[$(listener http 0 True Programmed)]}}"
k -n production patch httproute storefront --subresource=status --type=merge -p "{\"status\":{\"parents\":[$(parent infra public True Accepted)]}}"
k -n staging patch httproute preview --subresource=status --type=merge -p "{\"status\":{\"parents\":[$(parent infra public False NotAllowedByListeners)]}}"
echo "statut Gateway API écrit"
```

- [ ] **Step 5 : droits de dev**

Dans `hack/dev-rbac.yaml`, le ClusterRole `atlas-dev-node-reader` couvre aussi les PV (cluster-scoped, absents de `view`) :

```yaml
rules:
  - apiGroups: [""]
    resources: [nodes, persistentvolumes]
    verbs: [get, list, watch]
```

(son commentaire devient « « view » ne couvre ni les nodes ni les PV (ressources cluster-scoped) : alice reçoit en plus leur lecture ; bob, sans ce droit, voit des bâtiments anonymes et aucun PV. »), puis ajouter à la fin :

```yaml
---
# view et edit ignorent la Gateway API et Traefik (CRD sans rôles agrégés) :
# ce rôle les y agrège, pour qu'alice et bob les lisent dans leurs namespaces.
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: atlas-dev-gateway-view
  labels:
    rbac.authorization.k8s.io/aggregate-to-view: "true"
    rbac.authorization.k8s.io/aggregate-to-edit: "true"
rules:
  - apiGroups: [gateway.networking.k8s.io]
    resources: [gateways, httproutes, grpcroutes]
    verbs: [get, list, watch]
  - apiGroups: [traefik.io, traefik.containo.us]
    resources: [ingressroutes, ingressroutetcps, ingressrouteudps, traefikservices]
    verbs: [get, list, watch]
```

Dans `web/e2e/auth.spec.ts` :
- test d'alice, après l'attente de `"namespace":"kube-system"` :

```ts
  await expect.poll(() => stream()).toContain('"persistentVolumes":[{')
  await expect.poll(() => stream()).toContain('"source":"HTTPRoute"')
```

- test de bob, après `expect(frames).not.toContain('"kind":"node"')` :

```ts
  // Ni PV (lecture cluster-scoped), ni Gateway ou route de kube-system (couvert
  // par l'absence de « kube-system ») ; la HTTPRoute de production, oui.
  expect(frames).not.toContain('"persistentVolumes":[{')
  expect(frames).not.toContain('"kind":"persistentVolume"')
  expect(frames).toContain('"source":"HTTPRoute"')
```

- [ ] **Step 6 : vérifier**

Run : `make scenarios`, puis `kubectl --context kind-atlas get gateway,httproute,grpcroute -A`, `kubectl --context kind-atlas get traefikservice,ingressroutetcp,ingressrouteudp -A` et `kubectl --context kind-atlas get pv pv-spare-01 pv-old-uploads`
Expected: `infra/public` `PROGRAMMED True`, `infra/internal` `PROGRAMMED False`, `kube-system/platform` ; `storefront`, `orders-grpc`, `preview` ; `checkout-split`, `checkout-mirror`, `postgres`, `dns` ; `pv-spare-01` `Available`, `pv-old-uploads` `Released`. `make scenarios` relancé une seconde fois passe aussi (tout est idempotent).

Run : `make run-kind`, puis http://localhost:8080/gateways/infra/internal
Expected: badge « Non programmé », raison `AddressNotAssigned`.

Run (cluster kind avec Dex, voir README) : `kubectl --context kind-atlas apply -f hack/dev-rbac.yaml && make e2e-auth`
Expected: PASS.

- [ ] **Step 7 : commit**

```bash
git add .gitignore Makefile hack web/e2e/auth.spec.ts
git commit -m "test: scénarios kind Gateway API, Traefik complet et PV orphelins ; CRD épinglées en cache

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 31 : test d'intégration — CRD installées après Atlas

**Files:**
- Modify: `internal/kube/live_test.go`, `Makefile` (cible `test-integration`)

- [ ] **Step 1 : cible**

Dans le `Makefile`, remplacer la cible `test-integration` :

```make
test-integration: embed-dir crds
	GATEWAY_API_CRDS=$(abspath $(GATEWAY_API_CRDS)) TRAEFIK_CRDS=$(abspath $(TRAEFIK_CRDS)) \
		go test -tags integration -count=1 -v ./internal/kube -run Live
```

- [ ] **Step 2 : aides**

Dans `internal/kube/live_test.go`, imports supplémentaires `"os/exec"` et `"strings"`. Extraire le contexte :

```go
// liveContext : contexte kube des tests d'intégration (ATLAS_CONTEXT, kind-atlas par défaut).
func liveContext() string {
	if c := os.Getenv("ATLAS_CONTEXT"); c != "" {
		return c
	}
	return "kind-atlas"
}
```

et, dans `startLive`, remplacer les cinq premières lignes (lecture d'`ATLAS_CONTEXT`) par `rc, err := RestConfig("", liveContext())`. Ajouter :

```go
// kubectl lance kubectl sur le contexte des tests ; stdin : manifeste éventuel (« -f - »).
func kubectl(t *testing.T, stdin string, args ...string) {
	t.Helper()
	cmd := exec.Command("kubectl", append([]string{"--context", liveContext()}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kubectl %s : %v\n%s", strings.Join(args, " "), err, out)
	}
}

// restore : commande kubectl de nettoyage, sans échec du test.
func restore(args ...string) {
	_ = exec.Command("kubectl", append([]string{"--context", liveContext()}, args...)...).Run()
}

// manifest : manifeste de CRD mis en cache par make crds (chemin dans la variable env).
func manifest(t *testing.T, env string) string {
	t.Helper()
	p := os.Getenv(env)
	if p == "" {
		t.Skipf("%s absent : lancer par make test-integration", env)
	}
	return p
}

// isRoute : message de type typ (upsert, delete) pour une route donnée, qui satisfait ok si présent.
func isRoute(typ, source, ns, name string, ok func(model.Route) bool) func(stream.Message) bool {
	return func(m stream.Message) bool {
		r, is := m.Obj.(model.Route)
		return m.Type == typ && is && r.Source == source && r.Namespace == ns && r.Name == name && (ok == nil || ok(r))
	}
}

// backends : backends d'une route par nom de Service.
func backends(r model.Route) map[string]model.Backend {
	out := map[string]model.Backend{}
	for _, x := range r.Rules {
		out[x.Backend.Service] = x.Backend
	}
	return out
}
```

- [ ] **Step 3 : Gateway API installée après Atlas**

```go
const gatewayFixture = `
apiVersion: v1
kind: Namespace
metadata: { name: atlas-it-gw }
---
apiVersion: v1
kind: Service
metadata: { name: web, namespace: atlas-it-gw }
spec: { selector: { app: web }, ports: [{ port: 80 }] }
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: gw, namespace: atlas-it-gw }
spec:
  gatewayClassName: atlas-it
  listeners: [{ name: http, protocol: HTTP, port: 80 }]
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: web, namespace: atlas-it-gw }
spec:
  parentRefs: [{ name: gw }]
  rules:
    - backendRefs:
        - { name: web, port: 80, weight: 90 }
        - { name: web-canary, port: 80, weight: 10 }
`

const gatewayProgrammed = `{"status":{"conditions":[` +
	`{"type":"Accepted","status":"True","reason":"Accepted","message":"","lastTransitionTime":"2026-10-07T00:00:00Z","observedGeneration":1},` +
	`{"type":"Programmed","status":"True","reason":"Programmed","message":"","lastTransitionTime":"2026-10-07T00:00:00Z","observedGeneration":1}]}}`

// TestLiveGatewayCRDHot : Atlas démarre sans la Gateway API ; une HTTPRoute
// créée après l'installation des CRD est diffusée en moins de 5 s, le statut
// écrit sur son Gateway aussi, et supprimer la CRD retire la route du flux.
func TestLiveGatewayCRDHot(t *testing.T) {
	crds := manifest(t, "GATEWAY_API_CRDS")
	// Retire les CRD (et les objets des scénarios), remis en place à la fin.
	kubectl(t, "", "delete", "--ignore-not-found", "--wait=true", "-f", crds)
	t.Cleanup(func() {
		restore("delete", "namespace", "atlas-it-gw", "--ignore-not-found")
		restore("apply", "--server-side", "-f", crds)
		restore("wait", "--for", "condition=established", "--timeout=60s",
			"crd/gateways.gateway.networking.k8s.io", "crd/httproutes.gateway.networking.k8s.io", "crd/grpcroutes.gateway.networking.k8s.io")
		restore("apply", "-f", "../../hack/scenarios-gateway/gateways.yaml")
		_ = exec.Command("../../hack/scenarios-gateway/status.sh", liveContext()).Run()
	})
	_, sub, _ := startLive(t)

	kubectl(t, "", "apply", "--server-side", "-f", crds)
	kubectl(t, "", "wait", "--for", "condition=established", "--timeout=60s",
		"crd/gateways.gateway.networking.k8s.io", "crd/httproutes.gateway.networking.k8s.io")
	start := time.Now()
	kubectl(t, gatewayFixture, "apply", "-f", "-")
	_ = waitFor(t, sub, "HTTPRoute après l'installation des CRD", isRoute("upsert", model.SourceHTTPRoute, "atlas-it-gw", "web", func(r model.Route) bool {
		b := backends(r)
		return len(r.Gates) == 1 && r.Gates[0] == "atlas-it-gw/gw" &&
			w(b["web"]) == 900 && w(b["web-canary"]) == 100 && b["web-canary"].State == model.BackendMissing
	}))
	seen := time.Since(start)
	t.Logf("HTTPRoute visible %v après sa création (CRD installées après le démarrage)", seen)
	if seen > 5*time.Second {
		t.Errorf("HTTPRoute visible en %v (> 5 s)", seen)
	}

	// Pas de contrôleur dans kind : le statut est écrit à la main.
	kubectl(t, "", "-n", "atlas-it-gw", "patch", "gateway", "gw", "--subresource=status", "--type=merge", "-p", gatewayProgrammed)
	_ = waitFor(t, sub, "Gateway programmé", func(m stream.Message) bool {
		g, ok := m.Obj.(model.Gateway)
		return m.Type == "upsert" && ok && g.Namespace == "atlas-it-gw" && g.Name == "gw" && g.Programmed == model.CondTrue
	})

	start = time.Now()
	kubectl(t, "", "delete", "crd", "httproutes.gateway.networking.k8s.io", "--wait=true")
	_ = waitFor(t, sub, "route retirée avec sa CRD", isRoute("delete", model.SourceHTTPRoute, "atlas-it-gw", "web", nil))
	t.Logf("route retirée %v après la suppression de la CRD", time.Since(start))
}
```

- [ ] **Step 4 : TraefikService installé après Atlas, et PV orphelin**

```go
const traefikFixture = `
apiVersion: v1
kind: Namespace
metadata: { name: atlas-it-traefik }
---
apiVersion: v1
kind: Service
metadata: { name: a, namespace: atlas-it-traefik }
spec: { selector: { app: a }, ports: [{ port: 80 }] }
---
apiVersion: v1
kind: Service
metadata: { name: b, namespace: atlas-it-traefik }
spec: { selector: { app: b }, ports: [{ port: 80 }] }
---
apiVersion: traefik.io/v1alpha1
kind: TraefikService
metadata: { name: split, namespace: atlas-it-traefik }
spec:
  weighted:
    services:
      - { name: a, port: 80, weight: 3 }
      - { name: b, port: 80, weight: 1 }
---
apiVersion: traefik.io/v1alpha1
kind: IngressRoute
metadata: { name: split, namespace: atlas-it-traefik }
spec:
  routes:
    - match: Host(` + "`split.localtest.me`" + `)
      kind: Rule
      services: [{ name: split, kind: TraefikService }]
`

const orphanPV = `
apiVersion: v1
kind: PersistentVolume
metadata: { name: atlas-it-released }
spec:
  capacity: { storage: 1Gi }
  accessModes: [ReadWriteOnce]
  persistentVolumeReclaimPolicy: Retain
  storageClassName: manual
  claimRef: { namespace: atlas-it-traefik, name: gone, uid: 00000000-0000-0000-0000-000000000002 }
  hostPath: { path: /tmp/atlas-it-released }
`

// TestLiveTraefikServiceAndPV : la CRD des TraefikService installée après le
// démarrage est prise en compte, une IngressRoute qui en vise un se résout en
// Services pondérés en moins de 5 s, et un PV sans PVC est publié.
func TestLiveTraefikServiceAndPV(t *testing.T) {
	crds := manifest(t, "TRAEFIK_CRDS")
	kubectl(t, "", "delete", "crd", "traefikservices.traefik.io", "--ignore-not-found", "--wait=true")
	t.Cleanup(func() {
		restore("delete", "namespace", "atlas-it-traefik", "--ignore-not-found")
		restore("delete", "pv", "atlas-it-released", "--ignore-not-found")
		restore("apply", "--server-side", "-f", crds)
		restore("wait", "--for", "condition=established", "--timeout=60s", "crd/traefikservices.traefik.io")
		restore("apply", "-f", "../../hack/scenarios-traefik/")
	})
	_, sub, _ := startLive(t)

	kubectl(t, "", "apply", "--server-side", "-f", crds)
	kubectl(t, "", "wait", "--for", "condition=established", "--timeout=60s", "crd/traefikservices.traefik.io")
	start := time.Now()
	kubectl(t, traefikFixture, "apply", "-f", "-")
	_ = waitFor(t, sub, "IngressRoute via un TraefikService", isRoute("upsert", model.SourceIngressRoute, "atlas-it-traefik", "split", func(r model.Route) bool {
		b := backends(r)
		return w(b["a"]) == 750 && w(b["b"]) == 250 && b["a"].Via == "atlas-it-traefik/split" && b["a"].State == model.BackendOK
	}))
	seen := time.Since(start)
	t.Logf("route résolue %v après sa création", seen)
	if seen > 5*time.Second {
		t.Errorf("route résolue en %v (> 5 s)", seen)
	}

	start = time.Now()
	kubectl(t, orphanPV, "apply", "-f", "-")
	_ = waitFor(t, sub, "PV sans PVC", func(m stream.Message) bool {
		p, ok := m.Obj.(model.PersistentVolume)
		return m.Type == "upsert" && ok && p.Name == "atlas-it-released" && p.ClaimRef == "atlas-it-traefik/gone"
	})
	if seen := time.Since(start); seen > 5*time.Second {
		t.Errorf("PV visible en %v (> 5 s)", seen)
	}
}
```

- [ ] **Step 5 : vérifier**

Run : `go vet -tags integration ./internal/kube/ && make test-integration`
Expected: `TestLiveLatency`, `TestLiveServiceEndpoints`, `TestLiveGatewayCRDHot` et `TestLiveTraefikServiceAndPV` passent (`--- PASS`) ; les journaux donnent les délais (HTTPRoute et route résolue sous 5 s). Après le test, `kubectl --context kind-atlas get httproute -A` montre de nouveau `storefront` et `preview` (scénarios remis en place).

- [ ] **Step 6 : commit**

```bash
git add Makefile internal/kube/live_test.go
git commit -m "test(kube): CRD Gateway API et TraefikService installées après Atlas, PV orphelin, sur kind

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 32 : banc de charge

**Files:**
- Modify: `hack/load/kwok-up.sh`, `hack/load/kwok-down.sh`

- [ ] **Step 1 : Gateways, HTTPRoutes et PV kwok**

Dans `hack/load/kwok-up.sh`, ajouter aux variables :

```sh
GATEWAYS="${GATEWAYS:-10}"
PVS="${PVS:-30}"
```

et, après le bloc des PVC (avant l'`echo` des Services et PVC créés) :

```sh
# Gateway API (si ses CRD sont installées, par make scenarios) : GATEWAYS
# Gateways et une HTTPRoute 90/10 par Deployment, réparties sur eux.
if k get crd httproutes.gateway.networking.k8s.io >/dev/null 2>&1; then
  {
    g=1
    while [ "$g" -le "$GATEWAYS" ]; do
      cat <<YAML
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: load-gw-$(printf %02d "$g"), namespace: load-test }
spec:
  gatewayClassName: atlas-scenarios
  listeners: [{ name: http, protocol: HTTP, port: 80 }]
YAML
      g=$((g + 1))
    done
    d=1
    while [ "$d" -le "$DEPLOYS" ]; do
      cat <<YAML
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: load-$(printf %03d "$d"), namespace: load-test }
spec:
  parentRefs: [{ name: load-gw-$(printf %02d $(( (d - 1) % GATEWAYS + 1 ))) }]
  hostnames: [load-$(printf %03d "$d").localtest.me]
  rules:
    - backendRefs:
        - { name: load-$(printf %03d "$d")-1, port: 80, weight: 90 }
        - { name: load-$(printf %03d "$d")-2, port: 80, weight: 10 }
YAML
      d=$((d + 1))
    done
  } | k apply -f - >/dev/null
  echo "$GATEWAYS Gateways et $DEPLOYS HTTPRoutes créés dans load-test"
fi
# PV sans PVC (Available) : des citernes vides dans les entrepôts.
p=1
{
  while [ "$p" -le "$PVS" ]; do
    cat <<YAML
---
apiVersion: v1
kind: PersistentVolume
metadata: { name: load-pv-$(printf %03d "$p"), labels: { atlas-load: "true" } }
spec:
  capacity: { storage: 1Gi }
  accessModes: [ReadWriteOnce]
  storageClassName: load-orphans
  hostPath: { path: /tmp/load-pv-$(printf %03d "$p") }
YAML
    p=$((p + 1))
  done
} | k apply -f - >/dev/null
echo "$PVS PV orphelins créés"
```

(Avec `SERVICES_PER_DEPLOY=1`, le backend `-2` n'existe pas et chaque HTTPRoute porte un backend `missing` : c'est voulu, la règle reste dessinée.)

Dans `hack/load/kwok-down.sh`, ajouter avant la suppression des nodes :

```sh
kubectl --context "$CTX" delete pv -l atlas-load=true --ignore-not-found
```

- [ ] **Step 2 : mesurer**

Run : `make scenarios load-up`, puis `make run-kind` et http://localhost:8080/?perf=1 (même protocole qu'au jalon 8 : Chrome, fenêtre 1440 × 900, vue par défaut, rotation continue, puis porte `load-test/load-gw-01` sélectionnée, puis dézoom maximal).
Expected: 15 portes (les 10 Gateways de charge, `infra/public`, `infra/internal`, `kube-system/platform`, `nginx` et `traefik`), environ 100 HTTPRoutes, 30 citernes vides dans l'îlot `load-orphans` ; même cadence qu'au jalon 8 (120 images/s sur M4 au repos et en rotation, au moins 60 sur un portable récent), robots sous 3 ms par frame.

Run : `go run ./cmd/atlas --demo --demo-scale 100x30` puis http://localhost:8080/?perf=1
Expected: même constat avec les Gateways, HTTPRoutes et PV simulés ; relever leurs nombres dans la vue Liste (groupes Entrées et Stockage).

Noter les mesures (images/s, temps de frame des robots, draw calls, nombres d'objets) pour la tâche 34.

- [ ] **Step 3 : commit**

```bash
git add hack/load
git commit -m "test(charge): Gateways, HTTPRoutes pondérées et PV orphelins dans le banc kwok

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 33 : documentation

**Files:**
- Modify: `README.md`, `docs/spec.md`, `docs/superpowers/specs/2026-10-07-jalon-9-reseau-suite-design.md`

- [ ] **Step 1 : README**

1. Introduction : « … les Services sont des relais sur les avenues, les entrées (Ingress, IngressRoute Traefik, Gateways de la Gateway API) des portes à l'ouest, les PVC des citernes dans le quartier Entrepôts, et les PV sans PVC des citernes vides. »
2. Tableau des comptes de test : alice « `view` sur tout le cluster + lecture des nodes et des PV » ; ajouter sous le tableau : « `hack/dev-rbac.yaml` agrège aussi la lecture de la Gateway API et de Traefik à `view` et `edit` (ces CRD n'ont pas de rôles agrégés). »
3. Section « Sur un vrai cluster (kind) », remplacer le paragraphe sur `make scenarios` :

   > `make scenarios` télécharge une fois les CRD Traefik et Gateway API à une version épinglée (`make crds`, dans `hack/crds/.cache/`, ignoré par git), les installe sans contrôleur, puis ajoute des Services (sain, en panne, headless, ExternalName), un Ingress nginx avec un backend manquant, des IngressRoute (dont une via un TraefikService pondéré avec miroir), une IngressRouteTCP et une IngressRouteUDP, trois Gateways (programmé, non programmé, dans `kube-system`) avec une HTTPRoute pondérée 90/10, une GRPCRoute et une HTTPRoute refusée (statuts écrits par `hack/scenarios-gateway/status.sh`, faute de contrôleur), un StatefulSet avec ses PVC, un PVC en attente de consommateur et deux PV sans PVC (`Available`, `Released`).

4. **Droits du ServiceAccount**, remplacer le paragraphe :

   > **Droits du ServiceAccount** : lecture de nodes, pods, namespaces, events, Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs et metrics.k8s.io ; `get`, `list` et `watch` sur services, persistentvolumeclaims, persistentvolumes, ingresses, ingressroutes, ingressroutetcps et ingressrouteudps (`traefik.io`, `traefik.containo.us`), gateways, httproutes et grpcroutes (`gateway.networking.k8s.io`) ; `list` et `watch` sur endpointslices, ingressclasses, traefikservices, gatewayclasses et customresourcedefinitions (le `get` sert l'onglet YAML en `auth.mode=none`) ; `create` sur `subjectaccessreviews` ; `impersonate` sur users et groups (restreignable par `rbac.impersonate`). Aucun droit d'écriture sur les workloads, ni `pods/exec`, ni secrets, ni configmaps : logs, exec et actions passent toujours par impersonation de l'utilisateur. Un type réseau ou stockage que le ServiceAccount ne peut pas lister (sonde bornée à 10 s) est désactivé, avec un warning dans les logs ; sans EndpointSlices, les Services sont désactivés aussi. Une CRD Traefik ou Gateway API installée ou supprimée après le démarrage est prise en compte à chaud (informer sur les `customresourcedefinitions`) ; sans le droit de les lister, au prochain redémarrage.

5. **Tests**, remplacer la ligne `make test-integration` :

   ```sh
   make test-integration  # sur kind : pod créé ou supprimé en moins de 2 s, CRD Gateway API et TraefikService installées après le démarrage (moins de 5 s), PV orphelin
   ```

   et dans **Performance**, après « 150 PVC sans consommateur » : « , et, si les CRD de la Gateway API sont installées, 10 Gateways et une HTTPRoute 90/10 par Deployment, plus 30 PV sans PVC (`GATEWAYS=…`, `PVS=…`) ». Mettre à jour les nombres de la démo `--demo-scale 100x30` avec ceux relevés à la tâche 33.
6. **Architecture**, nouvelle puce après « Réseau et stockage » :

   > - **Types dynamiques et Gateway API** (`internal/kube/dynkinds.go`) : chaque type apporté par une CRD (IngressRoute, IngressRouteTCP/UDP et TraefikService dans les deux groupes Traefik ; Gateway, GatewayClass, HTTPRoute et GRPCRoute en `gateway.networking.k8s.io/v1`) a son informer `unstructured`, démarré quand sa CRD apparaît et arrêté quand elle disparaît (ses objets quittent alors le flux). Un Gateway est un kind du flux (`gateway` : classe, `accepted`, `programmed`, adresses, listeners) et une porte de la ville ; une route Gateway API a une porte par Gateway parent (`gates`) et l'état de ses backends vient du statut écrit par le contrôleur (`refused` si aucun Gateway ne l'accepte). Les TraefikService sont résolus jusqu'aux Services (profondeur 8, cycles détectés) : chaque Service final porte sa part du trafic en pour mille, ou `mirror` et son pourcentage. Les PV sans PVC sont un kind du flux (`persistentVolume`), dessinés en citernes vides dans l'îlot de leur classe.

7. **Liens profonds** : ajouter `/gateways/{namespace}/{nom}`, `/persistentvolumes/{nom}` et les sources `httproute`, `grpcroute`, `ingressroutetcp`, `ingressrouteudp` de `/routes/…` ; « `/gates/{ns}%2F{nom}` ouvre le Gateway s'il est visible ».
8. **Avancement** : ajouter la ligne `| 9 | Suite du réseau et du stockage : Gateway API, Traefik complet, CRD à chaud, PV sans PVC | fait |`.
9. Après « Choix du jalon 8 » et ses mesures, ajouter :

   > Choix du jalon 9 ([design](docs/superpowers/specs/2026-10-07-jalon-9-reseau-suite-design.md), [plan](docs/superpowers/plans/2026-10-07-jalon-9-reseau-suite.md)) :
   >
   > - Lecture seule : aucune action nouvelle. Les portes restent déduites des routes ; un Gateway visible enrichit sa porte de son état (rouge s'il n'est pas programmé, gris sans statut).
   > - Gateway API `v1` seulement, lue en `unstructured` (pas de dépendance `sigs.k8s.io/gateway-api`) ; TLSRoute, TCPRoute et UDPRoute (canal experimental) sont hors jalon. Atlas ne calcule pas les ReferenceGrant : il lit `status.parents` (`ResolvedRefs`, `Accepted`).
   > - Poids en pour mille de la règle d'origine, même unité pour Gateway API et Traefik ; un miroir n'a pas de part mais un pourcentage.
   > - Un PV n'est publié que sans PVC existant ; le YAML et les événements d'un objet cluster-scoped passent le namespace `_` dans l'URL.
   > - L'inspecteur d'un Gateway montre sa classe mais pas le contrôleur de la GatewayClass (hors du flux).
   > - CRD des scénarios et du test d'intégration téléchargées à version épinglée (`GATEWAY_API_VERSION`, `TRAEFIK_CRD_VERSION` dans le `Makefile`) plutôt que versionnées : environ 1 Mo évité dans le dépôt, mêmes CRD à chaque passage.

   puis un tableau « Mesures du banc (jalon 9) » aux colonnes du tableau du jalon 8 (`Cas`, `Au repos`, `Rotation continue`, `Draw calls`), une ligne par cas mesuré à la tâche 33 (kind + kwok vue par défaut, porte `load-test/load-gw-01` sélectionnée, dézoom maximal ; démo `--demo-scale 100x30` vue par défaut), avec les valeurs relevées.

- [ ] **Step 2 : spécification**

Dans `docs/spec.md` :
- Table « Correspondances », après la ligne « Contrôleur d'entrée » :

  ```markdown
  | Gateway (Gateway API) | **Porte** nommée `namespace/nom`, avec les portes des contrôleurs d'entrée | Rouge si non programmé, orange si un listener n'est pas prêt ou une route est refusée ou cassée, grise sans statut |
  ```

  après la ligne « Ingress, IngressRoute » :

  ```markdown
  | HTTPRoute, GRPCRoute, IngressRouteTCP, IngressRouteUDP | **Route** : une ligne principale par porte (une route Gateway API peut en avoir plusieurs) | Part du trafic (survol, inspecteur) ; miroir en pointillé discret ; route refusée en rouge pointillé avec un panneau « ⊘ » |
  ```

  et après la ligne « PVC » :

  ```markdown
  | PV sans PVC | **Citerne vide** en fil de fer, dans l'îlot de sa StorageClass | `Available` gris, `Released` gris avec un panneau, `Failed` rouge ; aucune conduite |
  ```

- « Plan de livraison » : ajouter « 9. **Suite du réseau et du stockage** : Gateway API, Traefik complet (IngressRouteTCP/UDP, TraefikService), CRD à chaud, PV sans PVC ; design `docs/superpowers/specs/2026-10-07-jalon-9-reseau-suite-design.md`. »

Dans le design du jalon 9 :
- « Intégration kind » : préciser que les CRD viennent de `make crds` (version épinglée) et que les scénarios ajoutent aussi un Gateway dans `kube-system` et une IngressRouteUDP dans `kube-system` pour le test de bob.
- « Inspecteur », ligne Gateway : « Classe, état et raison, … » (sans « et son contrôleur »).

- [ ] **Step 3 : commit**

```bash
git add README.md docs
git commit -m "docs: README et spécification du jalon 9

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

### Task 34 : vérification finale

- [ ] **Step 1 : tout lancer**

Run (racine) : `gofmt -l . && go vet ./... && go vet -tags integration ./internal/kube/ && make test && (cd web && npx tsc --noEmit) && make e2e && helm lint deploy/helm/cluster-atlas --set auth.oidc.clientID=ci --set publicURL=https://atlas.example.com && go run github.com/rhysd/actionlint/cmd/actionlint@latest`
Expected: aucune sortie de `gofmt`, tous les tests verts (Go, Vitest, Playwright), `0 chart(s) failed`, actionlint sans erreur.

Run (kind) : `make scenarios test-integration`
Expected: les quatre tests `Live` passent.

Run (kind avec Dex) : `make e2e-auth`
Expected: PASS (alice reçoit des PV et la HTTPRoute ; bob ni PV ni rien de `kube-system`).

- [ ] **Step 2 : critères de fin du design**

Cocher, preuves à l'appui (sortie de commande ou capture dans `web/e2e/__screenshots__/`) :
- tests Go (conversions, résolution des TraefikService, registre, déclenchements, filtre d'accès, simulateur), Vitest et e2e démo verts ;
- `make test-integration` vert sur kind : HTTPRoute et route via TraefikService visibles en moins de 5 s après leur création, CRD installées après le démarrage ; suppression de la CRD HTTPRoute qui retire la route ; PV orphelin publié ;
- `make e2e-auth` vert : bob ne reçoit ni Gateway ni route de `kube-system`, ni PV ;
- banc tenu (tâche 33) à la cadence du jalon 8 ;
- README, `docs/spec.md` et design du jalon 9 à jour.

Un critère qui ne passe pas se signale tel quel, avec sa sortie, au lieu d'être coché.
