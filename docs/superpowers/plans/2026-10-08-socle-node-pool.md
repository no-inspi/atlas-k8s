# Socle des node pools — plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal :** chaque node pool posé sur un socle surélevé qui affiche la capacité nominale de ses nodes et son total allouable.

**Architecture :** le backend Go ajoute `capacity` (lu dans `status.capacity`) au modèle de node. Côté web, une fonction pure `poolCaption` calcule les deux lignes du socle dans `layout.ts` ; `City.tsx` dessine le socle (boîte de hauteur `SOCLE_H`) ; bâtiments, pods, piles, marqueurs et liens au sol montent de `SOCLE_H` sur les quartiers. Réseau, avenues et file d'attente restent au sol.

**Tech stack :** Go (client-go), React Three Fiber / three.js, Vitest.

Design : [`docs/superpowers/specs/2026-10-08-socle-node-pool-design.md`](../specs/2026-10-08-socle-node-pool-design.md).

**Commandes :** depuis la racine, `go test ./internal/...` ; depuis `web/`, `npx vitest run <fichier>` et `npx tsc --noEmit`. Tout le dépôt : `make test`.

---

## Carte des fichiers

| Fichier | Rôle dans ce plan |
| --- | --- |
| `internal/model/model.go` | champ `Capacity` du node |
| `internal/kube/convert.go`, `convert_test.go` | lecture de `status.capacity` |
| `internal/demo/catalog.go`, `scale.go`, `sim.go`, `sim_test.go` | capacité nominale des nodes simulés |
| `web/src/api/types.ts` | `Node.capacity` |
| `web/src/store/fixtures.ts`, `web/src/scene/world.ts` | fixture et nodes fantômes |
| `web/src/scene/layout.ts`, `layout.test.ts` | `SOCLE_H`, `poolCaption`, `District.caption`, `LABEL_STRIP` |
| `web/src/scene/City.tsx` | socle, libellé sur deux lignes, libellés de nodes surélevés |
| `web/src/scene/Buildings.tsx`, `Pods.tsx`, `Stacks.tsx`, `Selection.tsx` | décalage de `SOCLE_H` |
| `web/src/scene/links.ts`, `links.test.ts` | `inDistrict`, `splitAtDistricts` |
| `web/src/scene/GroundLinks.tsx`, `ribbon.test.ts` | hauteur des rubans selon la position |

---

### Task 1 : capacité nominale dans le modèle Go

**Files :**
- Modify : `internal/model/model.go` (struct `Node`)
- Modify : `internal/kube/convert.go` (`ConvertNode`)
- Test : `internal/kube/convert_test.go`

- [ ] **Step 1 : test qui échoue.** Dans `convert_test.go`, fonction `node(...)`, ajouter `Capacity` au `Status`, juste avant `Allocatable` :

```go
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("16Gi"),
				corev1.ResourcePods: resource.MustParse("110"),
			},
```

Puis dans `TestConvertNode`, après le contrôle des ressources :

```go
	if n.Capacity.CPU != 4000 || n.Capacity.Memory != 16<<30 || n.Capacity.Pods != 110 {
		t.Errorf("capacity = %+v", n.Capacity)
	}
```

- [ ] **Step 2 : vérifier l'échec.** `go test ./internal/kube -run TestConvertNode` → échec de compilation `n.Capacity undefined`.

- [ ] **Step 3 : implémentation.** Dans `model.go`, struct `Node`, avant `Allocatable` :

```go
	Capacity       Resources   `json:"capacity"`
```

Dans `convert.go`, `ConvertNode`, sous `alloc := n.Status.Allocatable` :

```go
	capa := n.Status.Capacity
```

et dans le littéral `model.Node`, avant `Allocatable:` :

```go
		Capacity: model.Resources{
			CPU: capa.Cpu().MilliValue(), Memory: capa.Memory().Value(), Pods: capa.Pods().Value(),
		},
```

