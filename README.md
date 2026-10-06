# Cluster Atlas

Console Kubernetes web, déployée dans le cluster qu'elle observe, qui montre l'état du cluster en temps réel sous la forme d'une ville isométrique en 3D : chaque node pool est un quartier, chaque node un bâtiment, chaque pod un petit robot dont la couleur indique le namespace et l'antenne le statut.

La spécification complète est dans [`docs/spec.md`](docs/spec.md) et le prototype d'origine dans [`docs/prototype.html`](docs/prototype.html).

## Démarrer

Prérequis : Go 1.26 (exigé par client-go v0.37), Node 22 ; Docker et kind pour un vrai cluster.

```sh
make demo        # compile le front et le binaire, puis lance atlas --demo sur :8080
```

Ouvrez http://localhost:8080 : la ville tourne sur un cluster simulé (6 nodes, une trentaine de pods, un pod en CrashLoopBackOff, un en ImagePullBackOff, un Job périodique, des rollouts en staging).

Pour développer le front avec rechargement à chaud :

```sh
make dev         # backend démo sur :8080 + Vite sur :5173 (proxy /api)
```

### Sur un vrai cluster (kind)

```sh
make kind-up     # cluster « atlas » : control-plane + 4 workers en 3 pools, taint GPU, metrics-server
make scenarios   # CrashLoopBackOff, ImagePullBackOff, Pending, non ready, StatefulSet, DaemonSet, CronJob
make run-kind    # atlas --auth-mode=none --context kind-atlas sur :8080
make kind-down
```

Sans `--demo`, atlas lit le cluster de son kubeconfig (ou en in-cluster quand il tourne dans un pod). Tant que l'OIDC n'est pas en place (jalon 4), le mode cluster exige `--auth-mode=none`, réservé au développement.

| Flag | Variable | Rôle |
| --- | --- | --- |
| `--demo` | `ATLAS_DEMO=true` | cluster simulé |
| `--auth-mode` | `ATLAS_AUTH_MODE` | `oidc` (jalon 4) ou `none` |
| `--kubeconfig`, `--context` | `KUBECONFIG`, `ATLAS_CONTEXT` | cluster à lire hors in-cluster |
| `--pool-label` | `ATLAS_POOL_LABEL` | label du node pool (sinon détection GKE, Karpenter, EKS, type d'instance) |
| `--cluster-name` | `ATLAS_CLUSTER_NAME` | nom affiché |
| `--addr` | `ATLAS_ADDR` | adresse d'écoute (`:8080`) |

## Tests

```sh
make test        # go vet + go test -race, puis Vitest
make e2e         # Playwright contre le binaire en mode démo (installe d'abord : cd web && npx playwright install chromium)
make test-integration  # sur kind : un pod créé ou supprimé est diffusé en moins de 2 s
```

## Architecture

- **Un binaire Go** (`cmd/atlas`) sert le front compilé (`embed.FS`), les probes `/healthz` et `/readyz`, `/api/me` et le flux `/api/stream`.
- **`/api/stream`** (WebSocket) envoie un snapshot puis des deltas `upsert`/`delete` regroupés toutes les 250 ms et numérotés (`rev`). À la reconnexion, le client renvoie son dernier `rev` et reçoit seulement les deltas manquants, ou un nouveau snapshot si l'écart est trop grand. Les métriques arrivent toutes les 15 s.
- **La source Kubernetes** (`internal/kube`) : un informer partagé par type (nodes, pods, namespaces, Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs), allégé par `SetTransform` (ni managedFields, ni volumes, ni env, ni templates). Chaque événement marque des objets « sales », recalculés toutes les 100 ms depuis le cache ; seuls les vrais changements sont publiés. Le `displayStatus` est un portage de `printPod` de kubectl, et `requested` suit la règle de kube-scheduler (init containers, sidecars, overhead). metrics-server est relevé toutes les 15 s.
- **Le simulateur** (`internal/demo`) produit le même modèle et passe par le même hub (`internal/stream`) : le front ne sait pas s'il regarde un vrai cluster.
- **Le front** (`web/`, React + React Three Fiber) garde l'état dans un store Zustand indexé par UID. La scène lit le store dans sa boucle de rendu sans re-render React par message. Robots et bâtiments sont des `InstancedMesh`, avec un draw call par pièce quel que soit le nombre de pods.
- **Sécurité navigateur** : CSP stricte (`'self'` partout, aucun asset inline, polices auto-hébergées) et vérification de l'`Origin` à l'ouverture du WebSocket.

```text
cmd/atlas/         commande, flags (--demo, --addr, --cluster-name ; env ATLAS_*)
internal/model/    modèle réduit envoyé au front (Node, Pod, Workload, Metrics)
internal/stream/   hub snapshot + deltas, handler WebSocket
internal/kube/     informers, conversions, displayStatus, metrics-server
internal/demo/     cluster simulé
internal/server/   routeur HTTP, CSP, SPA
web/src/api/       types du protocole, client de stream
web/src/store/     état normalisé, bandeau d'événements
web/src/scene/     ville, bâtiments, robots, sélection, postures, disposition
web/src/ui/        barre du haut, stats, filtres, inspecteur
```

## Avancement

| Jalon | Contenu | État |
| --- | --- | --- |
| 1 | Squelette, binaire unique, scène 3D, mode démo | fait |
| 2 | Informers, `displayStatus`, flux branché sur un cluster kind | fait |
| 3 | Image, chart Helm, déploiement in-cluster | à venir |
| 4 | OIDC, sessions, impersonation, filtrage par droits | à venir |
| 5 | Inspecteur : logs, YAML, événements | à venir |
| 6 | Terminal et actions, audit | à venir |
| 7 | Échelle (LOD, regroupement, rendu à la demande), recherche, vue Liste, CI | à venir |

Choix propres au jalon 1, détaillés dans [`docs/superpowers/plans/2026-10-06-jalon-1-squelette-demo.md`](docs/superpowers/plans/2026-10-06-jalon-1-squelette-demo.md) :

- L'inspecteur n'a que l'onglet **Aperçu**, en lecture seule. Logs, terminal, YAML et événements arrivent au jalon 5, les actions au jalon 6.
- Les animations des robots sont calculées sur CPU dans les matrices d'instance. Le passage au vertex shader prévu par la spec sera fait au jalon 7 si le test de charge à 3 000 pods l'exige.
- La couleur d'un namespace vient de son annotation `atlas.io/color`, sinon d'un hash stable de son nom ; deux namespaces visibles ne partagent pas une teinte tant qu'il en reste dans la palette.

Choix du jalon 2 ([plan](docs/superpowers/plans/2026-10-06-jalon-2-lecture-cluster.md)) :

- ArgoCD : l'application vient de l'annotation `argocd.argoproj.io/tracking-id` ou du label `argocd.argoproj.io/instance`. Le statut de sync demanderait la lecture des `Application`, absente du ClusterRole de la spec : l'inspecteur affiche « Géré par ArgoCD ».
- Le propriétaire racine d'un pod de CronJob est son Job (les CronJobs ne sont pas dans le ClusterRole).
- Un node est dessiné en bâtiment GPU s'il a de la ressource `nvidia.com/gpu` ou le taint `nvidia.com/gpu`.
