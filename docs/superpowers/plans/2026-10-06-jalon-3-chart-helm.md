# Jalon 3 — Image, chart Helm et déploiement in-cluster : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** `helm install cluster-atlas deploy/helm/cluster-atlas -n cluster-atlas --create-namespace` sur kind donne une application prête, qui lit le cluster avec un ServiceAccount aux droits strictement ceux de la spec, derrière une NetworkPolicy.

**Architecture :** une image distroless multi-arch (front compilé → binaire Go statique → `distroless/static:nonroot`) ; un chart qui rend toute la configuration en variables d'environnement `ATLAS_*` (le binaire lira l'OIDC au jalon 4, les options exec/actions aux jalons 5–6) ; `/metrics` Prometheus pour le ServiceMonitor.

**Tech Stack :** Docker buildx, Helm 3, client_golang (Prometheus), kind + `kind load`, Trivy.

---

## Décisions

1. **Garde-fou `auth.mode: none`** : le chart échoue (`fail`) si `auth.mode=none` et que `ingress.enabled` ou `httpRoute.enabled` est vrai, comme le veut la spec. Tant que l'OIDC n'existe pas (jalon 4), la vérification sur kind se fait donc sans exposition, par `kubectl port-forward`. L'ingress et le HTTPRoute sont vérifiés par rendu (`helm template`) ; le parcours complet par URL, avec ingress-nginx et Dex, sera fait au jalon 4.
2. **Valeurs par défaut sûres** : `auth.mode: oidc`, `ingress.enabled: false` (la spec montre `true` dans son exemple de values ; un `helm install` sans configuration OIDC ne doit pas exposer quoi que ce soit). Le `values.yaml` documente l'exemple de la spec.
3. **Image** : `ghcr.io/no-inspi/cluster-atlas`, tag = `appVersion` du chart. La publication (CI, OCI) est au jalon 7.
4. **NetworkPolicy vers l'API server** : les IP réelles sont lues par `lookup` sur `Endpoints default/kubernetes` (après DNAT, c'est elles que la politique voit) ; repli configurable si `lookup` est vide (`helm template`, GitOps).
5. **`/metrics`** : client_golang ; compteurs de clients du flux, messages et lots envoyés, rev courant, plus les métriques Go/process standard.
6. **Tests du chart** : un test Go (`deploy/helm/chart_test.go`) appelle `helm template` et vérifie les documents rendus ; il est ignoré si `helm` est absent.

## Tâches

### Task 1 : `/metrics`
- [ ] Tests : `/metrics` répond en texte Prometheus et contient `atlas_stream_clients` et `atlas_stream_rev`.
- [ ] `internal/server/metrics.go` (registre dédié, collecteurs Go et process) ; le hub expose `Stats()` (clients, rev) lu par des `GaugeFunc` ; le handler WebSocket incrémente `atlas_stream_messages_total`.

### Task 2 : image
- [ ] `Dockerfile` multi-étapes : `node:22-alpine` (sur `$BUILDPLATFORM`) → `golang:1.26-alpine` (cross-compile `GOOS/GOARCH`, `CGO_ENABLED=0`, `-trimpath -ldflags "-s -w -X main.version=…"`) → `gcr.io/distroless/static:nonroot`, `USER 65532`, `EXPOSE 8080`.
- [ ] `.dockerignore` ; `make image`, `make kind-load`.
- [ ] Vérif : l'image démarre en `--demo`, taille notée, `trivy image --severity CRITICAL` sans résultat.

### Task 3 : chart
`deploy/helm/cluster-atlas/` : `Chart.yaml`, `values.yaml`, `values.schema.json` (types et énumérations), `templates/` :
- `_helpers.tpl` (noms, labels, validation), `serviceaccount.yaml`, `clusterrole.yaml` + `clusterrolebinding.yaml` (table de la spec, impersonation restreignable par `rbac.impersonate.users` / `groups`), `deployment.yaml` (sécurité de la spec, probes, env `ATLAS_*`, checksum du secret, anti-affinité si 2 replicas), `service.yaml`, `ingress.yaml`, `httproute.yaml`, `secret.yaml` (clé de cookie générée et conservée par `lookup`, client secret si pas d'`existingSecret`), `networkpolicy.yaml`, `pdb.yaml` (si `replicaCount > 1`), `servicemonitor.yaml`, `NOTES.txt`.
- [ ] Tests `chart_test.go` : rendu par défaut ; garde-fou none + ingress ; ClusterRole exact (aucun verbe d'écriture hors `impersonate`/`create subjectaccessreviews`, ni secrets ni configmaps) ; securityContext ; PDB seulement à 2 replicas ; HTTPRoute ; NetworkPolicy avec repli d'API server ; clé de cookie stable.
- [ ] `helm lint` propre.

### Task 4 : déploiement sur kind
- [ ] `make helm-kind` : image chargée dans kind, `helm upgrade --install` avec `auth.mode=none`, attente du rollout ; mesure du temps jusqu'au Ready.
- [ ] Vérifs : pod non-root et FS en lecture seule ; `kubectl auth can-i` du ServiceAccount (liste des pods oui, création/suppression non, secrets non) ; port-forward → `/api/me`, snapshot WebSocket, `/metrics` ; la NetworkPolicy n'empêche pas la synchronisation (pod Ready) ; mémoire du pod.
- [ ] README : installation, values principales, timeouts WebSocket des ingress (ingress-nginx, Traefik, GKE Gateway).