- [ ] **Step 4 : vérifier.** `go test ./internal/kube -run TestConvertNode` → PASS.

- [ ] **Step 5 : commit.**

```bash
git add internal/model/model.go internal/kube/convert.go internal/kube/convert_test.go
git commit -m "feat(model): capacité nominale des nodes (status.capacity)"
```

---

### Task 2 : capacité des nodes de la démo

**Files :**
- Modify : `internal/demo/catalog.go` (`poolDef`, `pools`)
- Modify : `internal/demo/scale.go` (catalogue `--demo-scale`)
- Modify : `internal/demo/sim.go` (`buildNodes`)
- Test : `internal/demo/sim_test.go`

- [ ] **Step 1 : test qui échoue.** Ajouter à `sim_test.go`, après `TestNodeRequestedTracksPods` :

```go
func TestNodeCapacityCoversAllocatable(t *testing.T) {
	_, sink := start(10)
	for name, n := range sink.nodes {
		if n.Capacity.CPU < n.Allocatable.CPU || n.Capacity.Memory < n.Allocatable.Memory || n.Capacity.CPU == 0 {
			t.Errorf("%s : capacity %+v, allocatable %+v", name, n.Capacity, n.Allocatable)
		}
	}
}
```

- [ ] **Step 2 : vérifier l'échec.** `go test ./internal/demo -run TestNodeCapacityCoversAllocatable` → FAIL (capacity nulle).

- [ ] **Step 3 : implémentation.** `catalog.go`, struct `poolDef` :

```go
type poolDef struct {
	Name, Machine string
	CPU           int64 // allocatable, millicores
	Mem           int64 // allocatable, octets
	CapCPU        int64 // capacité nominale, millicores
	CapMem        int64 // capacité nominale, octets
	Spot          bool
	GPU           int
	Count         int
}

var pools = []poolDef{
	{Name: "default-pool", Machine: "e2-standard-4", CPU: 3920, Mem: 13000 * mi, CapCPU: 4000, CapMem: 16 * gi, Count: 3},
	{Name: "spot-pool", Machine: "e2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, Spot: true, Count: 2},
	{Name: "gpu-pool", Machine: "g2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, GPU: 1, Count: 1},
}
```

`scale.go`, catalogue :

```go
	c := catalog{pools: []poolDef{
		{Name: "default-pool", Machine: "e2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, Count: std},
		{Name: "spot-pool", Machine: "e2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, Spot: true, Count: spot},
		{Name: "gpu-pool", Machine: "g2-standard-8", CPU: 7910, Mem: 27000 * mi, CapCPU: 8000, CapMem: 32 * gi, GPU: 1, Count: gpu},
	}}
```

`sim.go`, `buildNodes`, dans le littéral `model.Node`, avant `Allocatable:` :

```go
				Capacity:     model.Resources{CPU: p.CapCPU, Memory: p.CapMem, Pods: 110},
```

- [ ] **Step 4 : vérifier.** `go test ./internal/demo` → PASS (dont `TestDeterministic`).

- [ ] **Step 5 : commit.**

```bash
git add internal/demo
git commit -m "feat(demo): capacité nominale des nodes simulés"
```

---

### Task 3 : `Node.capacity` côté web

**Files :**
- Modify : `web/src/api/types.ts` (interface `Node`)
- Modify : `web/src/store/fixtures.ts` (`node()`)
- Modify : `web/src/scene/world.ts` (`ghostNodes`)

- [ ] **Step 1 : type.** `types.ts`, interface `Node`, avant `allocatable` :

```ts
  /** Capacité nominale de la machine (status.capacity). */
  capacity: Resources
```

- [ ] **Step 2 : vérifier l'échec.** `cd web && npx tsc --noEmit` → erreurs « Property 'capacity' is missing » dans `fixtures.ts` et `world.ts`.

