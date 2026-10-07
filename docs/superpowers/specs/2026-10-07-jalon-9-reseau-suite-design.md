# Jalon 9 — Suite du réseau et du stockage : design

Suite directe du jalon 8 ([design](2026-10-07-jalon-8-reseau-stockage-design.md)). Il reprend ce que celui-ci laissait hors jalon : Gateway API, Traefik complet (IngressRouteTCP/UDP, TraefikService), prise en compte à chaud des CRD, PersistentVolumes sans PVC. Toujours en lecture seule : aucune action nouvelle.

## But

Que la ville montre l'exposition réelle d'un cluster moderne, quel que soit le contrôleur d'entrée : un cluster sous Gateway API ou sous Traefik avancé (canary pondéré, miroir, TCP) doit se lire aussi bien qu'un cluster sous Ingress. Et qu'aucun type n'exige de redémarrer Atlas parce que sa CRD a été installée après lui.

Ordre des lots, chacun livrable et testable seul :

1. Registre des types dynamiques et CRD à chaud (Traefik y migre).
2. Gateway API.
3. Traefik complet.
4. PersistentVolumes sans PVC.

## Lot 1 — Registre des types dynamiques et CRD à chaud

### Registre (`internal/kube/dynkinds.go`)

Chaque type optionnel, apporté par une CRD, est déclaré une fois :

- son GVR (groupe, version, ressource) et le nom de la CRD qui l'apporte (`<ressource>.<groupe>`) ;
- son kind dans le flux et sa fonction de marquage (`onX`) ;
- ses index (par Service visé, par TraefikService visé, par Gateway parente).

Types déclarés :

| Groupe | Version | Ressources |
| --- | --- | --- |
| `traefik.io`, `traefik.containo.us` | `v1alpha1` | `ingressroutes`, `ingressroutetcps`, `ingressrouteudps`, `traefikservices` |
| `gateway.networking.k8s.io` | `v1` | `gatewayclasses`, `gateways`, `httproutes`, `grpcroutes` |

Tous sont lus en `unstructured` par le client dynamique, comme les IngressRoute du jalon 8 : aucune dépendance Go nouvelle (pas de `sigs.k8s.io/gateway-api`). `traefik.go` est absorbé par le registre.

Chaque type a son propre informer dynamique (`dynamicinformer.NewFilteredDynamicInformer`) et son propre canal d'arrêt, pour pouvoir être arrêté seul. Le transform existant (sans managedFields) s'applique.

### CRD à chaud

- Informer sur `customresourcedefinitions` (`apiextensions.k8s.io/v1`), filtré sur les noms du registre.
- **CRD ajoutée** (ou modifiée et servant désormais la version attendue, `served: true`) : list d'essai (sonde du jalon 8), démarrage de l'informer, attente de sa synchronisation, puis marquage de tous ses objets.
- **CRD supprimée** (ou version attendue plus servie) : arrêt de l'informer, retrait du flux de tous les objets du type, puis recalcul de ce qui en dépendait (les routes vers un TraefikService disparu passent en `missing` ; les routes rattachées à un Gateway disparu gardent leur porte, déduite, en état inconnu).
- Sans droit sur les CRD (sonde refusée) : repli sur le comportement du jalon 8, découverte d'API au démarrage, avec un warning dans les logs.
- `/readyz` n'attend pas les types dynamiques démarrés après coup.

## Lot 2 — Gateway API

### Nouveau kind `gateway` (clé `namespace/name`)

- `namespace`, `name`, `class` (`spec.gatewayClassName`).
- `accepted`, `programmed` : `true`, `false` ou `unknown` (pas de condition écrite par un contrôleur), avec `reason` et `message` de la condition `Programmed` (sinon `Accepted`).
- `addresses` : `status.addresses[].value`.
- `listeners: [{name, protocol, port, hostname, attachedRoutes, ready}]`, `ready` venant de la condition `Programmed` du listener dans `status.listeners` (`unknown` si absente).

