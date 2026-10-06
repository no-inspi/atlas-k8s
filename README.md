# Cluster Atlas

Console Kubernetes web, déployée dans le cluster qu'elle observe, qui montre l'état du cluster en temps réel sous la forme d'une ville isométrique en 3D : chaque node pool est un quartier, chaque node un bâtiment, chaque pod un petit robot dont la couleur indique le namespace et l'antenne le statut.

La spécification complète est dans [`docs/spec.md`](docs/spec.md) et le prototype d'origine dans [`docs/prototype.html`](docs/prototype.html).

## Démarrer

Prérequis : Go 1.26 (exigé par client-go v0.37), Node 22 ; Docker et kind pour un vrai cluster.

```sh
make demo        # compile le front et le binaire, puis lance atlas --demo sur :8080
```

Ouvrez http://localhost:8080 : la ville tourne sur un cluster simulé (6 nodes, une trentaine de pods, un pod en CrashLoopBackOff, un en ImagePullBackOff, un Job périodique, des rollouts en staging).

Pour développer le front avec rechargement à chaud sur le cluster simulé :

```sh
make dev-demo    # backend démo sur :8080 + Vite sur :5173 (proxy /api et /auth)
```

### Avec authentification (kind + Dex)

```sh
make kind-up scenarios   # une fois
make dev                 # Dex sur :5556, backend OIDC, Vite : http://localhost:5173
```

Deux comptes de test, mot de passe `password` :

| Compte | Groupe impersonné | Droits (hack/dev-rbac.yaml) | Ce qu'il voit |
| --- | --- | --- | --- |
| `alice@example.com` | `oidc:sre` | `view` sur tout le cluster + lecture des nodes | tous les namespaces, les nodes |
| `bob@example.com` | `oidc:dev` | `edit` dans `production` et `staging` | ces deux namespaces, bâtiments anonymes à la place des nodes ; terminal et actions dans ses namespaces |
| `carol@example.com` | `oidc:ops` | `cluster-admin` | tout ; cordon, uncordon, drain |

Le rôle `view` de Kubernetes ne couvre pas les nodes (ressource cluster-scoped) : sans droit de les lister, la ville dessine des bâtiments anonymes à partir du `nodeName` des pods visibles.

Parcours complet dans le cluster, par URL (ingress-nginx sur le port 80, Dex dans le cluster) :

```sh
make kind-down kind-up scenarios kind-oidc helm-kind-oidc
# http://atlas.localtest.me
```

### Sur un vrai cluster (kind)

```sh
make kind-up     # cluster « atlas » : control-plane + 4 workers en 3 pools, taint GPU, metrics-server
make scenarios   # CrashLoopBackOff, ImagePullBackOff, Pending, non ready, StatefulSet, DaemonSet, CronJob
make run-kind    # atlas --auth-mode=none --context kind-atlas sur :8080
make kind-down
```

Sans `--demo`, atlas lit le cluster de son kubeconfig (ou en in-cluster quand il tourne dans un pod). Le mode par défaut est `oidc` ; `--auth-mode=none` (sans authentification ni impersonation) est réservé au développement.

