# Cluster Atlas

Console Kubernetes web, déployée dans le cluster qu'elle observe, qui montre l'état du cluster en temps réel sous la forme d'une ville isométrique en 3D : chaque node pool est un quartier, chaque node un bâtiment, chaque pod un petit robot dont la couleur indique le namespace et l'antenne le statut.

La spécification complète est dans [`docs/spec.md`](docs/spec.md) et le prototype d'origine dans [`docs/prototype.html`](docs/prototype.html).

## Démarrer

Prérequis : Go 1.23+, Node 22.

```sh
make demo        # compile le front et le binaire, puis lance atlas --demo sur :8080
```

Ouvrez http://localhost:8080 : la ville tourne sur un cluster simulé (6 nodes, une trentaine de pods, un pod en CrashLoopBackOff, un en ImagePullBackOff, un Job périodique, des rollouts en staging).

Pour développer le front avec rechargement à chaud :

```sh
make dev         # backend démo sur :8080 + Vite sur :5173 (proxy /api)
```

## Tests

```sh
make test        # go vet + go test -race, puis Vitest
make e2e         # Playwright contre le binaire en mode démo (installe d'abord : cd web && npx playwright install chromium)
```

## Architecture du jalon 1

- **Un binaire Go** (`cmd/atlas`) sert le front compilé (`embed.FS`), les probes `/healthz` et `/readyz`, `/api/me` et le flux `/api/stream`.
- **`/api/stream`** (WebSocket) envoie un snapshot puis des deltas `upsert`/`delete` regroupés toutes les 250 ms et numérotés (`rev`). À la reconnexion, le client renvoie son dernier `rev` et reçoit seulement les deltas manquants, ou un nouveau snapshot si l'écart est trop grand. Les métriques arrivent toutes les 15 s.
- **Le simulateur** (`internal/demo`) produit exactement le modèle réduit que produiront les informers au jalon 2, et passe par le même hub (`internal/stream`). Le front ne sait pas s'il regarde un vrai cluster.
- **Le front** (`web/`, React + React Three Fiber) garde l'état dans un store Zustand indexé par UID. La scène lit le store dans sa boucle de rendu sans re-render React par message. Robots et bâtiments sont des `InstancedMesh`, avec un draw call par pièce quel que soit le nombre de pods.
- **Sécurité navigateur** : CSP stricte (`'self'` partout, aucun asset inline, polices auto-hébergées) et vérification de l'`Origin` à l'ouverture du WebSocket.

```text
cmd/atlas/         commande, flags (--demo, --addr, --cluster-name ; env ATLAS_*)
internal/model/    modèle réduit envoyé au front (Node, Pod, Workload, Metrics)
internal/stream/   hub snapshot + deltas, handler WebSocket
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
| 2 | Informers, `displayStatus`, flux branché sur un cluster kind | à venir |
| 3 | Image, chart Helm, déploiement in-cluster | à venir |
| 4 | OIDC, sessions, impersonation, filtrage par droits | à venir |
| 5 | Inspecteur : logs, YAML, événements | à venir |
| 6 | Terminal et actions, audit | à venir |
| 7 | Échelle (LOD, regroupement, rendu à la demande), recherche, vue Liste, CI | à venir |

Choix propres au jalon 1, détaillés dans [`docs/superpowers/plans/2026-10-06-jalon-1-squelette-demo.md`](docs/superpowers/plans/2026-10-06-jalon-1-squelette-demo.md) :

- L'inspecteur n'a que l'onglet **Aperçu**, en lecture seule. Logs, terminal, YAML et événements arrivent au jalon 5, les actions au jalon 6.
- Les animations des robots sont calculées sur CPU dans les matrices d'instance. Le passage au vertex shader prévu par la spec sera fait au jalon 7 si le test de charge à 3 000 pods l'exige.
- La couleur d'un namespace vient d'un hash stable de son nom ; deux namespaces visibles ne partagent pas une teinte tant qu'il en reste dans la palette. L'annotation `atlas.io/color` sera prise en compte quand les objets Namespace entreront dans le modèle (jalon 2).
- Sans `--demo`, le binaire refuse de démarrer : le mode cluster arrive au jalon 2.
