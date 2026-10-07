# Jalon 8 — Réseau et stockage dans la ville : design

Premier chantier hors MVP (spec, « Services, Ingress, PVC représentés dans la 3D »). Il ajoute à la ville les Services, les entrées (Ingress et IngressRoute Traefik) et les PVC, sans action d'écriture.

## But

Répondre d'un coup d'œil à **« mon application est-elle joignable et ses données sont-elles là ? »** (santé de l'exposition), avec la **topologie** en appui : on voit qui reçoit le trafic de qui et quel pod utilise quel volume en sélectionnant un objet. Les anomalies se voient en permanence : Service sans endpoint ready, route vers un Service introuvable, PVC Pending ou Lost.

## Métaphore retenue : avenue et entrepôts

La ville gagne une structure : une entrée, des avenues, une zone industrielle.

| Objet Kubernetes | Représentation | Ce qui est encodé |
| --- | --- | --- |
| Contrôleur d'entrée (IngressClass, ou `traefik` pour les IngressRoute) | **Porte** en arche à l'entrée ouest de la première avenue | Nombre de routes ; orange si une de ses routes est cassée |
| Ingress, IngressRoute | **Route** : ligne principale de la porte vers chaque Service visé | Hôtes, chemins ou `match`, état du backend |
| Service | **Relais** : disque posé sur l'avenue, bordé de la couleur du namespace, voyant dessus | Voyant vert (`ok`), orange (`degraded`), rouge clignotant (`down`) ; panneau pour `ExternalName` |
| Endpoints d'un Service | **Fibres** au sol, du relais aux pods par les rues | Visibles au survol ou à la sélection |
| PVC | **Citerne** dans le quartier Entrepôts, un îlot par StorageClass | Rayon selon la capacité (log, borné) ; Pending en pointillés orange, Lost en rouge |
| Montage d'un PVC par un pod | **Conduite** au sol de la citerne au pod | Toujours visible ; gouttes de data quand le pod est Running |

Le remplissage des citernes (espace utilisé) est hors jalon. Il demanderait soit `nodes/proxy` (exclu : quasi root sur les nodes), soit une source Prometheus, à traiter avec les métriques historiques.

## Modèle et backend

### Trois nouveaux kinds dans `/api/stream` (`internal/model`)

**`service`** (clé `namespace/name`)

- `namespace`, `name`, `type` (`ClusterIP`, `NodePort`, `LoadBalancer`, `ExternalName`), `headless`, `ports: [{name, port, targetPort, protocol, nodePort}]`, `loadBalancer: [ip|hostname]`, `externalName`.
- `endpoints: [{podUID, ready}]`, lus dans les EndpointSlices (label `kubernetes.io/service-name`, `targetRef.uid`, `conditions.ready`), dédoublonnés par pod.
- `health` calculé côté serveur : `ok` (au moins un endpoint, tous ready), `degraded` (une partie ready), `down` (aucun endpoint ready ou aucun endpoint), `external` (ExternalName). Les endpoints des pods en arrêt (`terminating`) sont ignorés, pour qu'un déploiement progressif ne dégrade pas le Service.
- Un Service sans selector et sans EndpointSlice n'est pas publié. Un ExternalName l'est toujours.

**`route`** (clé `source/namespace/name`)

- `source` : `Ingress` ou `IngressRoute`.
- `gate`, déterminé dans cet ordre :
  - Ingress : `spec.ingressClassName`, sinon l'annotation `kubernetes.io/ingress.class`, sinon l'IngressClass annotée `ingressclass.kubernetes.io/is-default-class: "true"`, sinon `default`.
  - IngressRoute : l'annotation `kubernetes.io/ingress.class` si elle est présente, sinon `traefik`.
- `rules: [{host, path, match, backend: {namespace, service, port, kind, state}}]`.
  - Ingress : une règle par couple (host, path), plus `defaultBackend` (host et path vides).
  - IngressRoute : une règle par service de chaque `routes[]` ; `match` est conservé brut, `host` et `path` sont extraits de `Host(`…`)` et de `PathPrefix(`…`)` ou `Path(`…`)` quand ils sont présents (premier de chaque).
  - `backend.namespace` : celui de la route, ou `services[].namespace` pour une IngressRoute.
  - `state` : `ok` (Service existant), `missing` (Service introuvable), `indirect` (`kind: TraefikService`, non résolu dans ce jalon).
- `addresses` : `status.loadBalancer.ingress` de l'Ingress (vide pour une IngressRoute).

**`volume`** (clé `namespace/name`)

- `namespace`, `name`, `storageClass`, `requested` (octets), `capacity` (octets, 0 si non lié), `accessModes`, `phase` (`Pending`, `Bound`, `Lost`), `volumeName`.
- `pods: [uid]` : pods qui le montent.

### Source (`internal/kube`)

- Nouveaux informers partagés : Services, EndpointSlices (`discovery.k8s.io/v1`), Ingress, IngressClass, PVC, tous allégés par le transform existant (pas de managedFields).
- IngressRoute : informer dynamique sur `ingressroutes`, démarré pour chaque groupe trouvé par la découverte d'API au démarrage, `traefik.io/v1alpha1` et/ou `traefik.containo.us/v1alpha1`. Une CRD installée après le démarrage est prise en compte au prochain redémarrage (documenté).
- Le transform des pods conserve désormais, parmi les volumes, les seuls `persistentVolumeClaim.claimName` et les volumes `ephemeral` (PVC `<pod>-<volume>`). Rien d'autre.
- Index : pods par PVC monté (`namespace/claim`), routes par Service visé (`namespace/service`).
- Déclenchements dans la boucle « objets sales » :
  - EndpointSlice → son Service ;
  - pod (ajout, changement, suppression) → ses PVC ;
  - PVC → son volume ;
  - Service créé ou supprimé → les routes qui le visent ;
  - IngressClass (changement de la classe par défaut) → toutes les Ingress.
- Un type dont le `list` est refusé au ServiceAccount (RBAC restreint, chart ancien) est désactivé avec un warning dans les logs. `/readyz` attend seulement les informers démarrés, et le reste de la ville fonctionne.

### Droits

- **ServiceAccount** (chart) : `list` et `watch` sur `services`, `persistentvolumeclaims`, `endpointslices` (`discovery.k8s.io`), `ingresses` et `ingressclasses` (`networking.k8s.io`), `ingressroutes` (`traefik.io` et `traefik.containo.us`). Toujours aucune écriture.
- **Filtre du flux** (`internal/access`), même mécanisme que pour les pods :
  - `service` exige `list services` dans son namespace ;
  - `route` exige `list ingresses` (`networking.k8s.io`) ou `list ingressroutes` (groupe de la ressource) ;
  - `volume` exige `list persistentvolumeclaims`.
- Les portes ne sont pas des objets du flux : le front les déduit des routes reçues. Un endpoint ou un montage qui pointe vers un pod invisible pour l'utilisateur est ignoré par le front.

### Démo (`internal/demo`)

Le simulateur produit le même modèle :
- des Services sur ses workloads (dont un `degraded` pendant les rollouts et un `down` derrière le pod en CrashLoopBackOff) et un `ExternalName` ;
- des routes Ingress (porte `nginx`) et IngressRoute (porte `traefik`), dont une vers un Service manquant ;
- des PVC Bound sur le StatefulSet et un PVC Pending.

`--demo-scale` génère environ 4 Services pour 30 pods et 1 PVC pour 20 pods.

## Front

### Disposition (`web/src/scene/layout.ts`, fonction pure)

- **Avenues** : une entre chaque paire de rangées de quartiers. Avec une seule rangée, une avenue devant elle, entre la ville et la file Pending. Largeur : une rangée de relais et deux voies de liens.
- **Tronçons** : les namespaces ayant au moins un relais visible, triés par nom, remplissent les avenues dans l'ordre. Chaque tronçon porte une bande de la couleur du namespace et son nom peint au sol. Les relais y sont rangés par nom, à pas fixe ; un nouveau Service décale ses voisins du tronçon (animé). Si une avenue est pleine, le tronçon continue sur la suivante. S'il n'y a plus de place, les relais restants du tronçon s'empilent avec un compteur.
- **Portes** : à l'entrée ouest de la première avenue, une par `gate`, triées par nom.
- **Entrepôts** : un quartier à l'est de la ville, un îlot par StorageClass (trié par nom ; `(aucune)` pour un PVC sans classe), citernes rangées par namespace puis par nom.
- La disposition ne dépend que des nodes, des Services visibles (namespace, nom) et des PVC visibles (classe, namespace, nom).

### Liens (routés en angles droits par les avenues et les allées, jamais à travers un bâtiment)

| Famille | Trajet | Visibilité |
| --- | --- | --- |
| Lignes principales | porte → relais, une par couple (porte, Service) | Toujours, discrètes, paquets animés |
| Routes cassées | porte → tronçon du namespace, panneau « ? » | Toujours, rouge pointillé |
| Conduites | citerne → avenue → allée → pod | Toujours ; gouttes animées si le pod est Running |
| Fibres | relais → avenue → allée → pod | Survol ou sélection d'un relais, d'une porte, d'un pod ou d'un workload |

Chaque famille est une seule géométrie (un draw call), reconstruite quand la topologie change. L'animation est un décalage dans le shader. `prefers-reduced-motion` coupe les paquets, les gouttes et le clignotement.

### Interactions

- Clic sur une porte, un relais ou une citerne : ouvre l'inspecteur et pose le marqueur de sélection.
- Sélection : tout le chemin s'allume (porte → relais → pods → citernes) et le reste passe à l'opacité réduite du filtre de namespace. Pour un pod sélectionné, on allume ses relais, leurs portes et ses citernes.
- Les chips de namespace filtrent aussi relais, routes et citernes.
- Recherche (`/`) : Services, routes et PVC par nom.
- Vue Liste : trois nouveaux groupes, Entrées (portes, puis leurs routes), Services, Stockage.
- Liens profonds : `?select=service/<ns>/<nom>`, `route/<source>/<ns>/<nom>`, `volume/<ns>/<nom>`, `gate/<nom>`.

### Échelle

- Vu de loin, les relais d'un tronçon se fondent en un bloc avec un compteur, dont le voyant prend la pire couleur du groupe. Fibres, paquets et gouttes n'apparaissent que de près.
- Relais et citernes sont des `InstancedMesh` (un par pièce). Le rendu reste à la demande.
- Banc : 100 nodes, 3 000 pods, environ 400 Services et 150 PVC, avec la même cible que la spec (60 images/s sur un portable récent).

### Thème

Nouveaux tokens CSS, en clair et en sombre : `--avenue`, `--warehouse`, `--fibre`, `--data`.

## Inspecteur (lecture seule)

| Objet | Aperçu | Onglets |
| --- | --- | --- |
| Service | Type, IP, ports → cible, endpoints ready/total avec lien vers chaque pod, routes qui le visent | YAML, Événements |
| Route | Porte, adresses, tableau hôte · chemin ou `match` → backend avec son état | YAML, Événements |
| Porte | Liste de ses routes avec leur état | — |
| PVC | Classe, capacité demandée et réelle, modes d'accès, phase, pods qui le montent ; si Pending, dernier événement `ProvisioningFailed` ou `WaitForFirstConsumer` en évidence | YAML, Événements |

Le YAML et les événements passent par le client impersonné, comme pour les pods ; client dynamique pour les IngressRoute. Les 403 et 404 sont affichés tels quels. Aucune action nouvelle.

## Hors jalon

IngressRouteTCP, IngressRouteUDP, Gateway API (HTTPRoute), résolution des TraefikService, remplissage des citernes, PersistentVolumes sans PVC, prise en compte à chaud d'une CRD installée après le démarrage.

## Tests et critères de fin

- **Go** :
  - conversion : Service et `health` ; Ingress avec ses trois sources de classe et le `defaultBackend` ; IngressRoute (`Host()`, `PathPrefix()`, namespace de backend, TraefikService) ; backend manquant ; PVC et pods qui le montent, volume éphémère ;
  - déclenchements : un endpoint qui passe non ready met à jour le Service ; supprimer un Service passe ses routes en `missing` ;
  - filtre d'accès sur les trois kinds ; type désactivé quand le `list` est refusé ; simulateur.
- **Vitest** : disposition (avenues selon le nombre de rangées, tronçons, débordement, portes, entrepôts, stabilité à ensemble égal), routage des liens, `health` → couleur.
- **e2e démo** : portes `nginx` et `traefik` présentes ; relais `down` en rouge ; clic sur un relais → ses pods allumés ; PVC Pending inspectable ; vue Liste avec les trois groupes ; lien profond vers un Service.
- **Intégration kind** : `make scenarios` ajoute un Ingress nginx, une IngressRoute (CRD Traefik installée par le script), un Service sans endpoint et un PVC Pending. Supprimer un pod endpoint met à jour son Service dans le flux en moins de 2 s.
- **Accès** : bob (`oidc:dev`) ne reçoit aucun Service, aucune route et aucun PVC de `kube-system`.
- **Charge** : `--demo-scale 100x30` et `make load-up` génèrent Services et PVC ; la cible d'images par seconde est tenue.
- **Docs** : README (architecture, droits du ServiceAccount, scénarios) et table des correspondances de `docs/spec.md` à jour.
