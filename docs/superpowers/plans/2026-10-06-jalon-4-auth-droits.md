# Jalon 4 — Authentification et droits : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** l'utilisateur se connecte par OIDC (code + PKCE) ; chaque requête vers l'API server porte son identité (impersonation) ; le flux `/api/stream` ne lui envoie que les objets qu'il a le droit de lister. `make dev` monte kind + Dex avec deux utilisateurs qui voient des choses différentes.

**Architecture :** `internal/auth` (OIDC, session en cookie chiffré AES-256-GCM, middleware, CSRF) ; `internal/access` (SubjectAccessReview faites par le ServiceAccount au nom de l'utilisateur, cache 60 s par utilisateur et (verbe, ressource, namespace) ; client Kubernetes impersonné par utilisateur ; `SelfSubjectAccessReview` en lot pour `/api/access-review`). Le filtrage du flux se fait dans le handler WebSocket de chaque client, jamais sous le verrou du hub : un client lent à évaluer ses droits ne ralentit pas les autres.

**Tech Stack :** coreos/go-oidc v3, golang.org/x/oauth2 (PKCE S256), go-jose (IdP de test), Dex (dev), kind + ingress-nginx (vérification in-cluster).

---

## Décisions

1. **Identité impersonnée** : `Impersonate-User` = claim `usernameClaim` (email par défaut), `Impersonate-Group` = `groupsPrefix` + chaque groupe. Refus de connexion si l'utilisateur commence par `system:` ; groupes `system:*` ignorés (même sans préfixe) : un IdP ne doit jamais pouvoir donner `system:masters`. Le préfixe, appliqué à tous les groupes, est la « restriction par préfixe » de la spec.
2. **Session** : cookie `atlas_session` chiffré (AES-256-GCM, clé `ATLAS_COOKIE_KEY` = base64 de 32 octets), contenu `{user, groups, exp}`, `HttpOnly`, `SameSite=Lax`, `Secure` si l'URL publique est en https (en dev sur `http://localhost`, pas `Secure`). Pas de refresh token : reconnexion à expiration (8 h par défaut). Cookie trop gros (> 3 800 octets, trop de groupes) : erreur explicite.
3. **Login** : état, nonce et vérificateur PKCE dans un cookie temporaire chiffré `atlas_oauth` (10 min). Retour vers la page d'origine seulement si c'est un chemin relatif.
4. **Routes protégées** : `/api/*` → 401 JSON sans session ; pages HTML → redirection `/auth/login`. Assets (`/assets/*`, favicon) et probes publics. `/metrics` reste sur son port.
5. **CSRF** : toute requête mutante (`POST`, `PUT`, `PATCH`, `DELETE`) sous `/api` exige l'en-tête `X-Atlas-Request: 1` (impossible à poser en cross-origin sans pré-vol CORS, que l'on n'autorise pas).
6. **Filtrage du flux** : pods, workloads → `list` sur la ressource dans le namespace ; namespaces → `list pods` dans ce namespace ; nodes → `list nodes` (cluster). Métriques filtrées de même. Sans droit sur les nodes, le front dessine des **bâtiments anonymes** à partir des `nodeName` des pods visibles (l'utilisateur les voit déjà dans ses pods), sans capacité ni détails.
7. **Droits qui changent** : le handler réévalue toutes les 60 s les décisions déjà prises pour ce client ; si l'une change, il ferme la connexion avec le code `4000` et le front se reconnecte sans `rev` (snapshot complet, refiltré). Session expirée en cours de flux : code `4401`, le front renvoie vers `/auth/login`.
8. **`auth.mode: none`** : pas d'impersonation ni de filtrage (droits du kubeconfig / ServiceAccount) ; inchangé depuis le jalon 2.
9. **Scopes** : `auth.oidc.scopes` (défaut `openid email profile`, Google refuse `groups`) ; Dex : `openid email profile groups`.

## Tâches

### Task 1 : session et cookies (`internal/auth/session.go`)
- [ ] Tests : aller-retour chiffré ; cookie modifié ou clé différente → rejet ; expiration ; taille max ; clé de mauvaise longueur refusée au démarrage.

### Task 2 : OIDC (`internal/auth/oidc.go`, `middleware.go`)
- [ ] IdP de test en Go (`httptest` : discovery, JWKS, authorize simulé, token qui vérifie le `code_verifier` S256, ID token signé RS256).
- [ ] Tests du parcours : `/auth/login` redirige avec `state`, `nonce`, `code_challenge` ; callback avec bon code → cookie de session, redirection vers la page d'origine ; `state` faux → 400 ; nonce faux → 401 ; `/auth/logout` efface le cookie ; utilisateur `system:…` refusé ; groupes préfixés, `system:*` ignorés ; middleware 401/redirect ; CSRF.

### Task 3 : droits (`internal/access`)
- [ ] `Reviewer.Allowed(ctx, user, verb, group, resource, namespace)` : `SubjectAccessReview` via le ServiceAccount ; cache 60 s ; erreurs → refus (jamais d'autorisation par défaut).
- [ ] `Clients.For(user)` : clientset impersonné, réutilisé par utilisateur.
- [ ] `/api/access-review` : lot de `SelfSubjectAccessReview` au nom de l'utilisateur.
- [ ] Tests avec le fake clientset (réacteurs SAR).

### Task 4 : filtrage du flux (`internal/stream/ws.go`)
- [ ] `Handler(hub, log, filters)` où `filters(r) (Filter, error)` ; `Filter.Allow(ctx, kind, obj)`, `Filter.Metrics(ctx, m)`, `Filter.Changed(ctx)` ; filtre « tout autorisé » pour none/démo.
- [ ] Tests : namespace refusé absent du snapshot, des deltas et des métriques ; réévaluation → fermeture 4000 ; session expirée → 4401.

### Task 5 : commande et chart
- [ ] Flags/env `ATLAS_OIDC_*`, `ATLAS_COOKIE_KEY`, `ATLAS_PUBLIC_URL`, `ATLAS_SESSION_TTL`, `ATLAS_OIDC_SCOPES` ; mode oidc branché ; chart : `auth.oidc.scopes`.

### Task 6 : front
- [ ] 401 → `/auth/login` ; codes 4401/4000 ; utilisateur et déconnexion dans la barre du haut ; en-tête CSRF sur les requêtes mutantes ; bâtiments anonymes. Tests Vitest.

### Task 7 : `make dev` avec Dex
- [ ] `hack/dex/config.yaml` : client `cluster-atlas`, utilisateurs `alice@example.com` (groupe `sre`) et `bob@example.com` (groupe `dev`) ; `hack/dev-rbac.yaml` : `oidc:sre` → `view` sur tout le cluster, `oidc:dev` → `edit` dans `production` et `staging` seulement.
- [ ] `make dev` : Dex en conteneur sur `:5556`, backend local en `auth.mode=oidc`, Vite.
- [ ] E2E Playwright (`e2e/auth.spec.ts`, projet séparé) : alice voit `kube-system` et les nodes ; bob ne reçoit aucun pod de `kube-system` et voit des bâtiments anonymes ; un appel sans cookie → 401.

### Task 8 : vérification in-cluster
- [ ] kind avec `extraPortMappings` 80 → ingress-nginx, Dex dans le cluster, réécriture CoreDNS de `dex.localtest.me` vers l'ingress pour que pods et navigateur voient le même issuer ; chart installé avec `auth.mode=oidc` + Ingress `atlas.localtest.me` ; Playwright se connecte par l'URL.