- [ ] **Step 3 : compléter.** `fixtures.ts`, `node()`, avant `allocatable` (e2-standard-4) :

```ts
    capacity: { cpu: 4000, memory: 16384 << 20, pods: 110 },
```

`world.ts`, `ghostNodes`, avant `allocatable` :

```ts
    capacity: { cpu: 0, memory: 0 },
```

- [ ] **Step 4 : vérifier.** `cd web && npx tsc --noEmit && npx vitest run` → aucune erreur, tests verts.

- [ ] **Step 5 : commit.**

```bash
git add web/src/api/types.ts web/src/store/fixtures.ts web/src/scene/world.ts
git commit -m "feat(web): capacité nominale des nodes dans le modèle"
```

---

### Task 4 : `poolCaption` et légende des quartiers

**Files :**
- Modify : `web/src/scene/layout.ts`
- Test : `web/src/scene/layout.test.ts`

- [ ] **Step 1 : tests qui échouent.** Dans `layout.test.ts`, compléter l'import :

```ts
import { AVENUE, GATE_ZONE, layoutCity, plotGeometry, poolCaption, slotCapacity, queuePosition } from './layout'
```

et ajouter à la fin du fichier :

```ts
describe('poolCaption', () => {
  const GI = 1 << 30
  // e2-standard-2 : capacité 2 vCPU / 7,77 Gi, allouable 1,93 vCPU / 6 Gi.
  const e2s2 = (name: string) => node({
    name, pool: 'p', instanceType: 'e2-standard-2',
    capacity: { cpu: 2000, memory: 8145248 * 1024 }, allocatable: { cpu: 1930, memory: 6 * GI },
  })
  const e2s4 = (name: string) => node({
    name, pool: 'p', instanceType: 'e2-standard-4',
    capacity: { cpu: 4000, memory: 16 * GI }, allocatable: { cpu: 3920, memory: 13000 * (1 << 20) },
  })

  it('pool homogène : type, taille nominale arrondie et total allouable', () => {
    expect(poolCaption('p', [e2s2('a'), e2s2('b'), e2s2('c')])).toEqual([
      'p · e2-standard-2', '3 × 2 vCPU / 8Gi · 5.79 vCPU / 18Gi allouables',
    ])
  })

  it('pool mixte : groupes par taille, du plus nombreux au moins nombreux', () => {
    expect(poolCaption('p', [e2s4('c'), e2s2('a'), e2s2('b')])).toEqual([
      'p · types mixtes', '2 × 2 vCPU / 8Gi + 1 × 4 vCPU / 16Gi · 7.78 vCPU / 24.7Gi allouables',
    ])
  })

  it('au-delà de 3 tailles : les 2 premières puis le reste compté', () => {
    const sized = [1, 2, 3, 4].map((c) => node({ name: `n${c}`, pool: 'p', capacity: { cpu: c * 1000, memory: 4 * GI } }))
    expect(poolCaption('p', sized)[1]).toMatch(/^1 × 1 vCPU \/ 4Gi \+ 1 × 2 vCPU \/ 4Gi \+ 2 autres · /)
  })

  it('nodes fantômes ou sans capacité : une seule ligne', () => {
    const ghost = node({ name: 'g', pool: 'nodes non visibles', instanceType: '', capacity: { cpu: 0, memory: 0 }, ghost: true })
    expect(poolCaption('nodes non visibles', [ghost])).toEqual(['nodes non visibles', ''])
  })

  it('type d’instance inconnu : le nom du pool seul', () => {
    expect(poolCaption('p', [node({ instanceType: '' })])[0]).toBe('p')
  })

  it('layoutCity reporte la légende sur le quartier', () => {
    const c = layoutCity([e2s2('a')], plotGeometry(12))
    expect(c.districts[0].caption[0]).toBe('p · e2-standard-2')
  })
})
```

- [ ] **Step 2 : vérifier l'échec.** `cd web && npx vitest run src/scene/layout.test.ts` → FAIL, `poolCaption` n'est pas exporté.

