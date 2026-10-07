# Cluster Atlas

Console Kubernetes web, déployée dans le cluster qu'elle observe, qui montre l'état du cluster en temps réel sous la forme d'une ville isométrique en 3D : chaque node pool est un quartier, chaque node un bâtiment, chaque pod un bloc ; les Services sont des relais sur les avenues, les entrées (Ingress, IngressRoute Traefik) des portes à l'ouest, les PVC des citernes dans le quartier Entrepôts.

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
make scenarios   # CrashLoopBackOff, ImagePullBackOff, Pending, non ready, StatefulSet, DaemonSet, CronJob ; réseau et stockage
make run-kind    # atlas --auth-mode=none --context kind-atlas sur :8080
make kind-down
```

`make scenarios` installe aussi la CRD Traefik (`ingressroutes.traefik.io`, sans contrôleur) et ajoute des Services (sain, en panne, headless, ExternalName), un Ingress nginx avec un backend manquant, une IngressRoute vers un Service introuvable, un StatefulSet avec ses PVC et un PVC en attente de consommateur.

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

Le chart crée le Deployment (image distroless `ghcr.io/no-inspi/atlas-k8s`, non-root, système de fichiers en lecture seule), le ServiceAccount et son ClusterRole, le Service, le Secret (clé de chiffrement des cookies générée puis conservée aux upgrades), une NetworkPolicy et, au choix, un Ingress ou un HTTPRoute. Valeurs principales :

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

**Droits du ServiceAccount** : lecture de nodes, pods, namespaces, events, Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs et metrics.k8s.io ; `get`, `list` et `watch` sur services, persistentvolumeclaims, ingresses et ingressroutes (`traefik.io`, `traefik.containo.us`), `list` et `watch` sur endpointslices et ingressclasses (le `get` sert l'onglet YAML en `auth.mode=none`) ; `create` sur `subjectaccessreviews` ; `impersonate` sur users et groups (restreignable par `rbac.impersonate`). Aucun droit d'écriture sur les workloads, ni `pods/exec`, ni secrets, ni configmaps : logs, exec et actions passent toujours par impersonation de l'utilisateur. Un type réseau ou stockage que le ServiceAccount ne peut pas lister (sonde bornée à 10 s) est désactivé, avec un warning dans les logs ; sans EndpointSlices, les Services sont désactivés aussi. Une CRD Traefik installée après le démarrage est prise en compte au prochain redémarrage.

**NetworkPolicy** : entrée HTTP seulement depuis `networkPolicy.allowFrom`, métriques depuis `networkPolicy.metricsFrom` ; sortie vers le DNS du cluster, l'API server (IP lues sur l'EndpointSlice `default/kubernetes` à l'installation, ou `networkPolicy.apiServer.cidrs`) et l'issuer OIDC. Après un changement d'IP de l'API server, relancez `helm upgrade`.

**Timeouts WebSocket** : le flux, les logs et le terminal sont des WebSockets de longue durée. Le backend envoie un ping toutes les 30 s, mais gardez des timeouts d'au moins 3 600 s côté proxy :

- **ingress-nginx** : posés automatiquement par le chart quand `ingress.className=nginx` (`nginx.ingress.kubernetes.io/proxy-read-timeout` et `proxy-send-timeout` à `3600`).
- **Traefik** : pas de coupure des WebSockets par défaut ; si les `respondingTimeouts` de l'entryPoint ont été réduits, gardez-les à `3600s` ou plus.
- **GKE Gateway** : ajoutez une `GCPBackendPolicy` sur le Service avec `spec.default.timeoutSec: 3600` ; le HTTPRoute du chart porte déjà `timeouts.request: 3600s`.

**Métriques** : Prometheus sur le port `9090` (`/metrics`), séparé du port public pour que l'Ingress ne les expose pas ; `metrics.serviceMonitor.enabled=true` crée un ServiceMonitor.

**Depuis son poste** : copiez `deploy.local.mk.example` en `deploy.local.mk` (registre, contexte kube, fichier de valeurs ; ignoré par git), puis `make deploy` construit l'image, la pousse et fait le `helm upgrade`. L'arbre de travail doit être commité : le tag de l'image est le commit.

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

**Performance.** `atlas --demo-scale 100x30` simule 100 nodes et 3 000 pods (avec environ 470 Services, 160 routes et 230 PVC) ; `?perf=1` dans l'URL affiche images/s, temps de frame, coût des robots et draw calls. Sur un vrai cluster, `make load-up` crée 100 nodes [kwok](https://kwok.sigs.k8s.io/) et 3 000 pods `pause` dans le cluster kind, plus 4 Services par Deployment (400) et 150 PVC sans consommateur (`NODES=…`, `PODS=…`, `SERVICES_PER_DEPLOY=…`, `PVCS=…` pour changer), `make load-down` les supprime.

**CI** (`.github/workflows/`) : `ci.yml` sur chaque push et pull request (gofmt, `go vet`, `go test -race`, tests et `helm lint` du chart, `tsc`, Vitest, e2e Playwright en mode démo, image multi-arch, Trivy) ; `release.yml` sur un tag `vX.Y.Z` pousse l'image `ghcr.io/<owner>/cluster-atlas` (amd64 et arm64) et le chart `oci://ghcr.io/<owner>/charts/cluster-atlas`. Les workflows sont vérifiés localement par `go run github.com/rhysd/actionlint/cmd/actionlint@latest`.

