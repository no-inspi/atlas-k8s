# Socle des node pools : design

## But

Que chaque node pool se lise d'un coup d'œil comme un quartier distinct, et qu'on y lise la taille des machines. Aujourd'hui le quartier est un aplat pâle qui se confond avec le sol, et son libellé (`default-pool · e2-standard-2`) ne dit ni le CPU ni la mémoire des nodes.

Choix retenus (maquettes : `.superpowers/brainstorm/`, option B) :

- chaque quartier est posé sur un **socle surélevé** bas, à tranche visible ;
- le socle affiche la **capacité nominale par node**, regroupée par taille de machine, et le **total allouable** du pool.

Hors périmètre : couleur propre à chaque pool (la couleur reste celle du style std / spot / gpu, le relief suffit à séparer deux pools voisins), taux de réservation sur le socle.

## Données

### Go (`internal/model`, `internal/kube/convert.go`)

`model.Node` gagne `Capacity Resources \`json:"capacity"\``, lu dans `n.Status.Capacity` (cpu en millicores, mémoire en octets, pods) comme `Allocatable`. La démo (`--demo`, `--demo-scale`) renseigne une capacité cohérente avec son type d'instance et supérieure ou égale à l'allouable.

### TypeScript (`web/src/api/types.ts`)

`Node.capacity: Resources`. Les nodes fantômes (`world.ts`, nodes connus seulement par le `nodeName` de leurs pods) ont une capacité nulle. Les fixtures des tests sont complétées.

## Texte du socle (`web/src/scene/layout.ts`)

Une fonction pure `poolCaption(members: Node[]): [string, string]` remplace le calcul actuel de `label`.

**Ligne 1** : `<pool> · <type>` si tous les nodes ont le même type d'instance non vide, `<pool> · types mixtes` s'ils en ont plusieurs, `<pool>` seul si aucun n'en a.

**Ligne 2** : groupes de taille nominale puis total allouable, par exemple `3 × 2 vCPU / 8Gi · 5.79 vCPU / 18Gi allouables` (unités collées comme `fmtMem`).

- Un groupe = nodes de même capacité nominale arrondie : CPU en vCPU entiers (arrondi au plus proche, minimum 1), mémoire en Gi entiers (arrondi au plus proche). Une e2-standard-2 (capacité mémoire ≈ 7,8 Gi) s'affiche donc `2 vCPU / 8Gi`.
- Groupes triés par effectif décroissant puis par taille croissante, joints par ` + ` ; au-delà de 3 groupes : les 2 premiers puis `+ N autres`.
- Total allouable : somme des `allocatable` du pool, CPU avec `fmtCpu` (en cœurs, suffixe ` vCPU`), mémoire avec `fmtMem`. Les unités suivent le reste de l'interface (Gi, point décimal).
- Les nodes fantômes ou de capacité nulle sont exclus des groupes ; si aucun node n'a de capacité, la ligne 2 est vide et le socle n'affiche qu'une ligne.

`District` remplace `label` par `caption: [string, string]`. `LABEL_STRIP` passe de 1,0 à 1,7 pour deux lignes.

## Socle (`web/src/scene/City.tsx`)

Constante exportée `SOCLE_H = 0.35` (dans `layout.ts`, à côté des autres dimensions).

- Chaque quartier devient une boîte `width × SOCLE_H × depth` posée au sol : dessus de la couleur de zone actuelle, tranche de la même couleur assombrie (≈ 25 %), `castShadow` et `receiveShadow`.
- Le texte est peint dans la bande avant du dessus : ligne 1 en gras, ligne 2 plus petite, à gauche, comme le libellé actuel. La texture de libellé accepte deux lignes.
- Les libellés de nodes (`shortNode`, ` · cordon`) passent à `SOCLE_H + 0.03`.
- Le sol, la chaussée, les avenues, la file d'attente, les entrepôts, les îlots de stockage et les arbres ne bougent pas.

## Ce qui monte sur le socle

- **Bâtiments** (`Buildings.tsx`) : le rendu est enveloppé dans un `group` décalé de `SOCLE_H`. Les positions calculées par `world` pour la sélection d'un node ajoutent `SOCLE_H`.
- **Pods** (`Pods.tsx`) : la hauteur cible devient `SOCLE_H + PLATFORM_TOP` sur un node et reste `0.02` dans la file d'attente ; l'interpolation existante fait monter la marche. `podPositions` porte donc déjà la bonne hauteur.
- **Piles** (`Stacks.tsx`) et **sélection** (`Selection.tsx`) : les marqueurs posés sur un node ou un pod ajoutent `SOCLE_H` là où ils utilisent une hauteur fixe (marqueur de node à `3.4`), et suivent `a.y` pour les pods.
- **Liens au sol** (`GroundLinks.tsx`, `links.ts`) : ils entrent dans les allées entre les parcelles, donc passeraient sous la dalle. La hauteur d'un ruban devient fonction de la position : `SOCLE_H + ε` à l'intérieur d'un rectangle de quartier, `ε` ailleurs. Chaque segment qui traverse le bord d'un quartier est coupé au bord (point inséré), pour que la marche soit nette. Une fonction pure `linkHeight(city, x, z)` est testée à part. Les panneaux « ? » et « ⊘ » suivent la même règle.
- **Réseau** (`Network.tsx`) : portes, relais, conduites et entrepôts sont sur les avenues et rues, hors quartiers : inchangés.

## Tests

- **Go** : `convert` reprend `Status.Capacity` ; la démo produit `capacity ≥ allocatable`.
- **Vitest** :
  - `poolCaption` : pool homogène (e2-standard-2 → `2 vCPU / 8Gi`), pool mixte (deux groupes, `types mixtes`), plus de 3 groupes (`+ N autres`), nodes fantômes seuls (une ligne), type d'instance vide ;
  - `linkHeight` et la coupe des segments au bord d'un quartier ;
  - `layout.test.ts` : nouvelle profondeur de quartier (`LABEL_STRIP`).
- **Playwright** : `make e2e` reste vert (pas de capture de référence versionnée).
- **Vérification visuelle** : `make demo`, `--demo-scale 100x30` (perf et lisibilité), puis kind (`make run-kind`) en clair et en sombre.