- [ ] **Step 3 : implémentation.** Dans `layout.ts` :

Imports, en tête :

```ts
import type { Node } from '../api/types'
import { fmtCpu, fmtMem } from '../ui/format'
```

`District` : remplacer `label: string` par

```ts
  /** Nom et type d'instance ; capacité nominale et total allouable (vide si inconnue). */
  caption: [string, string]
```

Constantes : remplacer la ligne `LABEL_STRIP` et ajouter `SOCLE_H` :

```ts
const LABEL_STRIP = 1.7 // bande à l'avant du quartier pour sa légende sur deux lignes (jamais masquée par un bâtiment)
/** Hauteur du socle d'un quartier : bâtiments, pods et liens des quartiers sont posés dessus. */
export const SOCLE_H = 0.35
```

Après `styleOf`, ajouter :

```ts
const GI = 1 << 30

/**
 * Légende d'un quartier. Ligne 1 : pool et type d'instance (« types mixtes » s'il
 * y en a plusieurs). Ligne 2 : nodes groupés par taille nominale arrondie (vCPU
 * et Gi entiers), puis total allouable ; vide si aucun node n'a de capacité.
 */
export function poolCaption(pool: string, members: Node[]): [string, string] {
  const types = new Set(members.map((n) => n.instanceType).filter(Boolean))
  const head = types.size === 1 ? `${pool} · ${[...types][0]}` : types.size > 1 ? `${pool} · types mixtes` : pool
  const sized = members.filter((n) => !n.ghost && n.capacity.cpu > 0)
  if (!sized.length) return [head, '']
  const groups = new Map<string, { cpu: number; mem: number; count: number }>()
  for (const n of sized) {
    const cpu = Math.max(1, Math.round(n.capacity.cpu / 1000)), mem = Math.round(n.capacity.memory / GI)
    const g = groups.get(`${cpu}/${mem}`) ?? { cpu, mem, count: 0 }
    g.count++
    groups.set(`${cpu}/${mem}`, g)
  }
  const sorted = [...groups.values()].sort((a, b) => b.count - a.count || a.cpu - b.cpu || a.mem - b.mem)
  const fmt = (g: { cpu: number; mem: number; count: number }) => `${g.count} × ${g.cpu} vCPU / ${g.mem}Gi`
  const sizes = sorted.length > 3 ? [...sorted.slice(0, 2).map(fmt), `${sorted.length - 2} autres`] : sorted.map(fmt)
  const cpu = sized.reduce((s, n) => s + n.allocatable.cpu, 0)
  const mem = sized.reduce((s, n) => s + n.allocatable.memory, 0)
  return [head, `${sizes.join(' + ')} · ${fmtCpu(cpu)} vCPU / ${fmtMem(mem)} allouables`]
}
```

Dans `layoutCity`, objet `boxes` : remplacer `label: sample.instanceType ? ... : pool,` par `caption: poolCaption(pool, members),` et supprimer la ligne `const sample = members[0]`. Dans `districts.push(...)`, remplacer `label: p.label` par `caption: p.caption`.

- [ ] **Step 4 : vérifier.** `cd web && npx vitest run src/scene/layout.test.ts` → PASS. `npx tsc --noEmit` → une erreur attendue dans `City.tsx` (`d.label`), corrigée à la tâche 5.

- [ ] **Step 5 : commit** (avec la tâche 5, pour ne pas laisser `tsc` en échec : passer directement à la tâche 5).

---

### Task 5 : le socle dans `City.tsx`

**Files :**
- Modify : `web/src/scene/City.tsx`

Pas de test unitaire (rendu three.js) : vérification par `tsc` et visuelle (tâche 9).

- [ ] **Step 1 : texture sur plusieurs lignes.** Remplacer `labelTexture` et `GroundLabel` par :