### Routes `HTTPRoute` et `GRPCRoute`

Le kind `route` gagne deux sources, `HTTPRoute` et `GRPCRoute` (groupe `gateway.networking.k8s.io`), clés `HTTPRoute/ns/name` et `GRPCRoute/ns/name`.

- **Portes** : nouveau champ `gates: []string`, présent sur toutes les routes. Pour Ingress et IngressRoute, `gates = [gate]`. Pour une route Gateway API, une porte par `parentRef` de kind `Gateway` : `"<ns>/<name>"` (namespace de la route par défaut). `gate` reste, égal à `gates[0]`, pour la compatibilité. Un nom de porte qui contient `/` désigne un Gateway ; les noms d'IngressClass n'en contiennent jamais.
- **Règles** : une règle par `backendRef` de chaque `rules[]`. `host` = premier `spec.hostnames[]` ; `path` = premier `matches[].path.value` (HTTPRoute) ; `match` = `<service>/<method>` du premier `matches[].method` (GRPCRoute). Champ `weight` : part du trafic de la règle source, en pour mille (poids normalisés, défaut 1), publié seulement s'il y a plusieurs backends dans la règle.
- **Backends** : seuls les `backendRefs` de kind `Service` (groupe vide) sont résolus ; un autre kind est publié avec l'état `missing` et son kind.
- **État**, d'après `status.parents[]` écrit par le contrôleur (Atlas ne calcule pas les ReferenceGrant lui-même) :
  - `refused` (nouvel état) si `Accepted=False` pour toutes les portes ;
  - `missing` si `ResolvedRefs=False` (backend introuvable ou refusé par ReferenceGrant) ou si le Service n'existe pas ;
  - `ok` sinon, y compris sans statut (pas de contrôleur).
- **Index** : routes par Service visé (existant) et par Gateway parente (`ns/name`), pour qu'un Gateway créé ou supprimé recalcule ses routes.

`gatewayclasses` n'a pas d'informer : seul son YAML est lisible (il porte `controllerName`) ; ce n'est pas un objet du flux.

## Lot 3 — Traefik complet

- **IngressRouteTCP, IngressRouteUDP** : routes de sources `IngressRouteTCP` et `IngressRouteUDP`, porte déterminée comme pour les IngressRoute (annotation `kubernetes.io/ingress.class`, sinon `traefik`). `match` brut conservé (`HostSNI(...)` pour TCP, absent pour UDP), `host` extrait de `HostSNI` s'il n'est pas `*`. Une règle par service de chaque `routes[]`.
- **TraefikService** : résolu récursivement jusqu'aux Services.
  - `weighted.services[]` : chaque feuille hérite du produit des poids normalisés le long du chemin, publié en `weight` (entier, en pour mille arrondi).
  - `mirroring` : le service principal est une feuille normale ; chaque `mirrors[]` devient une feuille avec `mirror: true` et son `percent`.
  - Chaque feuille porte `via: "<ns>/<TraefikService racine>"`.
  - Un TraefikService introuvable, un cycle ou plus de 8 niveaux : une seule règle `missing` avec `kind: TraefikService`.
  - L'état `indirect` n'est plus produit (conservé dans le modèle pour la compatibilité).
- **Index** : TraefikService par Service et par TraefikService visés ; une modification d'un TraefikService ou d'un Service feuille recalcule les routes qui l'atteignent (remontée transitive par l'index, bornée à 8 niveaux).

### Modèle commun des règles

Champs ajoutés à `Backend`, tous optionnels : `weight` (int, part du trafic en pour mille, même unité pour Gateway API et Traefik), `mirror` (bool), `percent` (int, miroir), `via` (string). État : `ok`, `missing`, `refused`, `indirect` (hérité). Le front du jalon 8 reste valide sans changement.

## Lot 4 — PersistentVolumes sans PVC

### Nouveau kind `persistentVolume` (clé `name`)

