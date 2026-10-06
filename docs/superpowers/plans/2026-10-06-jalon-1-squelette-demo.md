# Jalon 1 — Squelette et mode démo : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** un binaire Go unique (`atlas --demo`) qui sert le front compilé et alimente, via le vrai protocole `/api/stream`, une scène R3F (ville, bâtiments et robots instanciés) reproduisant le prototype `docs/prototype.html`.

**Architecture :** le simulateur vit **côté Go** et pousse des objets du modèle réduit (`Node`, `Pod`, `Workload`) dans un hub de stream (snapshot + deltas regroupés toutes les 250 ms, `rev`, rejeu à la reconnexion). Le front ne connaît que ce protocole : au jalon 2, les informers remplacent le simulateur sans toucher au front. Le front stocke l'état normalisé dans Zustand ; la scène lit le store dans `useFrame` (aucun re-render React par message) et dessine robots et bâtiments avec des `InstancedMesh` par pièce.

**Tech Stack :** Go 1.23+ (go.mod `go 1.23`), chi, coder/websocket, log/slog ; React 18, TypeScript 5, Vite 6, React Three Fiber 8, drei 9, three 0.170, Zustand 5, Vitest 3, Playwright.

Référence : `docs/spec.md` (sections API backend, Vue 3D, Performance, Stack) et `docs/prototype.html` (simulateur, palette, tokens CSS, géométries).

---

## Décisions propres à ce jalon

1. **Simulateur en Go, pas en JS.** Le prototype simule dans le navigateur ; ici le simulateur produit le modèle réduit et passe par le hub. Le mode démo teste donc le vrai chemin de données.
2. **Animations des robots calculées sur CPU** dans des matrices d'instance, via une fonction pure `poseAt()` testée. La spec vise le vertex shader ; le passage se fera au jalon 7 si le test de charge (3 000 pods) l'exige. Le nombre de draw calls est déjà celui de la spec (une `InstancedMesh` par pièce).
3. **Inspecteur limité à l'onglet Aperçu, en lecture seule** (pod et node). Logs, terminal, YAML, événements : jalon 5. Actions : jalon 6.
4. **Polices auto-hébergées** (`@fontsource/*`) : la CSP interdit tout script ou style externe.
5. **Hors mode démo, le binaire refuse de démarrer** avec le message « mode cluster disponible au jalon 2 ».
6. Le simulateur ajoute au prototype un DaemonSet, un StatefulSet, un Job et un pod `ImagePullBackOff`, pour couvrir toutes les lignes des tables « Correspondances » et « Statuts et postures » de la spec.

## Structure des fichiers

```text
cluster-atlas/
├── go.mod                         # module github.com/no-inspi/cluster-atlas
├── Makefile                       # web, build, test, demo, dev, e2e
├── README.md
├── cmd/atlas/main.go              # flags, wiring, arrêt propre
├── internal/
│   ├── model/model.go             # Node, Pod, Workload, Metrics (JSON du front)
│   ├── stream/
│   │   ├── hub.go                 # état courant, rev, regroupement 250 ms, rejeu
│   │   └── ws.go                  # handler WebSocket /api/stream
│   ├── demo/
│   │   ├── catalog.go             # namespaces, pools, workloads (repris du prototype)
│   │   └── sim.go                 # machine à états : scheduling, crashs, churn, jobs, métriques
│   └── server/server.go           # chi : /healthz, /readyz, /api/me, /api/stream, SPA + CSP
├── web/
│   ├── embed.go                   # package web : //go:embed all:dist
│   ├── index.html, vite.config.ts, tsconfig.json, package.json, playwright.config.ts
│   ├── e2e/demo.spec.ts
│   └── src/
│       ├── main.tsx, App.tsx, styles.css (tokens du prototype)
│       ├── api/types.ts, api/stream.ts
│       ├── store/cluster.ts, store/feed.ts
│       ├── scene/
│       │   ├── colors.ts          # hash namespace → palette 12 teintes
│       │   ├── posture.ts         # statut → antenne + posture ; poseAt()
│       │   ├── layout.ts          # quartiers, parcelles, SlotAllocator, file d'attente
│       │   ├── instanced.ts       # PartSpec, matériau avec opacité par instance
│       │   ├── Scene.tsx, City.tsx, Buildings.tsx, Robots.tsx, Selection.tsx
│       │   └── theme.ts           # lecture des tokens CSS pour three
│       └── ui/TopBar.tsx, Stats.tsx, Legend.tsx, Feed.tsx, Inspector.tsx
└── docs/ (spec, prototype, plans)
```

