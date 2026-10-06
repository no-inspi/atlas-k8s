# Jalon 6 — Terminal et actions : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** terminal interactif dans un container (équivalent `kubectl exec -it`), six actions d'exploitation (supprimer un pod, scale, rollout restart, cordon, uncordon, drain), confirmations, boutons grisés selon les droits, journal d'audit. Critères : terminal (shell, redimensionnement, Ctrl+C/Tab/flèches, `exit`), drain (PDB respectés, DaemonSets ignorés, robots qui réapparaissent ailleurs), une ligne d'audit par action et par session exec, utilisateur `view` désactivé et 403 sur appel forcé.

**Architecture :** `internal/exec` (WebSocket navigateur ↔ `remotecommand` v5.channel.k8s.io, repli SPDY, au nom de l'utilisateur), `internal/actions` (interface + implémentation Kubernetes impersonnée), `internal/audit` (une ligne JSON sur stdout). Le simulateur implémente les mêmes interfaces (shell simulé, actions sur le cluster simulé).

## Décisions

1. **Protocole navigateur** : messages binaires = stdin / stdout ; messages texte JSON : `{"type":"resize","cols","rows"}` du navigateur, `{"type":"info"|"error"|"exit",…}` du serveur.
2. **Shell** : `/bin/bash`, puis `/bin/sh` si le premier n'existe pas ; sinon message « aucun shell (image distroless ?) ».
3. **Garde-fous exec** : `features.exec.enabled`, namespaces interdits (`kube-system` par défaut) refusés même si le RBAC l'autorise, inactivité 15 min ; pod non Running → message explicite avant toute connexion.
4. **Drain** : `POST /api/nodes/{node}/drain?dryRun=true` renvoie le plan (pods évincés, DaemonSets et pods miroirs ignorés, PDB bloquants) ; l'exécution cordonne puis évince par l'API Eviction, en réessayant les refus de PDB (429) jusqu'à 60 s par pod, et renvoie le résultat pod par pod.
5. **Rollout restart** : annotation `kubectl.kubernetes.io/restartedAt` sur le template (Deployment, StatefulSet, DaemonSet). **Scale** : sous-ressource `scale` (Deployment, StatefulSet).
6. **ArgoCD** : sans lecture des `Application`, l'avertissement selfHeal s'affiche dès qu'un workload est géré par ArgoCD (« si selfHeal est activé, … »).
7. **`features.actions.enabled: false`** : routes d'écriture absentes (404) et boutons masqués ; `/api/me` expose les fonctionnalités.
8. **Dev** : troisième utilisateur Dex `carol@example.com` (groupe `ops` → `cluster-admin`) pour cordon/drain ; PDB `api-gateway` (minAvailable 3) dans les scénarios.