Publié seulement pour un PV **sans PVC existant** : phase `Available`, `Released` ou `Failed`, ou `Bound` avec un `claimRef` vers un PVC disparu. Un PV lié à un PVC existant n'est pas publié (le PVC le représente déjà).

- `name`, `storageClass`, `capacity` (octets), `accessModes`, `reclaimPolicy`, `phase`, `claimRef` (`ns/name` de l'ancien PVC, si présent).
- Déclenchements : PV → lui-même ; PVC créé ou supprimé → le PV de son `volumeName` (index des PV par claim).

## Droits

- **ServiceAccount** (chart) : `list` et `watch` sur `customresourcedefinitions` (`apiextensions.k8s.io`), `gateways`, `httproutes`, `grpcroutes` (`gateway.networking.k8s.io`), `ingressroutetcps`, `ingressrouteudps`, `traefikservices` (`traefik.io` et `traefik.containo.us`), `persistentvolumes` ; `get` seul sur `gatewayclasses` (lue par l'onglet YAML uniquement, ni informer ni sonde) ; `get` en plus sur `gateways`, `httproutes`, `grpcroutes`, `ingressroutetcps`, `ingressrouteudps`, `traefikservices`, `persistentvolumes` pour l'onglet YAML en `auth.mode=none`. Toujours aucune écriture.
- **Sondes** : chaque type est sondé comme au jalon 8 ; refusé, il est désactivé avec un warning.
- **Filtre du flux** (`internal/access`) :
  - `gateway` exige `list gateways` dans son namespace ;
  - `route` exige `list` de sa ressource dans son namespace (`httproutes`, `grpcroutes`, `ingressroutetcps`, `ingressrouteudps`, dans leur groupe) ;
  - `persistentVolume` exige `list persistentvolumes` au niveau du cluster.
- Une route visible dont le Gateway parent est invisible garde sa porte, déduite, en état inconnu (gris).

## Démo (`internal/demo`)

- Gateway API : `infra/public` (programmé, deux listeners), `infra/internal` (`Programmed=False`, raison `AddressNotAssigned`), une HTTPRoute canary `storefront` 90/10 vers deux Services, une GRPCRoute, une HTTPRoute refusée (`Accepted=False`, `NotAllowedByListeners`).
- Traefik : un TraefikService `weighted` avec un `mirroring` imbriqué, une IngressRouteTCP vers une base de données, une IngressRouteUDP.
- Deux PV `Released` (`Retain`) et un `Available`.
- `--demo-scale` : un Gateway pour 10 namespaces, un tiers des routes en HTTPRoute, 1 PV orphelin pour 10 PVC.

## Front

### Portes

- Une porte par Gateway, libellée `ns/name`, rangée avec les portes Ingress et Traefik à l'entrée ouest, toutes triées par nom.
- Voyant d'une porte Gateway : rouge si `programmed=false` ; orange si un listener n'est pas ready ou si une de ses routes est `refused` ou `missing` ; gris si `unknown` ou Gateway invisible ; vert sinon. Les portes déduites (Ingress, Traefik) gardent leur règle du jalon 8.
- La disposition dépend en plus de l'ensemble des portes (déjà le cas) et des PV orphelins (classe, nom).

### Liens

- Une route à plusieurs portes trace une ligne principale depuis chacune.
- Une règle `refused` est dessinée comme une route cassée (rouge pointillé) avec un panneau « ⊘ » au lieu de « ? ».
- Miroirs : ligne pointillée discrète, sans paquets. Poids 0 : ligne atténuée. Les paquets restent uniformes ; le poids se lit au survol et dans l'inspecteur.
- Toujours une géométrie par famille, reconstruite seulement quand la topologie change.

### Citernes vides (PV orphelins)

Dans l'îlot de leur StorageClass, après les PVC, en fil de fer : `Available` gris, `Released` gris avec un panneau, `Failed` rouge. Aucune conduite. Rayon selon la capacité, même échelle que les PVC.

### Inspecteur (lecture seule)

| Objet | Aperçu | Onglets |
| --- | --- | --- |
| Gateway | Classe, état et raison, adresses, tableau des listeners (protocole, port, hôte, routes attachées, ready), puis ses routes avec leur état | YAML, Événements |
| HTTPRoute, GRPCRoute | Portes, hôtes, tableau règle → backend avec poids et état, conditions par parent | YAML, Événements |
| IngressRouteTCP/UDP | Porte, `match`, backends | YAML, Événements |
| Route via TraefikService | Colonnes poids et `via`, miroirs avec leur pourcentage | — |
| PV | Capacité, classe, modes d'accès, reclaim policy, phase, ancien claim | YAML, Événements |

### Recherche, vue Liste, liens profonds

- Recherche (`/`) : Gateways, nouvelles routes et PV par nom.
- Vue Liste : Gateways dans « Entrées » (avec leurs routes), PV orphelins dans « Stockage ».
- Liens profonds : `/gateways/<ns>/<nom>`, `/routes/<httproute|grpcroute|ingressroutetcp|ingressrouteudp>/<ns>/<nom>`, `/persistentvolumes/<nom>`.

## Hors jalon

TLSRoute, TCPRoute et UDPRoute de la Gateway API (canal experimental) ; calcul des ReferenceGrant par Atlas ; remplissage des citernes ; informations du PV lié dans l'inspecteur d'un PVC ; versions `v1beta1` de la Gateway API.

## Tests et critères de fin

- **Go** :
  - conversion : Gateway (conditions présentes, absentes, listeners), HTTPRoute (plusieurs parents, poids, `refused`, `ResolvedRefs=False`, backend non-Service), GRPCRoute, IngressRouteTCP/UDP, PV (chaque phase, claimRef vers un PVC disparu) ;
  - résolution des TraefikService : pondérée, imbriquée, miroir, cycle, profondeur > 8, introuvable ;
  - registre : CRD ajoutée → informer démarré et objets publiés ; CRD supprimée → informer arrêté et objets retirés ; droit refusé → repli sur la découverte au démarrage ;
  - déclenchements : Gateway supprimé → ses routes ; TraefikService modifié → les routes qui l'atteignent ; PVC supprimé → son PV ;
  - filtre d'accès sur `gateway`, les nouvelles routes et `persistentVolume` ; simulateur.
- **Vitest** : portes Gateway et leur couleur, lignes multi-portes, miroirs et poids 0, citernes vides dans la disposition, stabilité à ensemble égal.
- **e2e démo** : porte `infra/public` présente, `infra/internal` en rouge ; poids 90/10 dans l'inspecteur de `storefront` ; route refusée signalée ; PV Released inspectable ; liens profonds vers un Gateway et un PV.
- **Intégration kind** : Atlas démarré **avant** les CRD. Les CRD viennent de `make crds` (versions épinglées, sha256 vérifiés, cache `hack/crds/.cache/`) ; `make scenarios` les installe ensuite, avec aussi un Gateway et une IngressRouteUDP dans `kube-system` pour le test de bob. `make test-integration` refuse tout contexte autre qu'un `kind-*` en boucle locale et remet CRD et scénarios en état quoi qu'il arrive (`make scenarios-restore`). Les CRD Gateway API (canal standard) et Traefik, puis un Gateway (statut écrit par `kubectl patch --subresource=status`, faute de contrôleur), une HTTPRoute, un TraefikService pondéré et un PV Released. Les objets apparaissent dans le flux en moins de 5 s ; supprimer la CRD HTTPRoute retire ses routes du flux.
- **Accès** : bob (`oidc:dev`) ne reçoit aucun Gateway ni aucune route de `kube-system`, et aucun PV.
- **Charge** : `--demo-scale 100x30` et `make load-up` génèrent Gateways, HTTPRoutes et PV ; la cible d'images par seconde du jalon 8 est tenue.
- **Docs** : README (avancement, droits du ServiceAccount, choix du jalon 9, scénarios) et table des correspondances de `docs/spec.md` à jour.