| Flag | Variable | Rôle |
| --- | --- | --- |
| `--demo` | `ATLAS_DEMO=true` | cluster simulé |
| `--auth-mode` | `ATLAS_AUTH_MODE` | `oidc` ou `none` |
| `--oidc-issuer-url`, `--oidc-client-id`, `--oidc-client-secret` | `ATLAS_OIDC_*` | client OIDC (secret : préférer la variable d'environnement) |
| `--oidc-username-claim`, `--oidc-groups-claim`, `--oidc-groups-prefix`, `--oidc-scopes` | `ATLAS_OIDC_*` | `email`, `groups`, `oidc:`, `openid,email,profile` |
| `--public-url` | `ATLAS_PUBLIC_URL` | URL vue du navigateur (URL de retour OIDC) |
| `--cookie-key` | `ATLAS_COOKIE_KEY` | base64 de 32 octets (`openssl rand -base64 32`) |
| `--session-ttl` | `ATLAS_SESSION_TTL` | `8h` |
| `--kubeconfig`, `--context` | `KUBECONFIG`, `ATLAS_CONTEXT` | cluster à lire hors in-cluster |
| `--pool-label` | `ATLAS_POOL_LABEL` | label du node pool (sinon détection GKE, Karpenter, EKS, type d'instance) |
| `--cluster-name` | `ATLAS_CLUSTER_NAME` | nom affiché |
| `--addr` | `ATLAS_ADDR` | adresse d'écoute (`:8080`) |

## Déployer dans un cluster

```sh
helm install cluster-atlas deploy/helm/cluster-atlas -n cluster-atlas --create-namespace -f values.yaml
```

Le chart crée le Deployment (image distroless `ghcr.io/no-inspi/cluster-atlas`, non-root, système de fichiers en lecture seule), le ServiceAccount et son ClusterRole, le Service, le Secret (clé de chiffrement des cookies générée puis conservée aux upgrades), une NetworkPolicy et, au choix, un Ingress ou un HTTPRoute. Valeurs principales :

```yaml
clusterName: gke-prod-europe-west1
auth:
  mode: oidc               # none : développement local, refusé si l'application est exposée
  oidc:
    issuerURL: https://accounts.google.com
    clientID: "…"
    existingSecret: ""     # sinon clientSecret ; clés client-secret et cookie-key
    groupsPrefix: "oidc:"
ingress:
  enabled: true
  className: nginx
  host: atlas.example.com
  certManager:
    clusterIssuer: letsencrypt
networkPolicy:
  allowFrom:               # namespace du contrôleur d'ingress ou de la Gateway
    - namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: ingress-nginx } }
```

Toutes les options sont commentées dans [`values.yaml`](deploy/helm/cluster-atlas/values.yaml) et typées par `values.schema.json`.

**Droits du ServiceAccount** : lecture de nodes, pods, namespaces, events, Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs et metrics.k8s.io ; `create` sur `subjectaccessreviews` ; `impersonate` sur users et groups (restreignable par `rbac.impersonate`). Aucun droit d'écriture sur les workloads, ni `pods/exec`, ni secrets, ni configmaps : logs, exec et actions passent toujours par impersonation de l'utilisateur.

**NetworkPolicy** : entrée HTTP seulement depuis `networkPolicy.allowFrom`, métriques depuis `networkPolicy.metricsFrom` ; sortie vers le DNS du cluster, l'API server (IP lues sur l'EndpointSlice `default/kubernetes` à l'installation, ou `networkPolicy.apiServer.cidrs`) et l'issuer OIDC. Après un changement d'IP de l'API server, relancez `helm upgrade`.

**Timeouts WebSocket** : le flux, les logs et le terminal sont des WebSockets de longue durée. Le backend envoie un ping toutes les 30 s, mais gardez des timeouts d'au moins 3 600 s côté proxy :

- **ingress-nginx** : posés automatiquement par le chart quand `ingress.className=nginx` (`nginx.ingress.kubernetes.io/proxy-read-timeout` et `proxy-send-timeout` à `3600`).
- **Traefik** : pas de coupure des WebSockets par défaut ; si les `respondingTimeouts` de l'entryPoint ont été réduits, gardez-les à `3600s` ou plus.
- **GKE Gateway** : ajoutez une `GCPBackendPolicy` sur le Service avec `spec.default.timeoutSec: 3600` ; le HTTPRoute du chart porte déjà `timeouts.request: 3600s`.

**Métriques** : Prometheus sur le port `9090` (`/metrics`), séparé du port public pour que l'Ingress ne les expose pas ; `metrics.serviceMonitor.enabled=true` crée un ServiceMonitor.

Sur kind, `make helm-kind` construit l'image, la charge dans le cluster et installe le chart (sans OIDC jusqu'au jalon 4, donc sans exposition : `kubectl -n cluster-atlas port-forward svc/cluster-atlas 8080`).

## Tests

```sh
make test        # go vet + go test -race, puis Vitest
make e2e         # Playwright contre le binaire en mode démo (installe d'abord : cd web && npx playwright install chromium)
make test-integration  # sur kind : un pod créé ou supprimé est diffusé en moins de 2 s
make e2e-auth    # sur kind + Dex : alice et bob se connectent, bob ne reçoit aucune frame de kube-system
ATLAS_URL=http://atlas.localtest.me npx playwright test -c playwright.auth.config.ts  # idem, in-cluster (depuis web/)
make scan        # image + Trivy (échoue sur une vulnérabilité critique)
```

## Architecture

- **Un binaire Go** (`cmd/atlas`) sert le front compilé (`embed.FS`), les probes `/healthz` et `/readyz`, `/api/me` et le flux `/api/stream`.
- **`/api/stream`** (WebSocket) envoie un snapshot puis des deltas `upsert`/`delete` regroupés toutes les 250 ms et numérotés (`rev`). À la reconnexion, le client renvoie son dernier `rev` et reçoit seulement les deltas manquants, ou un nouveau snapshot si l'écart est trop grand. Les métriques arrivent toutes les 15 s.
- **La source Kubernetes** (`internal/kube`) : un informer partagé par type (nodes, pods, namespaces, Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs), allégé par `SetTransform` (ni managedFields, ni volumes, ni env, ni templates). Chaque événement marque des objets « sales », recalculés toutes les 100 ms depuis le cache ; seuls les vrais changements sont publiés. Le `displayStatus` est un portage de `printPod` de kubectl, et `requested` suit la règle de kube-scheduler (init containers, sidecars, overhead). metrics-server est relevé toutes les 15 s.
- **Le simulateur** (`internal/demo`) produit le même modèle et passe par le même hub (`internal/stream`) : le front ne sait pas s'il regarde un vrai cluster.
- **Le front** (`web/`, React + React Three Fiber) garde l'état dans un store Zustand indexé par UID. La scène lit le store dans sa boucle de rendu sans re-render React par message. Robots et bâtiments sont des `InstancedMesh`, avec un draw call par pièce quel que soit le nombre de pods.
- **Inspecteur** (`internal/inspect`, `internal/logs`) : chaîne de propriétaires, YAML (sans `managedFields`), logs et événements. Propriétaires, YAML et logs passent par le client impersonné de l'utilisateur ; les événements viennent d'un cache partagé, après vérification de son droit `list events`. Les logs passent par un WebSocket avec backpressure : 2 000 lignes en attente au plus côté serveur, les lignes en trop sont comptées et signalées. Les erreurs de l'API server (403, 404) sont affichées telles quelles.
- **Terminal** (`internal/exec`) : `pods/exec` au nom de l'utilisateur, protocole WebSocket `v5.channel.k8s.io` avec repli SPDY ; `/bin/bash` puis `/bin/sh` ; xterm.js côté navigateur (stdin/stdout binaires, redimensionnement en JSON). Désactivable (`features.exec.enabled`), interdit dans `features.exec.deniedNamespaces`, fermé après `features.exec.idleTimeout` d'inactivité.
- **Actions** (`internal/actions`) : supprimer un pod, scale, rollout restart, cordon, uncordon, drain, toujours par impersonation. Confirmation pour supprimer, scaler à 0 et drainer (récapitulatif, puis saisie du nom court du node) ; boutons grisés avec l'explication « Vous n'avez pas le droit … » d'après une revue d'accès ; résultat en notification et dans le bandeau. Le drain cordonne, puis évince par l'API Eviction en respectant les PDB (refus réessayés 60 s) et laisse les pods de DaemonSet. `features.actions.enabled: false` donne une console en lecture seule.
- **Audit** (`internal/audit`) : une ligne JSON sur stdout par action et par ouverture ou fermeture de session exec, repérable par `"audit":true` :

  ```json
  {"time":"2026-10-06T10:31:02Z","audit":true,"user":"bob@example.com","groups":["oidc:dev"],"verb":"delete","resource":"pods","namespace":"production","name":"api-gateway-7f9-x","result":"success","durationMs":42}
  ```

  `result` vaut `success`, `forbidden` ou `failure` (avec `error`), et `requested` pour `exec-open`, dont l'issue est portée par `exec-close`. Les commandes tapées dans le terminal ne sont pas enregistrées.
- **Liens profonds** : `/pods/{namespace}/{nom}` et `/nodes/{nom}` ouvrent l'inspecteur sur l'objet ; l'URL suit la sélection.
- **Sécurité navigateur** : CSP stricte pour les scripts (`script-src 'self'`, aucun script externe), polices auto-hébergées ; styles en ligne autorisés pour Monaco (voir plus bas), vérification de l'`Origin` à l'ouverture du WebSocket, en-tête `X-Atlas-Request` exigé sur toute requête mutante (CSRF).
- **Authentification** (`internal/auth`) : OIDC code + PKCE ; session dans un cookie chiffré AES-256-GCM (`HttpOnly`, `SameSite=Lax`, `Secure` en https), sans token côté navigateur. Un nom d'utilisateur `system:*` est refusé et les groupes `system:*` ignorés ; tous les groupes sont préfixés (`oidc:`).
- **Droits** (`internal/access`) : chaque appel à l'API server pour un utilisateur passe par un client impersonné (`Impersonate-User`, `Impersonate-Group`). Le flux est filtré par client, hors du verrou du hub : un objet n'est envoyé que si l'utilisateur peut le lister dans son namespace (SubjectAccessReview, cache 60 s). Si ses droits changent, le flux se ferme (code 4000) et le front repart d'un snapshot ; à l'expiration de la session, code 4401 et retour à la connexion.

```text
cmd/atlas/         commande, flags (--demo, --addr, --cluster-name ; env ATLAS_*)
internal/model/    modèle réduit envoyé au front (Node, Pod, Workload, Metrics)
internal/stream/   hub snapshot + deltas, handler WebSocket
internal/kube/     informers, conversions, displayStatus, metrics-server
internal/demo/     cluster simulé
internal/auth/     OIDC, session, CSRF ; authtest/ : IdP de test
internal/access/   SubjectAccessReview, filtre du flux, clients impersonnés
internal/inspect/  contrat de l'inspecteur (propriétaires, YAML, événements, logs)
internal/logs/     WebSocket des logs avec backpressure
internal/server/   routeur HTTP, CSP, SPA, métriques
deploy/helm/       chart Helm et ses tests (helm template)
web/src/api/       types du protocole, client de stream
web/src/store/     état normalisé, bandeau d'événements
web/src/scene/     ville, bâtiments, robots, sélection, postures, disposition
web/src/inspector/ inspecteur : aperçu, logs (virtualisés), YAML (Monaco), événements
web/src/ui/        barre du haut, stats, filtres, liens profonds
```

## Avancement

| Jalon | Contenu | État |
| --- | --- | --- |
| 1 | Squelette, binaire unique, scène 3D, mode démo | fait |
| 2 | Informers, `displayStatus`, flux branché sur un cluster kind | fait |
| 3 | Image, chart Helm, déploiement in-cluster | fait |
| 4 | OIDC, sessions, impersonation, filtrage par droits | fait |
| 5 | Inspecteur : logs, YAML, événements | fait |
| 6 | Terminal et actions, audit | fait |
| 7 | Échelle (LOD, regroupement, rendu à la demande), recherche, vue Liste, CI | à venir |

Choix propres au jalon 1, détaillés dans [`docs/superpowers/plans/2026-10-06-jalon-1-squelette-demo.md`](docs/superpowers/plans/2026-10-06-jalon-1-squelette-demo.md) :

- L'inspecteur n'a que l'onglet **Aperçu**, en lecture seule. Logs, terminal, YAML et événements arrivent au jalon 5, les actions au jalon 6.
- Les animations des robots sont calculées sur CPU dans les matrices d'instance. Le passage au vertex shader prévu par la spec sera fait au jalon 7 si le test de charge à 3 000 pods l'exige.
- La couleur d'un namespace vient de son annotation `atlas.io/color`, sinon d'un hash stable de son nom ; deux namespaces visibles ne partagent pas une teinte tant qu'il en reste dans la palette.

Choix du jalon 2 ([plan](docs/superpowers/plans/2026-10-06-jalon-2-lecture-cluster.md)) :

- ArgoCD : l'application vient de l'annotation `argocd.argoproj.io/tracking-id` ou du label `argocd.argoproj.io/instance`. Le statut de sync demanderait la lecture des `Application`, absente du ClusterRole de la spec : l'inspecteur affiche « Géré par ArgoCD ».
- Le propriétaire racine d'un pod de CronJob est son Job (les CronJobs ne sont pas dans le ClusterRole).
- Un node est dessiné en bâtiment GPU s'il a de la ressource `nvidia.com/gpu` ou le taint `nvidia.com/gpu`.

Choix du jalon 3 ([plan](docs/superpowers/plans/2026-10-06-jalon-3-chart-helm.md)) :

- Par défaut, `ingress.enabled: false` : un `helm install` sans configuration OIDC n'expose rien.
- `/metrics` est servi sur un port dédié (9090) plutôt que sur le port public.
- La clé de cookie est le base64 de 32 octets aléatoires (elle passe en variable d'environnement).

Choix du jalon 4 ([plan](docs/superpowers/plans/2026-10-06-jalon-4-auth-droits.md)) :

- Pages et API protégées ; les assets du front (`/assets/*`) restent publics, ils ne contiennent aucun secret.
- `auth.oidc.scopes` vaut `openid email profile` par défaut (Google refuse `groups`) ; Dex, Keycloak et Entra ID acceptent `groups`.
- Avec `auth.mode=oidc`, le chart exige une URL publique : Ingress, HTTPRoute ou `publicURL`.

Choix du jalon 5 ([plan](docs/superpowers/plans/2026-10-06-jalon-5-inspecteur-lecture.md)) :

- **CSP et Monaco** : Monaco crée des `<style>` et des attributs `style` sans prise en charge de nonce (vérifié sur la 0.57) ; la CSP autorise donc `'unsafe-inline'` pour les styles, les scripts restant limités à `'self'`. Pour une CSP de styles stricte, CodeMirror 6 (qui accepte un nonce) remplacerait Monaco. Monaco (~730 Ko gzippés) n'est chargé qu'à l'ouverture de l'onglet YAML.
- Les événements sont rafraîchis toutes les 3 s tant que l'onglet est ouvert.

Choix du jalon 6 ([plan](docs/superpowers/plans/2026-10-06-jalon-6-terminal-actions.md)) :

- Le drain ignore aussi les pods statiques et ne force pas les pods sans contrôleur (comme `kubectl drain` sans `--force`) ; le récapitulatif les liste.
- Si le node drainé héberge Cluster Atlas lui-même, son pod est évincé en dernier, après la réponse (le chart lui donne son nom par la downward API) ; la page se reconnecte.
- Sans lecture des `Application` ArgoCD, l'avertissement de selfHeal s'affiche pour tout workload géré par ArgoCD.