```tsx
function labelTexture(lines: readonly string[], w: number, h: number, align: CanvasTextAlign, size: number, theme: Theme) {
  const key = [lines.join('\n'), w, h, align, size, theme.muted, theme.font].join('|')
  let tex = textures.get(key)
  if (tex) return tex
  const c = document.createElement('canvas')
  c.width = Math.round(w * 64)
  c.height = Math.round(h * 64)
  const g = c.getContext('2d')!
  const rows = lines.filter(Boolean)
  g.fillStyle = theme.muted
  g.textBaseline = 'middle'
  g.textAlign = align
  rows.forEach((text, i) => {
    // Première ligne en gras, les suivantes plus petites ; police réduite si le texte déborde.
    const weight = i === 0 ? 600 : 500
    let px = i === 0 ? size : Math.round(size * 0.72)
    g.font = `${weight} ${px}px ${theme.font}`
    while (px > 12 && g.measureText(text).width > c.width - 16) g.font = `${weight} ${(px -= 2)}px ${theme.font}`
    g.fillText(text, align === 'left' ? 8 : c.width / 2, (c.height * (i + 0.5)) / rows.length)
  })
  tex = new THREE.CanvasTexture(c)
  tex.colorSpace = THREE.SRGBColorSpace
  tex.anisotropy = 4
  if (textures.size > 400) textures.clear()
  textures.set(key, tex)
  return tex
}

function GroundLabel(props: { text: string | readonly string[]; w: number; h: number; x: number; z: number; y?: number; align?: CanvasTextAlign; size?: number; theme: Theme }) {
  const { text, w, h, x, z, y = 0.03, align = 'left', size = 56, theme } = props
  const map = labelTexture(typeof text === 'string' ? [text] : text, w, h, align, size, theme)
  return (
    <mesh rotation-x={-Math.PI / 2} position={[x, y, z]} raycast={() => null}>
      <planeGeometry args={[w, h]} />
      <meshBasicMaterial map={map} transparent depthWrite={false} />
    </mesh>
  )
}
```

- [ ] **Step 2 : composant `Socle`.** Après `Plane`, ajouter :

```tsx
/** Socle d'un quartier : dessus de la couleur de zone, tranches assombries. */
function Socle({ d, color }: { d: District; color: string }) {
  const side = useMemo(() => '#' + new THREE.Color(color).multiplyScalar(0.75).getHexString(), [color])
  return (
    <mesh position={[d.x, SOCLE_H / 2, d.z]} castShadow receiveShadow raycast={() => null}>
      <boxGeometry args={[d.width, SOCLE_H, d.depth]} />
      {/* Faces de la boîte : +x, -x, +y (dessus), -y, +z, -z. */}
      {[0, 1, 2, 3, 4, 5].map((i) => (
        <meshStandardMaterial key={i} attach={`material-${i}`} color={i === 2 ? color : side} roughness={0.95} />
      ))}
    </mesh>
  )
}
```

Import en tête :

```ts
import { SOCLE_H, type CityLayout, type District, type DistrictStyle } from './layout'
```

- [ ] **Step 3 : utiliser le socle.** Dans `City`, remplacer le bloc `layout.districts.map(...)` par :

```tsx
      {layout.districts.map((d) => {
        const w = Math.min(d.width - 0.4, 14)
        return (
          <group key={d.pool}>
            <Socle d={d} color={zone[d.style]} />
            <GroundLabel text={d.caption} w={w} h={1.5} x={d.x - d.width / 2 + 0.2 + w / 2} z={d.z + d.depth / 2 - 0.85} y={SOCLE_H + 0.02} theme={theme} />
          </group>
        )
      })}
```

et, dans le bloc des libellés de parcelles, ajouter `y={SOCLE_H + 0.03}` au `GroundLabel` des nodes.

- [ ] **Step 4 : vérifier.** `cd web && npx tsc --noEmit && npx vitest run` → aucune erreur.

