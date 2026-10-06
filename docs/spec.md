# Cluster Atlas — spécification technique

Oct 6, 2026 · @Charlie Apcher

## Contexte et objectif

Cluster Atlas est une console Kubernetes web, déployée dans le cluster qu'elle observe, qui représente l'état du cluster en temps réel sous forme de jeu de stratégie isométrique en 3D. Elle sert à la gestion opérationnelle du cluster (pods, nodes, workloads), pas au FinOps.

Un utilisateur ouvre une URL, s'authentifie via le SSO de l'organisation et voit le cluster vivant : chaque node est un bâtiment, chaque pod un petit robot. Un clic sur un pod donne ses logs en streaming, un shell dans le container (équivalent `kubectl exec -it`), le YAML du Deployment qui le gère et ses événements.

Un prototype interactif sur données simulées a validé l'UX ; cette spec décrit la version réelle.

**Principes non négociables**

- **In-cluster d'abord** : installé par un chart Helm, accessible par URL. Aucun kubeconfig ni `kubectl` sur le poste de l'utilisateur.
- **Le RBAC Kubernetes est la seule source de vérité des droits** : chaque requête vers l'API server est faite au nom de l'utilisateur connecté (impersonation). L'application n'accorde jamais un droit que l'utilisateur n'a pas.
- **L'API server est la seule source de données** : pas de base de données, l'état vient des watch Kubernetes.
- **Compatible GitOps** : l'application ne modifie pas les manifests gérés par ArgoCD ; elle les affiche et le signale.
- **La 3D sert la lecture** : chaque élément visuel encode une information (statut, charge, appartenance). Rien de purement décoratif ne doit gêner la lisibilité.

## Périmètre MVP