---

### Task 1 : socle du dépôt

**Files :** `go.mod`, `.gitignore`, `Makefile`, `web/package.json`, `web/vite.config.ts`, `web/tsconfig.json`, `web/index.html`, `web/src/main.tsx`, `web/embed.go`, `web/dist/.gitkeep`

- [ ] `go mod init github.com/no-inspi/cluster-atlas`, `go 1.23` ; deps : `github.com/go-chi/chi/v5`, `github.com/coder/websocket`.
- [ ] `web/` : React 18.3, react-dom, three 0.170, @react-three/fiber 8.18, @react-three/drei 9.122, zustand 5, @fontsource/instrument-sans, @fontsource/jetbrains-mono ; dev : typescript 5.9, vite 6, @vitejs/plugin-react 4, vitest 3, jsdom 26, @types/*, @playwright/test.
- [ ] `vite.config.ts` : proxy `/api` (ws: true), `/healthz` → `http://localhost:8080` ; `build.outDir = dist`, `emptyOutDir: true` ; Vitest en `jsdom`.
- [ ] `web/embed.go` :

```go
// Package web embarque le front compilé (web/dist) dans le binaire.
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
```

- [ ] `Makefile` : `web` (npm ci + build), `build` (web puis `go build -o bin/atlas ./cmd/atlas`), `test` (`go test ./...` + `npm test`), `demo` (build puis `bin/atlas --demo`), `dev` (atlas --demo + vite), `e2e`. Chaque cible Go fait `mkdir -p web/dist && touch web/dist/.gitkeep` pour que `go:embed` compile sans front.
- [ ] Vérif : `go build ./... && (cd web && npx tsc --noEmit)` passe. Commit `chore: socle du monorepo`.

### Task 2 : modèle réduit (`internal/model`)

Types exacts de la spec, sérialisés en camelCase. Quantités en entiers : CPU en millicores, mémoire en octets.

```go
type Resources struct { CPU int64 `json:"cpu"`; Memory int64 `json:"memory"`; Pods int64 `json:"pods,omitempty"` }
type Condition struct { Type string `json:"type"`; Status string `json:"status"`; Reason string `json:"reason,omitempty"`; Message string `json:"message,omitempty"` }
type Taint struct { Key, Value, Effect string }        // tags json key/value/effect
type Node struct {
  Name, Pool, InstanceType, Zone string; Spot bool; GPU int
  Allocatable, Requested Resources; Conditions []Condition; Taints []Taint
  Unschedulable bool; KubeletVersion string; CreatedAt time.Time
}
type ContainerStatus struct { Name, Image string; Ready bool; State string; Reason string; Restarts int32; Init bool }
type OwnerRef struct { Kind, Name string }
type Pod struct {
  UID, Name, Namespace, NodeName, Phase, DisplayStatus string; Restarts int32
  Containers []ContainerStatus; Owner OwnerRef; Requests, Limits Resources
  PodIP string; CreatedAt time.Time; QOSClass string; Ready bool
}
type ArgoInfo struct { Application, SyncStatus string }
type Workload struct { Kind, Name, Namespace string; Replicas, ReadyReplicas int32; Argo *ArgoInfo `json:"argocd,omitempty"` }
type Usage struct { CPU, Memory int64 }
type Metrics struct { Pods map[string]Usage `json:"pods"`; Nodes map[string]Usage `json:"nodes"` }
```

`Pod.Ready` et `Pod.Limits` complètent le modèle de la spec (posture « Running non ready », jauge mémoire vs limit). Clés : `NodeKey(n)=n.Name`, `PodKey(p)=p.UID`, `WorkloadKey(w)=w.Kind+"/"+w.Namespace+"/"+w.Name`.

- [ ] Test `model_test.go` : un `Pod` sérialisé contient `"displayStatus"`, `"nodeName"`, `"owner":{"kind":…}` ; `Workload` sans Argo n'a pas de clé `argocd` ; `WorkloadKey` attendu.
- [ ] Implémenter, `go test ./internal/model`, commit.

### Task 3 : hub de stream (`internal/stream/hub.go`)

Contrat :

```go
type Kind string // "node" | "pod" | "workload"
type Message struct {
  Type string `json:"type"`           // snapshot | upsert | delete | metrics
  Rev  uint64 `json:"rev,omitempty"`
  Kind Kind   `json:"kind,omitempty"`
  Obj  any    `json:"obj,omitempty"`
  Nodes []model.Node `json:"nodes,omitempty"`; Pods []model.Pod `json:"pods,omitempty"`; Workloads []model.Workload `json:"workloads,omitempty"`
  Metrics *model.Metrics `json:"metrics,omitempty"`
}
func NewHub(opts Options) *Hub      // Options{FlushInterval: 250ms, History: 4096, Clock}
func (h *Hub) Upsert(kind Kind, key string, obj any)
func (h *Hub) Delete(kind Kind, key string, obj any)
func (h *Hub) SetMetrics(m model.Metrics)
func (h *Hub) Flush()                 // appelé par Run toutes les FlushInterval ; exposé pour les tests
func (h *Hub) Run(ctx context.Context)
func (h *Hub) Subscribe(lastRev uint64) (initial []Message, sub *Subscription)
// Subscription : C <-chan []Message (un lot par flush), Close()
```

Règles :
- `Upsert/Delete` mettent à jour l'état courant tout de suite et ajoutent au lot en attente, **coalescé par (kind, key)** : seul le dernier changement d'un objet part dans le lot.
- `Flush` attribue un `rev` croissant à chaque message du lot, les ajoute à l'historique (anneau de `History` messages) et envoie le lot à chaque abonné. Un abonné lent (canal plein, capacité 64 lots) est fermé ; le client se reconnecte avec son `rev`.
- `Subscribe(0)` ou `lastRev` hors historique → `[snapshot]` (objets triés par clé, `rev` = rev courant) suivi du dernier message `metrics` connu. `lastRev` dans l'historique → les deltas `> lastRev` puis metrics. `lastRev == rev courant` → seulement metrics.
- `SetMetrics` diffuse immédiatement un message `metrics` (sans rev).

- [ ] Tests (`hub_test.go`), tous écrits avant l'implémentation :
  - snapshot initial vide puis contenant les objets upsertés, triés ;
  - deux upserts du même pod avant un flush → un seul message, dernier état ;
  - upsert puis delete avant flush → un seul `delete` ;
  - revs strictement croissants d'un lot à l'autre ;
  - `Subscribe(rev)` dans l'historique → rejoue seulement les deltas manquants ;
  - `Subscribe(rev)` sorti de l'historique (History=2) → snapshot ;
  - abonné qui ne lit pas → fermé après 64 lots sans bloquer `Flush` ;
  - metrics : dernier message rejoué à l'abonnement.
- [ ] Implémenter (mutex unique, pas de goroutine par abonné), `go test -race ./internal/stream`, commit.

### Task 4 : handler WebSocket (`internal/stream/ws.go`)

```go
func Handler(h *Hub, log *slog.Logger) http.Handler
```

- `websocket.Accept` avec `CompressionMode: websocket.CompressionContextTakeover` (permessage-deflate) ; vérification d'`Origin` laissée au comportement par défaut de coder/websocket (même hôte).
- Query `?rev=N` → `Subscribe(N)`. Écrit les messages initiaux puis chaque lot, **un message JSON par frame texte**. Ping toutes les 30 s. Fermeture propre quand le contexte se termine ou que l'abonnement est fermé (`StatusTryAgainLater`).
- [ ] Test avec `httptest.NewServer` + `websocket.Dial` : reçoit le snapshot ; après `Upsert`+`Flush`, reçoit un `upsert` avec le bon `rev` ; reconnexion avec `?rev=` reçoit le delta manquant et pas de snapshot ; un `Origin` étranger est refusé (403).
- [ ] Commit.

### Task 5 : catalogue démo (`internal/demo/catalog.go`)

Reprend les données du prototype : namespaces `production, staging, monitoring, argocd, kube-system` ; pools `default-pool` (3 nodes e2-standard-4, 3920m / 13000Mi), `spot-pool` (2 nodes e2-standard-8 spot, 7910m / 27000Mi), `gpu-pool` (1 node g2-standard-8 + 1 GPU, taint `nvidia.com/gpu=present:NoSchedule`) ; les 13 Deployments du prototype avec image, requests, `crashy` pour `payment-worker`, `gpu` pour `ml-inference`. Annotations ArgoCD (`app.kubernetes.io/managed-by: argocd`) sur les Deployments de `production`, `staging`, `monitoring` → `Workload.Argo{Application: "<ns>-apps", SyncStatus: "Synced"}`.

Ajouts (décision 6) :
- DaemonSet `node-exporter` (monitoring, 50m/64Mi, tolère tout → un pod par node, GPU inclus) ;
- StatefulSet `postgres-payments` (production, 2 replicas, 500m/1Gi, noms `-0`, `-1`) ;
- Job `db-backup` (production, recréé toutes les 45 s, `Completed` après 8 s, supprimé 30 s après) ;
- Deployment `checkout-preview` (staging, 1 replica, image `…:does-not-exist` → `ImagePullBackOff`).
- `orders-service` staging : un replica `Running` mais non ready (readiness KO) pour la posture « tête baissée ».

- [ ] Pas de test séparé : le catalogue est couvert par les tests du simulateur. Commit avec la Task 6.

### Task 6 : simulateur (`internal/demo/sim.go`)

```go
type Sink interface {
  Upsert(kind stream.Kind, key string, obj any)
  Delete(kind stream.Kind, key string, obj any)
  SetMetrics(m model.Metrics)
}
type Options struct { ClusterName string; Seed int64; Now func() time.Time }
func New(sink Sink, opts Options) *Sim
func (s *Sim) Step(now time.Time)          // avance la machine à états ; déterministe à seed + horloge données
func (s *Sim) Run(ctx context.Context)     // Step toutes les 200 ms, métriques toutes les 15 s
func (s *Sim) Scale(ns, name string, replicas int32) // utilisé par les tests ; les actions arrivent au jalon 6
```

Comportement (repris du prototype, avec les durées du prototype) :
- Démarrage : nodes et workloads publiés, pods placés immédiatement (`Running`, âges aléatoires 1–72 h), le pod `crashy` démarre en `CrashLoopBackOff` avec 6–14 redémarrages.
- **Reconcile** : chaque Deployment/StatefulSet maintient `replicas` pods (scale down : les plus récents d'abord) ; un DaemonSet un pod par node schedulable ; les Jobs selon leur horloge.
- **Scheduler** : un pod `Pending` depuis > 900 ms est placé sur un node non cordonné, compatible GPU/taints, avec une place libre (12 places max par node, comme le prototype) et assez de CPU allouable ; tri : moins de replicas du même workload d'abord, puis node le moins chargé. Sinon `Pending` avec `displayStatus: Pending` (pas de node).
- **Cycle de vie** : `ContainerCreating` 1–1,8 s puis `Running` ; le pod crashy repasse `Error` (1,2 s) → `CrashLoopBackOff` (5,8 s) → `Running`, `restarts++` ; `Terminating` 900 ms puis delete.
- **Churn** : toutes les 16 s, 60 % de chances qu'un pod `Running` de `staging` soit supprimé (rollout).
- `Node.Requested` recalculé à chaque placement/suppression ; `Workload.ReadyReplicas` mis à jour.
- **Métriques** : CPU = request × U(0,25 ; 1,15), mémoire = request × U(0,45 ; 0,95) pour les pods Running ; nodes = somme.
- `DisplayStatus` suit les chaînes de `kubectl get pods` (`Running`, `Pending`, `ContainerCreating`, `CrashLoopBackOff`, `Error`, `ImagePullBackOff`, `Terminating`, `Completed`).

- [ ] Tests (`sim_test.go`) avec un `fakeSink` qui enregistre l'état final, seed fixe et horloge simulée (pas de `time.Sleep`) :
  - au démarrage : 6 nodes, tous les pods attendus, chaque replica d'un Deployment à 3 replicas sur 3 nodes distincts ;
  - `ml-inference` est sur le node GPU ; aucun autre pod (hors node-exporter) sur le node GPU ;
  - node-exporter : exactement un pod par node ;
  - `checkout-preview` reste `ImagePullBackOff` ;
  - après 20 s simulées, le pod crashy a plus de redémarrages et est passé par `Error` et `CrashLoopBackOff` ;
  - `Scale("production","api-gateway",12)` → certains pods restent `Pending` sans `nodeName` (capacité épuisée) ;
  - Job : `Completed` puis supprimé dans la fenêtre attendue ;
  - même seed + même horloge → même séquence d'événements (déterminisme).
- [ ] Implémenter, `go test -race ./internal/demo`, commit `feat(demo): simulateur de cluster`.

### Task 7 : serveur HTTP (`internal/server`) et `cmd/atlas`

```go
type Config struct { ClusterName string; Demo bool; Static fs.FS }
func New(cfg Config, hub *stream.Hub, log *slog.Logger) http.Handler
```

- `/healthz` → 200 `ok` ; `/readyz` → 200 quand le hub a reçu son premier état (`hub.Ready()`), sinon 503.
- `/api/me` → `{"user":"demo","groups":["demo"],"cluster":"<ClusterName>","demo":true}`.
- `/api/stream` → `stream.Handler`.
- Tout le reste : fichiers de `Static` (`web/dist`), repli sur `index.html` pour les routes SPA, `Cache-Control: immutable` sur `/assets/*`. Si `index.html` est absent : 503 « front non compilé : lancez `make web` ».
- En-têtes sur toutes les réponses : `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; worker-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: same-origin`.
- `cmd/atlas/main.go` : flags `--addr :8080`, `--demo`, `--cluster-name gke-prod-europe-west1` (env `ATLAS_*`) ; logs slog JSON ; sans `--demo` → exit 2 avec le message de la décision 5 ; arrêt propre sur SIGTERM (Shutdown 10 s).
- [ ] Tests `server_test.go` (avec `fstest.MapFS`) : healthz, readyz 503 puis 200, `/api/me`, CSP présente, `/pods/foo` sert `index.html`, `/assets/x.js` sert le fichier, front absent → 503.
- [ ] `go build ./cmd/atlas && ./atlas --demo` répond sur `/healthz`. Commit.

### Task 8 : front — types, store et client de stream

`api/types.ts` : miroir exact du JSON de la Task 2 et des `Message` de la Task 3.

`store/cluster.ts` (Zustand, `subscribeWithSelector`) :

```ts
interface ClusterState {
  connection: 'connecting' | 'live' | 'reconnecting'
  rev: number
  nodes: Record<string, Node>; pods: Record<string, Pod>; workloads: Record<string, Workload>
  metrics: Metrics
  me: Me | null
  selection: { type: 'pod' | 'node'; key: string } | null
  nsFilter: string | null
  feed: FeedItem[]                 // 5 derniers, le plus récent en tête
  version: number                  // +1 à chaque changement d'objets, lu par la scène
  applyMessage(m: Message): void
  select(sel: ClusterState['selection']): void
  setNsFilter(ns: string | null): void
}
```

`store/feed.ts` : `feedFor(prev: Pod | undefined, next: Pod | undefined): FeedItem | null` — création (`ReplicaSet … a créé …`), placement (`Scheduler : <pod> → <suffixe node>`), passage en `CrashLoopBackOff`/`Error` (niveau `e`), `Pending` persistant (`… ne peut pas être placé`, niveau `w`), suppression. Pas de flot à l'arrivée du snapshot.

`api/stream.ts` : `connectStream(store)` ouvre `ws(s)://host/api/stream?rev=<rev>`, applique chaque message, reconnexion avec backoff 0,5 s → 8 s, état `connection`.

- [ ] Tests Vitest : snapshot remplace l'état ; upsert/delete pod ; `rev` suit le dernier message ; metrics ; sélection d'un pod supprimé conservée (l'inspecteur affiche « supprimé ») ; `feedFor` sur chaque transition ; le snapshot ne produit pas de flot.
- [ ] Commit.

### Task 9 : fonctions pures de la scène

`scene/colors.ts` : palette de 12 teintes (les 5 couleurs du prototype + 7 compatibles clair/sombre) ; `nsColor(ns)` = palette[fnv1a(ns) % 12]. (L'annotation `atlas.io/color` nécessite les objets Namespace dans le modèle : jalon 2.)

`scene/posture.ts` :

```ts
type Antenna = 'ok' | 'warn' | 'err' | 'mute' | 'done'
type PostureKind = 'idle' | 'sulk' | 'stomp' | 'spin' | 'fallen' | 'sitting' | 'shrink' | 'fade'
function postureFor(p: Pod): { antenna: Antenna; blink: boolean; kind: PostureKind; question: boolean }
function poseAt(kind: PostureKind, t: number, phase: number): { hop: number; yaw: number; fall: number; scale: number; headTilt: number }
```

Table testée ligne à ligne contre la spec : Running ready → ok/idle ; Running non ready → warn fixe/sulk ; Pending → warn/stomp ; ContainerCreating & PodInitializing → warn/spin (scale 0,8) ; CrashLoopBackOff, Error, OOMKilled → err clignotant/fallen ; ImagePullBackOff & ErrImagePull → err/sitting + question ; Terminating → mute/shrink ; Completed → done (bleu)/fade.

`scene/layout.ts` :
- `layoutCity(nodes)` : pools triés par nom, nodes triés par nom en grille dans leur quartier (3 colonnes, parcelles de 4,1 + allée de 1,9) ; renvoie positions des parcelles, rectangles et libellés des quartiers, zone de file d'attente devant la ville.
- `slotCount(maxPodsPerNode)` → côté de grille (4×3 par défaut, au plus 48 places).
- `SlotAllocator` : `assign(nodeName, podUid)` → première place libre, stable tant que le pod existe ; `release(podUid)` ; `sync(pods)` libère les absents.
- `queuePosition(index)`.

- [ ] Tests Vitest : chaque statut de la table ; `poseAt` borné et périodique ; `nsColor` stable ; layout stable quel que soit l'ordre d'entrée ; un pod garde sa place quand un voisin disparaît ; un nouveau pod prend la première place libre.
- [ ] Commit.

### Task 10 : scène 3D

- `scene/instanced.ts` : `PartSpec { geometry, material: 'body'|'head'|'dark'|'bulb'|…, offset: [x,y,z], inInner: boolean }` ; `createOpacityMaterial(params)` : `MeshStandardMaterial` patché par `onBeforeCompile` (attribut d'instance `aOpacity` → `diffuseColor.a *= vOpacity`).
- `Scene.tsx` : `<Canvas orthographic shadows dpr={[1,2]}>`, caméra (24, 26, 26), `OrbitControls` (polar 0,45–1,15, zoom 0,6–3,2, damping 0,08, `screenSpacePanning: false`) ; lumières du prototype ; fond = `--bg`.
- `City.tsx` : sol, routes, quartiers teintés par type de pool (`--zone-std/spot/gpu`), libellés au sol en `CanvasTexture`, file d'attente, arbres. Recalculé seulement quand la liste des nodes change.
- `Buildings.tsx` : une `InstancedMesh` par pièce et par style (maison : murs, fenêtres, toit, cheminée ; tente : toile, toit, mât, drapeau ; GPU : bloc, bandeau lumineux, caissons, pales). Plateforme commune (picking des nodes). Barrière rayée si `unschedulable`. NotReady : couleur grisée, fenêtres éteintes, icône d'alerte. Fumée : 5 bouffées instanciées par maison, opacité ∝ `requested.cpu / allocatable.cpu` ; vitesse des pales ∝ charge.
- `Robots.tsx` : pièces instanciées (2 jambes, corps, tête, 2 yeux, antenne, ampoule, casque, sac à dos, casquette, point d'interrogation) ; capacité = nombre de pods, réallouée par paliers de 256. Dans `useFrame` : position cible (place du node ou file), lissage `1 - exp(-dt·6)`, `poseAt`, couleur corps = `nsColor`, pulsation rouge si `fallen`, ampoule = couleur d'antenne, opacité 0,1 si filtre namespace actif et pod hors filtre, 0,75 si Pending. Accessoires : casque si owner DaemonSet, sac si StatefulSet, casquette si Job (échelle 0 sinon). `prefers-reduced-motion` : pas de balancement ni clignement.
- `Selection.tsx` : marqueur conique flottant au-dessus de l'objet sélectionné ; arcs (`LineSegments`, 14 au plus) vers les autres pods du même `owner`.
- Picking : `onClick` sur les meshes instanciés corps/tête (→ `instanceId` → uid) et plateformes (→ node) ; clic dans le vide ferme ; déplacement > 6 px = pan, pas de clic.
- `theme.ts` : lit les tokens CSS, ré-applique aux matériaux sur changement de `prefers-color-scheme`.
- [ ] Vérif manuelle via `make demo` (et capture Playwright, Task 12) : rendu proche du prototype dans les deux thèmes. Commit.

### Task 11 : interface (HUD et inspecteur Aperçu)

Reprend tel quel le CSS du prototype (`styles.css`, tokens clair/sombre, `@fontsource`).
- `TopBar` : marque, nom du cluster (`/api/me`), pastille Live/Reconnexion + horloge, badge « Données simulées » si `me.demo`.
- `Stats` : nodes prêts, pods Running/total, en attente (Pending + ContainerCreating), en erreur ; avec filtre : « N pods · M nodes ».
- `Legend` : chips des namespaces présents, `aria-pressed`, clic = filtre.
- `Feed` : 5 dernières entrées (`aria-live="polite"`), caché sous 760 px.
- `Inspector` : panneau latéral ≥ 761 px, feuille en bas en dessous ; onglet Aperçu seul. Pod : namespace, node (lien), IP, âge, QoS, redémarrages, containers (image, état, ready), chaîne de propriétaires, répartition des replicas (liens nodes), CPU/mémoire vs request et limit (« indisponible » sans métriques). Node : pool, type, zone, spot, kubelet, âge, conditions, taints, jauges CPU/mémoire/pods (barre orange > 90 %), liste des pods cliquable. Pod disparu : « Ce pod a été supprimé. » `Échap` ferme.
- [ ] Test Vitest (React Testing Library non requis : tests sur les fonctions de formatage `fmtCpu`, `fmtMem`, `age` extraites dans `ui/format.ts`).
- [ ] Commit.

### Task 12 : e2e, README, vérification finale

- `web/e2e/demo.spec.ts` (Playwright, `webServer` = `../bin/atlas --demo --addr :18080`) : la page charge sans erreur console ni violation CSP ; le canvas existe ; « Pods Running » affiche `x/y` avec y > 20 ; le badge « Données simulées » est visible ; clic sur une chip de namespace → la stat de filtre apparaît ; capture d'écran clair et sombre dans `web/e2e/__screenshots__` (artefact, pas d'assertion visuelle).
- `README.md` : ce qu'est le projet, `make demo`, `make dev`, `make test`, état des jalons (1 fait, 2–7 à venir), décisions de ce plan.
- [ ] `make test` vert, `make e2e` vert, `bin/atlas --demo` ouvert dans un navigateur : rendu vérifié sur capture.
- [ ] Commit final du jalon.

---

## Auto-revue contre la spec (jalon 1)

- Monorepo et arborescence de la spec : Task 1, structure ci-dessus (les dossiers `auth/`, `access/`, `logs/`, `exec/`, `actions/`, `audit/`, `deploy/helm`, `hack/` arrivent aux jalons qui les utilisent).
- Binaire Go qui sert le front (`embed.FS`) : Tasks 1, 7.
- Scène R3F : ville, quartiers par pool, bâtiments par type de node, robots instanciés, file d'attente : Task 10 ; tables de correspondances et de postures : Tasks 9, 10.
- Flux `/api/stream` snapshot + deltas 250 ms + rev + reconnexion + metrics 15 s + deflate : Tasks 3, 4, 6.
- Mode démo `--demo` sans API server : Tasks 5–7.
- Filtres namespace, compteur, bandeau d'événements, sélection + arcs + marqueur, thèmes clair et sombre, `prefers-reduced-motion` : Tasks 10, 11.
- CSP stricte, Origin des WebSockets : Tasks 4, 7, 12.
- Hors jalon 1 et assumé : onglets Logs/Terminal/YAML/Événements, actions, recherche `/`, vue Liste, LOD et regroupement > 48 pods, rendu à la demande, animation en shader.