- [ ] **Step 5 : commit (tâches 4 et 5).**

```bash
git add web/src/scene/layout.ts web/src/scene/layout.test.ts web/src/scene/City.tsx
git commit -m "feat(scene): socle des node pools et légende de capacité"
```

---

### Task 6 : bâtiments, pods, piles et marqueurs sur le socle

**Files :**
- Modify : `web/src/scene/Buildings.tsx` (rendu final)
- Modify : `web/src/scene/Pods.tsx` (hauteur cible)
- Modify : `web/src/scene/Stacks.tsx` (sprite)
- Modify : `web/src/scene/Selection.tsx` (marqueur de node)

Pas de test unitaire (rendu) : `tsc`, tests existants, puis vérification visuelle.

- [ ] **Step 1 : bâtiments.** `Buildings.tsx`, ajouter `SOCLE_H` à l'import depuis `./layout` (ou créer `import { SOCLE_H } from './layout'` s'il n'y en a pas), puis remplacer `return <group ref={group} />` par :

```tsx
  // Bâtiments posés sur le socle de leur quartier ; le picking suit la matrice du groupe.
  return <group ref={group} position-y={SOCLE_H} />
```

- [ ] **Step 2 : pods.** `Pods.tsx`, importer `SOCLE_H` depuis `./layout`, et remplacer :

```ts
      const ty = target.onNode ? PLATFORM_TOP : 0.02
```

par

```ts
      const ty = target.onNode ? SOCLE_H + PLATFORM_TOP : 0.02
```

- [ ] **Step 3 : piles.** `Stacks.tsx`, importer `SOCLE_H` depuis `./layout`, et remplacer `position={[st.x, 0.5 + st.top + 0.55, st.z]}` par :

```tsx
position={[st.x, SOCLE_H + 0.5 + st.top + 0.55, st.z]}
```

- [ ] **Step 4 : marqueur de node.** `Selection.tsx`, importer `SOCLE_H` depuis `./layout`, et remplacer `marker.position.set(p.x, 3.4 + bob, ...)` par :

```ts
        marker.position.set(p.x, SOCLE_H + 3.4 + bob, p.z - world.layout.geometry.depth / 2 + 0.5)
```

(Le marqueur de pod suit déjà `a.y`, qui inclut `SOCLE_H`.)

- [ ] **Step 5 : vérifier.** `cd web && npx tsc --noEmit && npx vitest run` → vert.

- [ ] **Step 6 : commit.**

```bash
git add web/src/scene/Buildings.tsx web/src/scene/Pods.tsx web/src/scene/Stacks.tsx web/src/scene/Selection.tsx
git commit -m "feat(scene): bâtiments, pods et marqueurs posés sur le socle"
```

---

### Task 7 : coupe des liens au bord des quartiers

**Files :**
- Modify : `web/src/scene/links.ts`
- Test : `web/src/scene/links.test.ts`

- [ ] **Step 1 : tests qui échouent.** Dans `links.test.ts`, compléter l'import :

```ts
import { buildLinks, inDistrict, isLit, PathMemo, pathOf, podUidsOf, sameTopology, splitAtDistricts, type Link } from './links'
```

et ajouter à la fin :

```ts
describe('quartiers et liens', () => {
  const d = city.districts[0]
  const west = d.x - d.width / 2, south = d.z + d.depth / 2

  it('inDistrict : intérieur et bords compris, extérieur exclu', () => {
    expect(inDistrict(city, d.x, d.z)).toBe(true)
    expect(inDistrict(city, west, d.z)).toBe(true)
    expect(inDistrict(city, west - 0.01, d.z)).toBe(false)
  })

  it('splitAtDistricts : un point au bord quand le segment entre dans un quartier', () => {
    const pts = splitAtDistricts(city, [[d.x, south + 2], [d.x, d.z]])
    expect(pts).toHaveLength(3)
    expect(pts[1][0]).toBeCloseTo(d.x)
    expect(pts[1][1]).toBeCloseTo(south)
  })

  it('splitAtDistricts : rien à couper hors des quartiers ou dedans', () => {
    expect(splitAtDistricts(city, [[west - 5, south + 1], [west - 1, south + 1]])).toHaveLength(2)
    expect(splitAtDistricts(city, [[d.x - 0.5, d.z], [d.x + 0.5, d.z]])).toHaveLength(2)
  })
})
```

