# Jalon 7 — Échelle et finition : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** tenir 3 000 pods sur 100 nodes (60 images/s sur portable récent, mémoire du backend < 300 Mi), ajouter la recherche, la vue Liste accessible et le choix du thème, publier par CI ; tous les critères d'acceptation de la spec cochés.

## Décisions

1. **Mesurer avant d'optimiser.** Un mode démo à grande échelle (`--demo-scale=100x30`) et un compteur d'images (`?perf=1`) servent de banc ; le test kwok (100 nodes, 3 000 pods réels) valide la mémoire du backend et le chemin informers.
2. **Niveaux de détail** : en zoom éloigné, un robot devient un cube coloré (une pièce au lieu de treize) ; fumée et ventilateurs seulement en zoom rapproché. **Regroupement** : au-delà de 48 pods sur un node, une pile par workload avec un compteur.
3. **Rendu à la demande** : `frameloop="demand"` ; une frame est demandée à chaque changement d'état, interaction caméra ou animation en cours ; animations idle coupées quand l'onglet est caché ou avec `prefers-reduced-motion`.
4. **Animation en vertex shader** seulement si la mesure montre que la mise à jour CPU des matrices coûte trop à 3 000 pods.
5. **Recherche** (`/`) : pods, nodes, workloads ; centre la caméra et sélectionne. **Vue Liste** : arbre namespace → workload → pod, et nodes, au clavier (rôle `tree`), même inspecteur. **Thème** : système / clair / sombre, mémorisé.
6. **CI** (GitHub Actions) : lint (go vet, gofmt, tsc), tests Go et Vitest, `helm lint` + tests du chart, e2e démo, build multi-arch, Trivy ; sur tag `v*` : image et chart OCI poussés sur ghcr.io. Vérifiée localement par actionlint.
7. **Hors MVP** (README) : supprimer un node via le fournisseur (NodeClaim Karpenter, node pool GKE).
