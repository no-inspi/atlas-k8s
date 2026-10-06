# Jalon 5 — Inspecteur en lecture : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** l'inspecteur de pod gagne les onglets Logs (streaming), YAML (workload racine, Monaco en lecture seule) et Événements (en direct) ; la chaîne de propriétaires devient cliquable. Critères : les logs suivent un pod en direct et « instance précédente » montre les logs d'avant le crash ; le YAML est celui du Deployment propriétaire, sans `managedFields`, avec ArgoCD quand il existe.

**Architecture :** une interface `inspect.Backend` (propriétaires, YAML, événements, logs) avec deux implémentations : `kube.Inspector` (clients impersonnés de l'utilisateur pour propriétaires, YAML et logs ; cache d'événements partagé + SubjectAccessReview pour les événements) et le simulateur (mode démo). Les logs passent par un WebSocket dédié (`internal/logs`) avec backpressure : un tampon borné, les lignes en trop sont comptées et signalées.

**Tech Stack :** client-go (typed + dynamic, impersonation), sigs.k8s.io/yaml, Monaco (`monaco-editor`, chargé à la demande, YAML seulement), TanStack Virtual.

---

## Décisions

1. **CSP et Monaco** : Monaco crée des `<style>` et des attributs `style` dans du HTML ; sans prise en charge de nonce (vérifié dans les sources 0.57), il faut `style-src 'self' 'unsafe-inline'`. Les scripts restent `'self'` uniquement (la spec définit la CSP stricte par « aucun script externe »). Alternative si l'on veut une CSP de styles stricte : CodeMirror 6, qui accepte un nonce.
2. **Événements** : informer partagé sur `events` (déjà dans le ClusterRole), indexé par objet concerné ; l'endpoint vérifie `list events` dans le namespace au nom de l'utilisateur. Le front interroge toutes les 3 s tant que l'onglet est ouvert.
3. **YAML** : kinds autorisés `core/v1/Pod`, `apps/v1/{Deployment,ReplicaSet,StatefulSet,DaemonSet}`, `batch/v1/Job` ; lecture par le client dynamique impersonné ; `metadata.managedFields` retiré, le reste tel que l'API le renvoie ; `status` replié à l'affichage.
4. **Logs** : `pods/log` impersonné, `timestamps=true`, `follow` sauf pour l'instance précédente, `tailLines=500` par défaut (max 5 000). Messages : `{"type":"lines","lines":[{"ts","text"}]}` toutes les 100 ms, `{"type":"dropped","count"}`, `{"type":"end"}`, `{"type":"error","message"}`. Tampon serveur de 2 000 lignes. Côté front : 5 000 lignes, rendu virtualisé.
5. **Erreurs de l'API server** renvoyées telles quelles (403 avec le message RBAC, 404).

## Tâches

1. `model.Event`, interface `inspect.Backend`, `kube.Inspector` (propriétaires, YAML, logs, événements via informer) — tests avec fake clientsets (typed, dynamic) et réacteurs Forbidden.
2. Démo : événements, logs (générateurs du prototype, instance précédente au crash), YAML généré — tests.
3. Routes REST et WebSocket de logs (`internal/logs`) — tests (lignes, backpressure, fin, erreur, 403).
4. Front : onglet actif conservé, chaîne cliquable, onglets Logs, YAML (Monaco paresseux), Événements — tests Vitest (niveaux, tampon, filtre).
5. E2E démo (logs, YAML, événements) et kind authentifié (instance précédente de payment-worker, YAML du Deployment avec badge ArgoCD, bob refusé sur kube-system).