- [ ] **Step 2 : vérifier l'échec.** `cd web && npx vitest run src/scene/links.test.ts` → FAIL, exports manquants.

- [ ] **Step 3 : implémentation.** Dans `links.ts`, après `dedupe` :

```ts
/** Point (x, z) dans un quartier, bords compris. */
export function inDistrict(city: CityLayout, x: number, z: number): boolean {
  return city.districts.some((d) => Math.abs(x - d.x) <= d.width / 2 + 1e-6 && Math.abs(z - d.z) <= d.depth / 2 + 1e-6)
}

/**
 * Polyligne coupée aux bords des quartiers qu'elle traverse : chaque segment est
 * ensuite entièrement sur un socle ou entièrement au sol.
 */
export function splitAtDistricts(city: CityLayout, pts: readonly Pt[]): Pt[] {
  const out: Pt[] = []
  pts.forEach((p, i) => {
    if (i > 0) {
      const q = pts[i - 1]
      const dx = p[0] - q[0], dz = p[1] - q[1]
      const cuts: number[] = []
      const keep = (t: number) => t > 1e-6 && t < 1 - 1e-6
      for (const d of city.districts) {
        const x0 = d.x - d.width / 2, x1 = d.x + d.width / 2, z0 = d.z - d.depth / 2, z1 = d.z + d.depth / 2
        if (dx) for (const ex of [x0, x1]) {
          const t = (ex - q[0]) / dx, z = q[1] + t * dz
          if (keep(t) && z >= z0 && z <= z1) cuts.push(t)
        }
        if (dz) for (const ez of [z0, z1]) {
          const t = (ez - q[1]) / dz, x = q[0] + t * dx
          if (keep(t) && x >= x0 && x <= x1) cuts.push(t)
        }
      }
      for (const t of cuts.sort((a, b) => a - b)) out.push([q[0] + t * dx, q[1] + t * dz])
    }
    out.push(p)
  })
  return out
}
```

- [ ] **Step 4 : vérifier.** `cd web && npx vitest run src/scene/links.test.ts` → PASS.

- [ ] **Step 5 : commit.**

```bash
git add web/src/scene/links.ts web/src/scene/links.test.ts
git commit -m "feat(scene): coupe des liens au bord des quartiers"
```

---

### Task 8 : rubans surélevés sur les quartiers