## Architecture

- **Un binaire Go** (`cmd/atlas`) sert le front compilé (`embed.FS`), les probes `/healthz` et `/readyz`, `/api/me` et le flux `/api/stream`.
- **`/api/stream`** (WebSocket) envoie un snapshot puis des deltas `upsert`/`delete` regroupés toutes les 250 ms et numérotés (`rev`). À la reconnexion, le client renvoie son dernier `rev` et reçoit seulement les deltas manquants, ou un nouveau snapshot si l'écart est trop grand. Les métriques arrivent toutes les 15 s.
- **La source Kubernetes** (`internal/kube`) : un informer partagé par type (nodes, pods, namespaces, Deployments, ReplicaSets, StatefulSets, DaemonSets, Jobs), allégé par `SetTransform` (ni managedFields, ni volumes, ni env, ni templates). Chaque événement marque des objets « sales », recalculés toutes les 100 ms depuis le cache ; seuls les vrais changements sont publiés. Le `displayStatus` est un portage de `printPod` de kubectl, et `requested` suit la règle de kube-scheduler (init containers, sidecars, overhead). metrics-server est relevé toutes les 15 s.
- **Réseau et stockage** (`internal/kube/source_net.go`, `traefik.go`) : Services (santé et endpoints lus dans les EndpointSlices, endpoints en arrêt ignorés), routes (Ingress et IngressRoute ramenées à une porte : IngressClass, annotation, classe par défaut, ou `traefik`) et PVC (pods qui les montent, lus dans le cache des pods) sont trois kinds du flux, calculés par la même boucle d'objets sales. Côté front, `netLayout.ts` range les relais par namespace sur les avenues (pas de 1,3, resserré jusqu'à 0,65, puis les plus gros namespaces repliés chacun en un bloc avec compteur ; deux objets ne partagent jamais une place), les portes à l'entrée ouest et les citernes par StorageClass (colonnes ajustées pour que les entrepôts restent à peu près aussi profonds que la ville ; PVC Pending en orange translucide) ; `links.ts` trace les liens en angles droits par les rues et `GroundLinks.tsx` les dessine. Sélectionner un objet allume son chemin (porte → Service → pods → PVC). Lignes principales et conduites sont toujours visibles, les fibres Service → pods seulement au survol ou à la sélection ; les paquets défilent à 15 images/s, et s'arrêtent de loin. Les chips de namespace estompent aussi relais, citernes et liens au sol.
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
- **Échelle** : de loin, un robot est un cube (une pièce au lieu de treize) ; fumée et ventilateurs seulement de près. Au-delà de 48 pods, un node montre une pile par workload (ou par namespace s'il y en a trop) avec un compteur. Le rendu est à la demande : une frame par changement d'état, mouvement de caméra ou animation en cours, aucune quand rien ne bouge ; les animations au repos s'arrêtent avec `prefers-reduced-motion` ou onglet caché.
- **Recherche, vue Liste, thème** : `/` cherche un pod, un node ou un workload, centre la caméra et sélectionne. La vue Liste est un arbre namespace → workload → pod, plus les nodes, navigable au clavier (rôle `tree`) et ouvre le même inspecteur. Thème système, clair ou sombre, mémorisé dans le navigateur.
- **Liens profonds** : `/pods/{namespace}/{nom}`, `/nodes/{nom}`, `/services/{namespace}/{nom}`, `/routes/{ingress|ingressroute}/{namespace}/{nom}`, `/volumes/{namespace}/{nom}` et `/gates/{nom}` ouvrent l'inspecteur sur l'objet ; l'URL suit la sélection.
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
web/src/ui/        barre du haut, stats, filtres, recherche, vue Liste, thème, liens profonds
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
| 7 | Échelle (LOD, regroupement, rendu à la demande), recherche, vue Liste, CI | fait |
| 8 | Réseau et stockage : Services, Ingress et IngressRoute, PVC dans la ville | fait |

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

Choix du jalon 7 ([plan](docs/superpowers/plans/2026-10-06-jalon-7-echelle-finition.md)) :

- Pas de vertex shader pour les animations : la mesure ne l'exige pas (mise à jour CPU des robots : 1 ms de loin, 3 ms de près, à 3 000 pods).
- **Hors MVP**, en plus de la liste de la spec : supprimer un node via le fournisseur (NodeClaim Karpenter, node pool GKE). `kubectl delete node` ne suffit pas : le kubelet le réenregistre tant que la machine tourne.

Mesures (kind sur MacBook M4, Chrome) :

| Mesure | Résultat |
| --- | --- |
| 3 000 pods sur 100 nodes kwok, vue d'ensemble en rotation | 60 images/s, robots 1,0 ms par frame |
| idem, zoom rapproché | 60 images/s, robots 2,9 ms par frame |
| Premier snapshot affiché | 186 ms |
| Mémoire du backend à 3 142 pods | 92 Mi |
| Pod créé / supprimé visible dans la vue, sous charge | 337 ms / 239 ms |

Choix du jalon 8 ([design](docs/superpowers/specs/2026-10-07-jalon-8-reseau-stockage-design.md), [plan](docs/superpowers/plans/2026-10-07-jalon-8-reseau-stockage.md)) :

- Lecture seule : aucune action nouvelle. Les portes ne sont pas des objets du flux, le front les déduit des routes.
- Les routes vers un `TraefikService` sont marquées `indirect`, non résolues ; IngressRouteTCP/UDP, Gateway API et remplissage des citernes sont hors jalon.

Mesures du banc (Chrome piloté par Playwright, GPU matériel Apple M4 via ANGLE Metal, écran 120 Hz donc plafond à 120 images/s, fenêtre 1440 × 900 ; indicatives) :

| Cas | Au repos | Rotation continue | Draw calls |
| --- | --- | --- | --- |
| kind + kwok : 105 nodes, 3 144 pods, 412 Services, 153 PVC, vue par défaut | 120 images/s (8,3 ms) | 120 images/s, robots 1,1 à 1,5 ms | ≈ 400 |
| idem, relais `load-test/load-001-1` sélectionné (30 pods allumés) | 120 images/s | 120 images/s, robots 1,5 à 2,1 ms | 400 |
| idem, dézoom maximal | 120 images/s | 119 images/s | 399 |
| démo `--demo-scale 100x30` : 100 nodes, 2 975 pods, 470 Services, 157 routes, 232 PVC, vue par défaut | 120 images/s | 120 images/s, robots 1,2 à 1,4 ms | ≈ 720 |
| idem, relais `production/api-gateway` sélectionné / dézoom maximal | 120 images/s | 120 images/s, robots jusqu'à 2,7 ms | 721 |
| les mêmes cas avec `prefers-reduced-motion` (sans pods ni paquets animés) | 0 image/s hors transitoires (arrivées de pods, flux) | 119 à 120 images/s | — |

Au repos, la ville est redessinée à pleine cadence tant qu'un pod s'anime (CrashLoopBackOff, ImagePullBackOff, Pending : il y en a dans les deux bancs) : limite connue de `Pods.tsx`, antérieure à ce jalon, qui redemande une frame à chaque image pendant ces animations. Sans elles, le rendu à la demande tient (aucune frame au repos) ; les paquets seuls tournent à 15 images/s de près et s'arrêtent de loin.

## Critères d'acceptation

Vérifiés sur kind avec Dex (`make e2e-auth`, in-cluster et en local) ; le passage sur un cluster GKE réel reste à faire.

- [x] `helm install` sur un cluster vierge donne une URL fonctionnelle en moins de 2 min, sans `kubectl` (≈ 7 s sur kind, image déjà présente).
- [x] Un pod créé, supprimé ou qui change de statut apparaît en moins de 2 s (0,1 à 0,35 s, `make test-integration`).
- [x] L'utilisateur `view` voit les pods et lit les logs ; actions et terminal sont désactivés avec une explication ; un appel forcé renvoie 403.
- [x] L'utilisateur sans droits sur `kube-system` ne reçoit aucun objet de ce namespace dans le flux.
- [x] Le terminal ouvre un shell, gère le redimensionnement, Ctrl+C, Tab, les flèches, et se ferme à `exit`.
- [x] Les logs suivent un pod en direct ; « instance précédente » montre les logs d'avant le crash.
- [x] Le YAML est celui du Deployment propriétaire, sans `managedFields`, avec ArgoCD quand il existe (application seulement, voir jalon 2).
- [x] Un drain respecte les PDB et ignore les DaemonSets ; les robots évincés réapparaissent ailleurs.
- [x] Chaque action et chaque session exec produisent une ligne d'audit.
- [x] 60 images/s à 3 000 pods ; mémoire du backend sous 300 Mi (92 Mi). Mesuré sur M4 : à confirmer sur un M1.
- [x] Pod non-root, système de fichiers en lecture seule, Trivy sans vulnérabilité critique.
