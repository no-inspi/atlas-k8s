# Jalon 2 — Lecture du cluster : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** `atlas --auth-mode=none --kubeconfig …` (ou en in-cluster) reflète en direct un vrai cluster : informers partagés, modèle réduit, `displayStatus` identique à `kubectl get pods`, métriques metrics-server, flux `/api/stream` inchangé côté front.

**Architecture :** un nouveau paquet `internal/kube` remplace le simulateur comme source du hub. Les informers (un watch par type, `SetTransform` pour ne garder que l'utile) déclenchent des clés « sales » ; une boucle de réconciliation recalcule les objets concernés depuis les listers et publie `upsert`/`delete` dans le hub. Les conversions (Node, Pod, Workload, Namespace) sont des fonctions pures testées sur des objets `corev1`/`appsv1`.

**Tech Stack :** client-go / api / apimachinery / metrics v0.37 (le go.mod passe à la version exigée par client-go, la spec dit « Go 1.23+ »), fake clientset pour les tests, kind pour la vérification de bout en bout.

---

## Décisions

1. **Authentification** : l'OIDC arrive au jalon 4. D'ici là, le mode cluster exige `--auth-mode=none` explicite ; sans ce flag, le binaire refuse de démarrer (sécurité par défaut, comme `auth.mode: none` réservé au dev dans la spec).
2. **Connexion à l'API server** : in-cluster si `KUBERNETES_SERVICE_HOST` est défini, sinon `--kubeconfig` / `$KUBECONFIG` / `~/.kube/config` (dev local).
3. **Statut ArgoCD** : la spec ne donne pas au ServiceAccount la lecture des `Application`. On extrait l'application des annotations/labels (annotation `argocd.argoproj.io/tracking-id`, sinon label `argocd.argoproj.io/instance` ; pas `app.kubernetes.io/instance`, posé par trop de charts Helm sans ArgoCD) et on laisse `syncStatus` vide ; le front affiche « Géré par ArgoCD (application X) ». Le statut de sync réel demandera d'ajouter `applications` en lecture au ClusterRole : à trancher au jalon 5.
4. **Namespaces dans le flux** : nouveau `kind: "namespace"` (`{name, color}`), pour l'annotation `atlas.io/color` de la spec.
5. **Propriétaire racine** : Pod → ReplicaSet → Deployment ; Pod → StatefulSet / DaemonSet / Job ; ReplicaSet sans Deployment reste racine ; pod sans owner : `owner` vide. Les CronJobs ne sont pas dans le ClusterRole : la racine d'un pod de CronJob est son Job.
6. **Workloads publiés** : Deployments, StatefulSets, DaemonSets, Jobs et ReplicaSets orphelins.
7. **Métriques** : polling de `metrics.k8s.io` toutes les 15 s ; si l'API est absente, aucun message `metrics` (le front affiche « indisponible »).

## Fichiers

```text
internal/model/model.go           + Namespace
internal/kube/
  status.go     displayStatus (portage de printPod de kubectl), readiness, restarts
  convert.go    Node, Pod, Workload, Namespace → modèle ; détection pool/spot/GPU/zone ; ArgoCD
  owners.go     résolution du propriétaire racine via les listers
  source.go     informers + transforms + file de clés sales + publication dans le hub
  metrics.go    polling metrics-server
  client.go     construction de la rest.Config (in-cluster / kubeconfig)
cmd/atlas/main.go                  mode cluster (--auth-mode=none, --kubeconfig)
hack/kind.yaml                     cluster kind : 1 control-plane + 4 workers étiquetés en 3 pools
hack/scenarios/*.yaml              CrashLoopBackOff, ImagePullBackOff, Pending (CPU), StatefulSet, DaemonSet, Job, non ready
hack/metrics-server.sh             installe metrics-server sur kind (--kubelet-insecure-tls)
internal/kube/live_test.go         (build tag integration) latence < 2 s sur un vrai cluster
web/src/…                          namespace dans les types/store, couleur surchargée, libellé ArgoCD sans statut
Makefile                           kind-up, kind-down, scenarios, run-kind, test-integration
```

### Task 1 : dépendances et modèle
- [ ] `go get k8s.io/client-go@v0.37.1 k8s.io/api@v0.37.1 k8s.io/apimachinery@v0.37.1 k8s.io/metrics@v0.37.1`.
- [ ] `model.Namespace{Name, Color}` + `stream.KindNamespace` ; le snapshot inclut `namespaces`. Tests hub/model mis à jour d'abord.

### Task 2 : `displayStatus` (TDD, table de cas)
Portage de `printPod` (k8s.io/kubernetes/pkg/printers/internalversion) : raison de phase, `Init:N/M`, `Init:<reason>`, `Init:ExitCode:N`, `Init:Signal:N`, sidecars (init `restartPolicy: Always` démarrés ignorés), containers parcourus en sens inverse (waiting/terminated), `Completed` + container prêt → `Running` ou `NotReady`, `Terminating`, `Unknown` (NodeLost). Restarts = somme des containers (init compris, comme kubectl). Cas testés : Pending sans node, ContainerCreating, PodInitializing, Init:0/1, Init:CrashLoopBackOff, Init:Error, CrashLoopBackOff, Error, OOMKilled, ImagePullBackOff, ErrImagePull, Completed, Running, Running non ready, Terminating, Evicted (status.reason), NodeLost → Unknown, ExitCode:137 sans raison.

### Task 3 : conversions (TDD)
- Node : pool (label `cloud.google.com/gke-nodepool`, `karpenter.sh/nodepool`, `eks.amazonaws.com/nodegroup`, `--pool-label` s'il est fourni, sinon `node.kubernetes.io/instance-type`, sinon « default ») ; instanceType ; zone (`topology.kubernetes.io/zone`) ; spot (`cloud.google.com/gke-spot`, `cloud.google.com/gke-preemptible`, `karpenter.sh/capacity-type=spot`, `eks.amazonaws.com/capacityType=SPOT`) ; GPU = allocatable `nvidia.com/gpu` ; conditions Ready/MemoryPressure/DiskPressure/PIDPressure ; taints ; `requested` = somme des requests des pods non terminés du node (règle kube-scheduler : max(somme containers, max init) + overhead).
- Pod : champs du modèle, requests/limits effectifs, QoS, `statusMessage` = message de `PodScheduled=False` ou `status.message`.
- Workload : replicas désirés/prêts par type ; ArgoCD.
- Namespace : couleur `atlas.io/color` si c'est un `#RRGGBB` valide.

### Task 4 : propriétaires et source
- `owners.go` : `RootOwner(pod, rsLister)` ; tests avec listers alimentés par des indexers.
- `source.go` : `NewSource(client, hub, opts)` ; transforms (supprime `managedFields`, annotations `kubectl.kubernetes.io/last-applied-configuration`, et pour les pods `spec.volumes`, env) ; handlers → file de clés (`node/x`, `pod/ns/name`, `workload/kind/ns/name`, `namespace/x`) ; un changement de pod marque son node (ancien et nouveau) et son workload racine ; un ReplicaSet marque son Deployment et ses pods (index par owner UID) ; boucle de réconciliation toutes les 100 ms ; suppression publiée avec le dernier modèle connu. `Run(ctx)` attend `WaitForCacheSync`, publie l'état complet puis `hub.MarkReady()`.
- [ ] Tests avec `fake.NewSimpleClientset` : état initial publié ; création d'un pod → upsert pod + node.requested mis à jour ; Deployment/RS/Pod → owner Deployment ; suppression → delete ; cordon → node.unschedulable.

### Task 5 : métriques
- `metrics.go` : `PodMetricses("").List` et `NodeMetricses().List` toutes les 15 s → `hub.SetMetrics` ; erreur NotFound/ServiceUnavailable → journalisée une fois, rien publié. Test avec le fake clientset metrics.

### Task 6 : commande
- `--auth-mode` (défaut `oidc` → refus « OIDC disponible au jalon 4 »), `--kubeconfig`, `--pool-label`, `--cluster-name` ; `/api/me` renvoie `demo:false`, utilisateur `anonymous`. `--demo` inchangé.

### Task 7 : front
- Types, store (`namespaces` Map), `nsColors` accepte des surcharges ; libellé ArgoCD sans statut. Tests Vitest d'abord.

### Task 8 : kind, scénarios, vérification
- `make kind-up` (cluster `atlas`, workers étiquetés `cloud.google.com/gke-nodepool` = default-pool ×2, spot-pool ×1 avec `cloud.google.com/gke-spot=true`, gpu-pool ×1 tainted), metrics-server, `make scenarios`.
- Test d'intégration (tag `integration`) : crée un pod, mesure le délai jusqu'au delta dans le hub (< 2 s), le supprime, mesure le delete.
- Vérification visuelle : `bin/atlas --auth-mode=none` sur kind, capture Playwright ; les statuts affichés doivent correspondre à `kubectl get pods -A`.
- README à jour, commit.

## Auto-revue contre la spec (jalon 2)

- Informers, modèle réduit, `displayStatus` : Tasks 2–4. `SetTransform` : Task 4. Snapshot + deltas + reconnexion : inchangés (jalon 1). Métriques 15 s : Task 5. Front branché : Task 7. « La vue reflète un cluster kind en direct » : Task 8. Critère « < 2 s » : test d'intégration.
- Hors jalon : filtrage par droits (jalon 4), chart (jalon 3).