**Files :**
- Modify : `web/src/scene/GroundLinks.tsx` (`ribbon`, `setGeometry`, réécriture d'opacité)
- Test : `web/src/scene/ribbon.test.ts`

- [ ] **Step 1 : test qui échoue.** Dans `ribbon.test.ts`, bloc `describe('ribbon')`, ajouter :

```ts
  it('relève chaque segment selon la hauteur à son milieu', () => {
    const lift = (x: number) => (x > 2 ? 0.35 : 0)
    const r = ribbon([{ points: [[0, 0], [2, 0], [4, 0]], alpha: 1, live: true }], 0.2, 0.03, lift)
    const ys = [...r.position].filter((_, i) => i % 3 === 1).map((y) => Math.round(y * 100) / 100)
    expect(ys).toEqual([0.03, 0.03, 0.03, 0.03, 0.38, 0.38, 0.38, 0.38])
  })
```

- [ ] **Step 2 : vérifier l'échec.** `cd web && npx vitest run src/scene/ribbon.test.ts` → FAIL (4ᵉ argument ignoré, toutes les hauteurs à 0,03).

- [ ] **Step 3 : `ribbon`.** Nouvelle signature et hauteur par segment :

```ts
/**
 * Rubans de largeur w le long des polylignes ; dist : abscisse curviligne (motif
 * continu aux coudes). lift : surélévation d'un segment selon son milieu (socles).
 */
export function ribbon(items: readonly RibbonItem[], w: number, y: number, lift?: (x: number, z: number) => number): Ribbon {
```

Dans la boucle, après le calcul de `ex, ez` :

```ts
      const sy = y + (lift ? lift((x0 + x1) / 2, (z0 + z1) / 2) : 0)
```

et remplacer les quatre `position[o++] = y;` par `position[o++] = sy;`.

- [ ] **Step 4 : composant.** Imports :

```ts
import { inDistrict, isLit, splitAtDistricts, type Family, type Link, type Pt } from './links'
import { SOCLE_H } from './layout'
```

Dans `built`, ajouter un champ `points: new Map<Family, Pt[][]>()` (type : `points: Map<Family, Pt[][]>`). Remplacer `setGeometry` par :

```ts
    const city = world.layout
    const lift = (x: number, z: number) => (city && inDistrict(city, x, z) ? SOCLE_H : 0)
    const setGeometry = (f: Family, ls: Link[]) => {
      const mesh = meshes.get(f)!
      // Coupés aux bords des quartiers : chaque segment est sur un socle ou au sol.
      const pts = ls.map((l) => (city ? splitAtDistricts(city, l.points) : [...l.points]))
      built.current.points.set(f, pts)
      mesh.geometry.dispose()
      mesh.geometry = geometryOf(ribbon(ls.map((l, i) => ({ points: pts[i], alpha: alphaOf(l), live: DASHED.has(l.family) || l.live })), WIDTH[f], Y[f], lift))
    }
```

Dans la réécriture d'opacité, remplacer `writeAlpha(attr.array as Float32Array, ls, (i) => alphaOf(ls[i]))` par :

```ts
writeAlpha(attr.array as Float32Array, b.points.get(f)!.map((points) => ({ points })), (i) => alphaOf(ls[i]))
```

(Les panneaux « ? » et « ⊘ » sont sur les voies des avenues, jamais dans un quartier : inchangés.)

- [ ] **Step 5 : vérifier.** `cd web && npx tsc --noEmit && npx vitest run` → vert.

- [ ] **Step 6 : commit.**

```bash
git add web/src/scene/GroundLinks.tsx web/src/scene/ribbon.test.ts
git commit -m "feat(scene): liens au sol surélevés sur les socles"
```

---

### Task 9 : vérification complète

- [ ] **Step 1 : tests.** `make test` → `go vet`, `go test -race`, tests du chart et Vitest verts.

- [ ] **Step 2 : e2e.** `make e2e` → vert.

- [ ] **Step 3 : démo.** `make demo`, ouvrir http://localhost:8080 et vérifier, en clair puis en sombre :
  - trois socles distincts, tranche visible, ombre portée ;
  - légende `default-pool · e2-standard-4` / `3 × 4 vCPU / 16Gi · 11.76 vCPU / 38.1Gi allouables` ;
  - bâtiments et pods posés sur le socle (pas enfoncés, pas flottants) ; un pod qui passe de la file d'attente à un node monte la marche ;
  - liens au sol visibles dans les allées, marche nette au bord du quartier ;
  - sélection d'un node et d'un pod : marqueur au-dessus, pas dans le bâtiment ; clic sur un bâtiment sélectionne bien son node.

- [ ] **Step 4 : charge.** `bin/atlas --demo-scale 100x30`, `?perf=1` : images/s comparables à `main` (6 matériaux par socle, une boîte par pool : négligeable).

- [ ] **Step 5 : kind.** `make run-kind` : légendes avec les capacités réelles des nodes kind.