Le MVP couvre un seul cluster (celui où l'application est installée), les Deployments et leurs pods, les nodes, et six actions d'exploitation.

**Dans le MVP**

- Vue 3D temps réel des nodes (groupés par node pool) et des pods, avec filtres par namespace.
- Inspecteur de pod : aperçu, logs en streaming, terminal exec, YAML du workload propriétaire, événements.
- Inspecteur de node : capacité allouée, conditions, taints, liste des pods.
- Actions : supprimer un pod, scale d'un Deployment, rollout restart, cordon, uncordon, drain.
- Workloads reconnus : Deployment, ReplicaSet, StatefulSet, DaemonSet, Job, pods sans propriétaire.
- Authentification OIDC, impersonation, journal d'audit.

**Hors MVP (prévu plus tard)**

- Multi-cluster.
- Édition de YAML et apply depuis l'interface.
- Métriques historiques (graphiques sur 24 h) : le MVP n'affiche que l'instantané de metrics-server.
- Services, Ingress, PVC représentés dans la 3D.
- Port-forward, copie de fichiers, containers éphémères de debug.
- Coûts et FinOps.

## Architecture

Un seul pod applicatif dans le cluster sert le front et l'API ; le navigateur ne parle qu'à lui, jamais à l'API server.

&#91;embedded content: architecture in-cluster · 1 pod applicatif\]

Le ServiceAccount ne fait que lire le cache partagé des informers ; logs, exec et actions partent toujours au nom de l'utilisateur, donc le RBAC existant du cluster s'applique tel quel. Le backend est sans état : un redémarrage coupe les sessions exec et les flux, que le front rouvre automatiquement.

## Sécurité

Un shell dans un container depuis un navigateur est un accès privilégié : chaque appel à l'API server porte l'identité de l'utilisateur, et chaque action en écriture est journalisée.

**Authentification**

- OIDC (authorization code + PKCE) contre l'IdP de l'organisation (Google Workspace, Entra ID, Keycloak, Dex). Configuré par le chart : issuer, client ID, secret, claim des groupes.
- Session en cookie `HttpOnly`, `Secure`, `SameSite=Lax`, chiffré (clé dans un Secret). Durée configurable, 8 h par défaut. Pas de token dans le `localStorage`.
- Mode `auth.mode: none` réservé au développement local, refusé si `ingress.enabled: true`.

**Autorisation par impersonation**

- Le backend appelle l'API server avec son ServiceAccount en ajoutant `Impersonate-User: <email>` et `Impersonate-Group: <groupes OIDC>`.
- Le ServiceAccount n'a que le droit `impersonate` sur `users` et `groups` (restreignable par `resourceNames` et par préfixe de groupe), plus la lecture nécessaire au cache partagé (voir ci-dessous).
- L'interface masque ou désactive les actions refusées, en interrogeant `SelfSubjectAccessReview` au nom de l'utilisateur. Le refus final reste celui de l'API server.

**Cache partagé et filtrage**

Les informers (un watch par type de ressource, partagé entre tous les utilisateurs) tournent avec le ServiceAccount, en lecture seule. Avant d'envoyer un objet à un client, le backend vérifie que l'utilisateur a `list` sur ce type dans ce namespace (`SubjectAccessReview`, résultat mis en cache 60 s par utilisateur). Un utilisateur sans droits sur `kube-system` ne voit pas ses pods.

**Exec, logs, actions**

- `pods/exec`, `pods/log`, `delete`, `patch` passent toujours par impersonation, jamais par le cache.
- Option `features.exec.enabled` (défaut `true`) et liste de namespaces interdits à l'exec (défaut : `kube-system`).
- Timeout d'inactivité d'une session exec : 15 min par défaut.

**Audit**

Chaque action en écriture et chaque ouverture ou fermeture de session exec produit une ligne JSON sur stdout : horodatage, utilisateur, groupes, verbe, ressource, namespace, nom, résultat, durée. Les commandes tapées dans le shell ne sont pas enregistrées dans le MVP (l'audit log de l'API server couvre l'ouverture de session).

**Navigateur**

CSP stricte (aucun script externe, assets servis par le backend), protection CSRF par en-tête sur les requêtes mutantes, vérification de l'`Origin` à l'ouverture des WebSockets.

## API backend

Le front reçoit l'état du cluster par un WebSocket unique (snapshot puis deltas) ; logs et exec ont chacun leur WebSocket ; les actions passent par REST.

| Méthode | Route | Rôle |
| --- | --- | --- |
| GET | `/auth/login`, `/auth/callback`, `/auth/logout` | Flux OIDC |
| GET | `/api/me` | Utilisateur, groupes, nom du cluster |
| WS | `/api/stream` | Snapshot puis deltas de l'état du cluster, filtré par droits |
| GET | `/api/namespaces/{ns}/pods/{pod}/owner` | Chaîne de propriétaires (Pod › ReplicaSet › Deployment) |
| GET | `/api/yaml/{group}/{version}/{kind}/{ns}/{name}` | YAML nettoyé (sans `managedFields`) + infos ArgoCD |
| GET | `/api/namespaces/{ns}/pods/{pod}/events` | Événements du pod |
| WS | `/api/namespaces/{ns}/pods/{pod}/logs?container=&previous=&tailLines=500` | Logs en streaming |
| WS | `/api/namespaces/{ns}/pods/{pod}/exec?container=&command=/bin/sh` | Terminal interactif |
| POST | `/api/access-review` | Lot de `SelfSubjectAccessReview` pour griser les actions |
| DELETE | `/api/namespaces/{ns}/pods/{pod}` | Supprimer un pod |
| PATCH | `/api/namespaces/{ns}/{kind}/{name}/scale` | Scale (Deployment, StatefulSet) |
| POST | `/api/namespaces/{ns}/{kind}/{name}/restart` | Rollout restart (annotation `restartedAt`) |
| POST | `/api/nodes/{node}/cordon`, `/uncordon`, `/drain` | Gestion des nodes ; le drain utilise l'API Eviction et respecte les PDB |
| GET | `/healthz`, `/readyz`, `/metrics` | Probes et métriques Prometheus du backend |

**Flux `/api/stream`**

Messages JSON : `{"type":"snapshot","rev":N,"nodes":[…],"pods":[…],"workloads":[…]}` à la connexion, puis `{"type":"upsert"|"delete","kind":"pod"|"node"|"workload","rev":N,"obj":{…}}`. Les deltas sont regroupés toutes les 250 ms. Les métriques (metrics-server) arrivent toutes les 15 s dans un message `metrics`. À la reconnexion, le client renvoie son dernier `rev` ; si l'écart est trop grand, le serveur renvoie un snapshot.

**Modèle envoyé au front (réduit, pas l'objet Kubernetes complet)**

- `Node` : `name`, `pool` (label `cloud.google.com/gke-nodepool`, `karpenter.sh/nodepool`, `eks.amazonaws.com/nodegroup` ou `node.kubernetes.io/instance-type` en repli), `instanceType`, `zone`, `spot` (booléen), `gpu` (nombre), `allocatable` (cpu, mémoire, pods), `requested` (somme des requests), `conditions`, `taints`, `unschedulable`, `kubeletVersion`, `createdAt`.
- `Pod` : `uid`, `name`, `namespace`, `nodeName`, `phase`, `displayStatus` (le statut tel que `kubectl get pods` l'afficherait : `CrashLoopBackOff`, `ContainerCreating`, `Terminating`…), `restarts`, `containers` (nom, image, ready, state), `owner` (kind + name du workload racine), `requests`, `podIP`, `createdAt`, `qosClass`.
- `Workload` : `kind`, `name`, `namespace`, `replicas` désirés et prêts, `argocd` (application et statut de sync si l'objet porte les annotations ou labels ArgoCD).

**Logs** : le backend ouvre `pods/log?follow=true` au nom de l'utilisateur et relaie les lignes ; backpressure côté serveur (lignes ignorées et signalées si le client ne suit pas).

**Exec** : protocole Kubernetes `v5.channel.k8s.io` (WebSocket) via `remotecommand.NewWebSocketExecutor`, repli SPDY si l'API server ne le supporte pas. Côté navigateur, messages binaires stdin/stdout et un message JSON `resize` {cols, rows}. Shell essayé dans l'ordre `/bin/bash`, `/bin/sh` ; message clair si aucun n'existe (images distroless).

## Vue 3D

Le cluster est une ville vue en isométrique : les node pools sont des quartiers, les nodes des bâtiments posés sur une parcelle, les pods des robots qui vivent sur la parcelle de leur node.

**Correspondances**

| Objet Kubernetes | Représentation | Ce qui est encodé |
| --- | --- | --- |
| Node pool | Quartier (zone au sol teintée, nom peint au sol) | Regroupement des nodes |
| Node standard | Maison sur une parcelle, cheminée qui fume | Fumée proportionnelle au CPU demandé / allouable |
| Node spot / préemptible | Tente avec drapeau | Caractère temporaire du node |
| Node GPU | Bâtiment sombre avec ventilateurs sur le toit | Vitesse des ventilateurs selon la charge |
| Node cordonné | Barrière rayée devant la parcelle | `unschedulable: true` |
| Node NotReady | Bâtiment grisé, fenêtres éteintes, icône d'alerte | Condition `Ready` fausse |
| Pod | Robot (corps, tête, yeux, antenne lumineuse) | Couleur du corps = namespace ; antenne = statut |
| Pod de DaemonSet | Robot avec casque | Présent sur chaque node |
| Pod de StatefulSet | Robot avec sac à dos | Pod avec identité et stockage |
| Pod de Job | Robot avec casquette, disparaît une fois `Completed` | Tâche ponctuelle |
| Pods en attente | File d'attente devant la ville | Pods `Pending` sans `nodeName` |

**Statuts et postures**

| Statut affiché | Antenne | Posture |
| --- | --- | --- |
| Running (tous containers ready) | Verte | Balancement lent, clignement des yeux |
| Running mais non ready | Orange fixe | Immobile, tête baissée |
| Pending | Orange | Trépigne dans la file d'attente |
| ContainerCreating / PodInitializing | Orange | Tourne sur lui-même, taille réduite |
| CrashLoopBackOff / Error / OOMKilled | Rouge clignotante | Tombé à la renverse, corps qui pulse en rouge |
| ImagePullBackOff | Rouge | Assis, point d'interrogation au-dessus |
| Terminating | Grise | Rétrécit puis disparaît |
| Completed (Job) | Bleue | S'efface après 30 s |

**Disposition**

- Quartiers ordonnés par nom de pool ; nodes d'un pool en grille, triés par nom pour rester stables d'un rechargement à l'autre.
- Parcelle d'un node : grille de places, taille calculée sur le nombre max de pods du cluster par node (pas de redimensionnement en direct ; au-delà de 48 pods, regroupement par workload, voir Performance).
- Un pod garde sa place tant qu'il existe ; un nouveau pod prend la première place libre. Les déplacements (file → node) sont animés.
- Les couleurs de namespace sont dérivées d'un hash stable du nom dans une palette de 12 teintes ; surchargeables par l'annotation `atlas.io/color` sur le namespace.

**Interactions**

- Pivot, zoom, panoramique (souris, tactile, clavier). Caméra orthographique, angles bornés pour garder la vue isométrique.
- Clic sur un pod ou un node : ouvre l'inspecteur, marqueur au-dessus de l'objet, arcs qui relient le pod sélectionné à tous les autres pods du même workload.
- Chips de namespace : filtre (les autres pods passent à 10 % d'opacité) et compteur « N pods sur M nodes ».
- Recherche (`/`) par nom de pod, node ou workload : centre la caméra et sélectionne.
- Bandeau d'événements récents (créations, crashs, scheduling, évictions) en bas à gauche.
- Accessibilité : chaque objet est aussi listé dans un arbre accessible au clavier (vue « Liste ») qui ouvre le même inspecteur ; `prefers-reduced-motion` coupe les animations idle ; thème clair et sombre.

## Inspecteur

L'inspecteur est un panneau latéral sur desktop (≥ 761 px) et une feuille glissante en bas sur mobile ; il garde son onglet actif quand on change de pod.

**Pod**

- **Aperçu** : namespace, node (lien), IP, âge, QoS, redémarrages, containers (image, état, ready). Chaîne de propriétaires cliquable (Deployment › ReplicaSet › Pod). Répartition des replicas (« 3 replicas sur 3 nodes », liens vers les nodes). Usage CPU et mémoire vs requests et limits (metrics-server ; mention « indisponible » s'il est absent). Actions : supprimer le pod, scale −/+, rollout restart.
- **Logs** : sélecteur de container (init containers inclus), suivre (auto-scroll), instance précédente (`previous=true`, proposé si `restarts > 0`), filtre texte côté client, coloration par niveau (ERROR, WARN, INFO détectés par regex), téléchargement des lignes chargées. Rendu virtualisé, tampon de 5 000 lignes.
- **Terminal** : xterm.js avec addon fit, sélecteur de container, reconnexion. Si le pod n'est pas Running, message explicite au lieu du prompt (« container non démarré : CrashLoopBackOff »).
- **YAML** : YAML du workload racine (pas du pod) par défaut, bascule vers le pod ou le ReplicaSet. `managedFields` retirés, `status` repliable. Éditeur Monaco en lecture seule. Si l'objet est géré par ArgoCD : badge avec l'application et le statut de sync, et rappel que les modifications passent par Git.
- **Événements** : liste triée, plus récents en haut, `Warning` mis en évidence, mise à jour en direct.

**Node**

- **Aperçu** : pool, type d'instance, zone, spot, version kubelet, âge, conditions (Ready, MemoryPressure, DiskPressure, PIDPressure), taints, labels principaux.
- Capacité allouée : CPU, mémoire et pods demandés vs allocatable, avec barre orange au-delà de 90 %.
- Liste des pods du node (cliquable).
- Actions : cordon, uncordon, drain. Le drain affiche d'abord un récapitulatif (pods évincés, pods de DaemonSet ignorés, PDB qui bloqueraient) et demande confirmation en tapant le nom court du node.

**Règles communes aux actions**

- Confirmation obligatoire pour supprimer un pod, drain et scale à 0.
- Avertissement si le workload est géré par ArgoCD avec selfHeal : le scale sera annulé à la prochaine synchronisation.
- Bouton désactivé avec infobulle « Vous n'avez pas le droit `<verbe>` sur `<ressource>` » si l'access review est négative.
- Résultat affiché dans un toast et dans le bandeau d'événements, avec le message d'erreur de l'API server tel quel en cas d'échec.

## Déploiement

L'application s'installe avec `helm install cluster-atlas oci://<registry>/charts/cluster-atlas -n cluster-atlas --create-namespace -f values.yaml` et devient accessible par une URL protégée par OIDC.

**Ce que le chart crée**

- `Deployment` : une image unique (backend Go qui sert aussi le front compilé), 1 replica par défaut, 2 en option. Les sessions étant des cookies chiffrés, aucune affinité n'est nécessaire ; un client reconnecté reçoit un nouveau snapshot.
- `ServiceAccount` + `ClusterRole` + `ClusterRoleBinding` (détail ci-dessous).
- `Service` ClusterIP (port 8080) et, au choix, `Ingress` ou `HTTPRoute` (Gateway API), avec annotations libres et TLS (cert-manager ou secret existant). Timeouts WebSocket à documenter pour ingress-nginx, Traefik et GKE Gateway (au moins 3 600 s).
- `Secret` pour le client secret OIDC et la clé de chiffrement des cookies (générée si absente, conservée aux upgrades via `lookup`).
- `NetworkPolicy` (activée par défaut) : entrée seulement depuis le namespace de l'ingress controller, sortie seulement vers l'API server, le DNS du cluster et l'issuer OIDC.
- `PodDisruptionBudget` si `replicaCount > 1`, `ServiceMonitor` optionnel.

**ClusterRole du ServiceAccount**

| Ressources | Verbes | Usage |
| --- | --- | --- |
| `users`, `groups` | `impersonate` | Toutes les requêtes faites au nom de l'utilisateur |
| `nodes`, `pods`, `namespaces`, `events` | `get`, `list`, `watch` | Cache partagé de la vue 3D |
| `apps/*` (deployments, replicasets, statefulsets, daemonsets), `batch/jobs` | `get`, `list`, `watch` | Résolution des propriétaires, replicas |
| `metrics.k8s.io/pods`, `metrics.k8s.io/nodes` | `get`, `list` | Usage CPU et mémoire |
| `authorization.k8s.io/subjectaccessreviews` | `create` | Filtrage par droits de l'utilisateur |

Le ServiceAccount n'a aucun droit d'écriture sur les workloads ni sur `pods/exec` : toutes les écritures passent par impersonation. Aucune lecture de `secrets` ni de `configmaps`.

**Pod de l'application**

`runAsNonRoot`, `readOnlyRootFilesystem`, aucune capability, `seccompProfile: RuntimeDefault`, image distroless. Requests par défaut 100m / 128Mi, limite mémoire 512Mi. Probes sur `/healthz` et `/readyz` (prêt une fois les caches des informers synchronisés).

**Values principales**

```yaml
clusterName: gke-prod-europe-west1
auth:
  mode: oidc            # oidc | none (dev uniquement)
  oidc:
    issuerURL: https://accounts.google.com
    clientID: ""
    existingSecret: ""  # clés : client-secret, cookie-key
    usernameClaim: email
    groupsClaim: groups
    groupsPrefix: "oidc:"
  sessionTTL: 8h
features:
  exec:
    enabled: true
    deniedNamespaces: [kube-system]
    idleTimeout: 15m
  actions:
    enabled: true       # false = console en lecture seule
ui:
  poolLabel: ""         # vide = détection automatique
ingress:
  enabled: true
  className: nginx
  host: atlas.example.com
  tls: true
networkPolicy:
  enabled: true
```

**Configuration de l'API server pour l'OIDC**

L'impersonation n'exige pas que l'API server fasse confiance à l'IdP : c'est le backend qui valide le token. En revanche, les `RoleBinding` de l'organisation doivent viser les mêmes noms d'utilisateur et de groupes (avec le même préfixe) que ceux envoyés par le backend. Le README donne un exemple de binding `view` et `edit` par groupe.

## Performance

Cible : 60 images/s sur un portable récent et 30 sur un mobile milieu de gamme, pour un cluster de 100 nodes et 3 000 pods.

**Rendu**

- Chaque partie de robot (corps, tête, yeux, jambes, antenne) est un `InstancedMesh` : quelques draw calls pour tous les pods, pas un mesh par pod. Couleur et statut passent par des attributs d'instance ; les animations (balancement, chute, rotation) sont calculées dans le vertex shader à partir d'un attribut `state` et d'une phase par instance.
- Bâtiments : un `InstancedMesh` par style de bâtiment. Fumée et ventilateurs seulement en zoom rapproché.
- Niveaux de détail : sous un certain zoom, un pod devient un simple cube coloré ; au-delà de 48 pods sur un node, les pods sont regroupés en une pile par workload avec un compteur.
- Picking par ID de couleur ou par raycast sur les instances (`instanceId`), sans recréer de géométrie.
- Ombres limitées aux bâtiments ; rendu à la demande (`frameloop="demand"`) quand rien ne bouge et que l'onglet n'est pas visible.

**Données**

- Informers avec transformations (`SetTransform`) pour ne garder en mémoire que les champs utiles ; mémoire du backend sous 300 Mi à 3 000 pods.
- Deltas regroupés toutes les 250 ms, compressés (`permessage-deflate`).
- Côté front, état normalisé dans un store (Zustand) indexé par UID ; la scène lit le store par sélecteurs, sans re-render React par message.
- Access reviews mises en cache 60 s par utilisateur et par (verbe, ressource, namespace).

## Stack et structure du repo

Un monorepo, un binaire Go qui embarque le front compilé (`embed.FS`), une image, un chart.

| Couche | Choix |
| --- | --- |
| Backend | Go 1.23+, `client-go` (informers, impersonation, `remotecommand`), `chi` pour le routage, `coder/websocket`, `coreos/go-oidc` + `golang.org/x/oauth2`, `log/slog` en JSON |
| Front | React 18, TypeScript, Vite, React Three Fiber, drei, Zustand, xterm.js (+ fit), Monaco (lecture seule), TanStack Virtual pour les logs |
| Build | Dockerfile multi-étapes (node → go → distroless/static), image multi-arch amd64/arm64 |
| Packaging | Chart Helm dans `deploy/helm`, publié en OCI |
| CI | GitHub Actions : lint, tests, build, scan Trivy, push image et chart sur tag |

```text
cluster-atlas/
├── cmd/atlas/            # main : config, serveur HTTP
├── internal/
│   ├── auth/             # OIDC, sessions, middleware
│   ├── kube/             # clients, impersonation, informers, modèle réduit
│   ├── stream/           # hub WebSocket, snapshot + deltas
│   ├── access/           # SubjectAccessReview + cache
│   ├── logs/  exec/      # proxys WebSocket
│   ├── actions/          # delete, scale, restart, cordon, drain
│   └── audit/
├── web/                  # app Vite
│   └── src/
│       ├── scene/        # ville, bâtiments, robots instanciés, caméra, picking
│       ├── inspector/    # onglets pod et node
│       ├── store/        # état normalisé, connexion au stream
│       └── api/
├── deploy/helm/cluster-atlas/
├── hack/                 # kind + Dex pour le dev local, scénarios de charge
└── docs/
```

**Environnement de dev** : `make dev` lance un cluster kind, Dex (OIDC) avec deux utilisateurs (un `view`, un `edit`), le backend en local avec `auth.mode: oidc`, et Vite en proxy. Un script `hack/scenarios` crée des workloads de démonstration (CrashLoopBackOff, ImagePullBackOff, pods Pending par manque de CPU, StatefulSet, DaemonSet, Job).

**Mode démo** : `atlas --demo` sert la vue avec un cluster simulé (comme le prototype), sans API server, pour les captures et les tests visuels.

## Tests et critères d'acceptation

Le MVP est accepté quand les scénarios ci-dessous passent sur un cluster kind avec Dex, puis sur un cluster GKE réel.

**Tests automatisés**

- Go : tests unitaires (calcul du `displayStatus`, résolution des propriétaires, filtrage par droits, construction des deltas) ; tests d'intégration avec `envtest` pour l'impersonation et les actions.
- Front : Vitest sur le store et les correspondances statut → posture ; Playwright en mode démo pour les parcours de l'inspecteur.
- E2E : kind + Dex + chart installé, Playwright se connecte avec les deux utilisateurs.
- Charge : `hack/scenarios` crée 3 000 pods (pause) sur des nodes kwok ; mesure des images/s et de la mémoire du backend.

**Critères d'acceptation**

- [ ] `helm install` sur un cluster vierge donne une URL fonctionnelle en moins de 2 min, sans aucune étape `kubectl` côté utilisateur.
- [ ] Un pod créé, supprimé ou qui change de statut apparaît dans la vue en moins de 2 s.
- [ ] L'utilisateur `view` voit les pods, lit les logs, mais les boutons d'action et le terminal sont désactivés avec une explication ; un appel forcé à l'API renvoie 403.
- [ ] L'utilisateur sans droits sur `kube-system` ne reçoit aucun objet de ce namespace dans le flux WebSocket.
- [ ] Le terminal ouvre un shell, gère le redimensionnement, les touches de contrôle (Ctrl+C, Tab, flèches) et se ferme proprement à `exit`.
- [ ] Les logs suivent un pod en direct et l'option « instance précédente » montre les logs d'avant le crash.
- [ ] Le YAML affiché est celui du Deployment propriétaire, sans `managedFields`, avec le statut ArgoCD quand il existe.
- [ ] Un drain respecte les PDB et ignore les pods de DaemonSet ; les robots évincés réapparaissent sur d'autres nodes.
- [ ] Chaque action et chaque session exec produisent une ligne d'audit.
- [ ] 60 images/s à 3 000 pods sur un MacBook M1 ; mémoire du backend sous 300 Mi.
- [ ] Le pod de l'application tourne en non-root, système de fichiers en lecture seule, et passe le scan Trivy sans vulnérabilité critique.

## Plan de livraison

Sept jalons, chacun livrable et testable seul ; Claude Code termine un jalon (code, tests, README à jour) avant de passer au suivant.

1. **Squelette et mode démo** : monorepo, binaire Go qui sert le front, scène R3F avec ville, bâtiments et robots instanciés alimentés par le simulateur (`--demo`). Fin : le prototype tourne depuis le binaire.
2. **Lecture du cluster** : informers, modèle réduit, `displayStatus`, flux `/api/stream` snapshot + deltas, front branché sur le flux. Fin : la vue reflète un cluster kind en direct (sans auth, `auth.mode: none`).
3. **Chart Helm et déploiement in-cluster** : image, chart, ServiceAccount et ClusterRole, Ingress/HTTPRoute, NetworkPolicy, probes. Fin : `helm install` sur kind donne une URL.
4. **Authentification et droits** : OIDC, sessions, impersonation, filtrage du flux par access review, `make dev` avec Dex. Fin : les deux utilisateurs de test voient des choses différentes.
5. **Inspecteur en lecture** : aperçu pod et node, YAML, événements, logs en streaming. Fin : critères logs et YAML validés.
6. **Terminal et actions** : exec WebSocket, xterm.js, delete, scale, restart, cordon, uncordon, drain, confirmations, audit. Fin : critères exec, drain et audit validés.
7. **Échelle et finition** : LOD, regroupement des pods, rendu à la demande, recherche, vue Liste accessible, thème sombre, test de charge kwok, CI et publication. Fin : tous les critères d'acceptation cochés.

**Consignes pour Claude Code**

- Ne jamais ajouter de droit d'écriture au ServiceAccount pour contourner un problème : toute écriture passe par impersonation.
- Garder le mode démo fonctionnel à chaque jalon ; il sert de banc d'essai visuel.
- Toute nouvelle dépendance front doit être compatible avec une CSP sans script externe.
- En cas d'ambiguïté sur le comportement Kubernetes, s'aligner sur ce qu'afficherait `kubectl` (statut des pods, ordre des événements, comportement du drain).
