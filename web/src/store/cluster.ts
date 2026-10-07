import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import {
  routeKey, serviceKey, volumeKey, workloadKey,
  type Me, type Message, type Metrics, type Namespace, type Node, type Pod, type Route, type Service, type Volume, type Workload,
} from '../api/types'
import { loadView, saveView, type PodView } from '../scene/podView'
import { feedForNode, feedForPod, type FeedDraft, type FeedItem, type FeedLevel } from './feed'

export interface Toast {
  id: number
  text: string
  level: FeedLevel
}

export type SelectionType = 'pod' | 'node' | 'service' | 'route' | 'volume' | 'gate'
export type Selection = { type: SelectionType; key: string; name: string } | null
export type InspectorTab = 'overview' | 'logs' | 'terminal' | 'yaml' | 'events'
/** Objet affiché par l'onglet YAML (null : workload racine du pod). */
export type YamlTarget = { kind: string; name: string } | null
export type Connection = 'connecting' | 'live' | 'reconnecting'

const FEED_SIZE = 5

/**
 * État normalisé du cluster. Les Map sont mutées en place et `version` est
 * incrémenté à chaque lot : la scène lit getState() dans sa boucle de rendu,
 * les composants React s'abonnent à `version` quand ils doivent se redessiner.
 */
export interface ClusterState {
  connection: Connection
  rev: number
  version: number
  nodes: Map<string, Node>
  pods: Map<string, Pod>
  workloads: Map<string, Workload>
  namespaces: Map<string, Namespace>
  services: Map<string, Service>
  routes: Map<string, Route>
  volumes: Map<string, Volume>
  metrics: Metrics
  me: Me | null
  selection: Selection
  /** Onglet actif, conservé quand on passe d'un pod à l'autre. */
  inspectorTab: InspectorTab
  yamlTarget: YamlTarget
  /** Point de la ville à centrer (recherche) ; seq change à chaque demande. */
  focus: { x: number; z: number; seq: number } | null
  /** Vue principale : ville 3D ou liste accessible. */
  view: '3d' | 'list'
  nsFilter: string | null
  /** Tri, hauteur et masquage des pods dans la ville (conservés dans le navigateur). */
  podView: PodView
  /** Pod (ou pile) sous le pointeur, et position du pointeur dans la page. */
  hover: { uid: string; x: number; y: number } | null
  /** Porte, relais ou citerne sous le pointeur (« type:clé ») : ses fibres s'affichent. */
  hoverNet: string | null
  feed: FeedItem[]
  toasts: Toast[]

  applyMessages(msgs: Message[]): void
  setConnection(c: Connection): void
  resetRev(): void
  setMe(me: Me): void
  select(sel: { type: SelectionType; key: string } | null): void
  toggleNsFilter(ns: string): void
  setPodView(patch: Partial<PodView>): void
  setHover(hover: { uid: string; x: number; y: number } | null): void
  setHoverNet(key: string | null): void
  setInspectorTab(tab: InspectorTab): void
  openYaml(target: YamlTarget): void
  /** Résultat d'une action : notification et entrée dans le bandeau d'événements. */
  notify(text: string, level?: FeedLevel): void
  dismissToast(id: number): void
  focusOn(x: number, z: number): void
  setView(view: '3d' | 'list'): void
  reset(): void
}

let feedSeq = 0

const initial = () => ({
  connection: 'connecting' as Connection,
  rev: 0,
  version: 0,
  nodes: new Map<string, Node>(),
  pods: new Map<string, Pod>(),
  workloads: new Map<string, Workload>(),
  namespaces: new Map<string, Namespace>(),
  services: new Map<string, Service>(),
  routes: new Map<string, Route>(),
  volumes: new Map<string, Volume>(),
  metrics: { pods: {}, nodes: {} } as Metrics,
  me: null,
  selection: null as Selection,
  inspectorTab: 'overview' as InspectorTab,
  yamlTarget: null as YamlTarget,
  focus: null as { x: number; z: number; seq: number } | null,
  view: '3d' as '3d' | 'list',
  nsFilter: null,
  podView: loadView(),
  hover: null as { uid: string; x: number; y: number } | null,
  hoverNet: null as string | null,
  feed: [] as FeedItem[],
  toasts: [] as Toast[],
})

export const useCluster = create<ClusterState>()(
  subscribeWithSelector((set, get) => ({
    ...initial(),

    applyMessages(msgs) {
      const st = get()
      let { rev, metrics, nodes, pods, workloads, namespaces, services, routes, volumes } = st
      let changed = false
      const drafts: FeedDraft[] = []

      for (const m of msgs) {
        switch (m.type) {
          case 'snapshot':
            nodes = new Map((m.nodes ?? []).map((n) => [n.name, n]))
            pods = new Map((m.pods ?? []).map((p) => [p.uid, p]))
            workloads = new Map((m.workloads ?? []).map((w) => [workloadKey(w), w]))
            namespaces = new Map((m.namespaces ?? []).map((n) => [n.name, n]))
            services = new Map((m.services ?? []).map((x) => [serviceKey(x), x]))
            routes = new Map((m.routes ?? []).map((x) => [routeKey(x), x]))
            volumes = new Map((m.volumes ?? []).map((x) => [volumeKey(x), x]))
            rev = m.rev
            changed = true
            break
          case 'metrics':
            metrics = m.metrics
            break
          case 'upsert':
          case 'delete': {
            const del = m.type === 'delete'
            if (m.kind === 'pod') {
              const prev = pods.get(m.obj.uid)
              const d = feedForPod(prev, del ? undefined : m.obj)
              if (d) drafts.push(d)
              if (del) pods.delete(m.obj.uid)
              else pods.set(m.obj.uid, m.obj)
            } else if (m.kind === 'node') {
              const d = feedForNode(nodes.get(m.obj.name), del ? undefined : m.obj)
              if (d) drafts.push(d)
              if (del) nodes.delete(m.obj.name)
              else nodes.set(m.obj.name, m.obj)
            } else if (m.kind === 'namespace') {
              if (del) namespaces.delete(m.obj.name)
              else namespaces.set(m.obj.name, m.obj)
            } else if (m.kind === 'service') {
              if (del) services.delete(serviceKey(m.obj))
              else services.set(serviceKey(m.obj), m.obj)
            } else if (m.kind === 'route') {
              if (del) routes.delete(routeKey(m.obj))
              else routes.set(routeKey(m.obj), m.obj)
            } else if (m.kind === 'volume') {
              if (del) volumes.delete(volumeKey(m.obj))
              else volumes.set(volumeKey(m.obj), m.obj)
            } else {
              const k = workloadKey(m.obj)
              if (del) workloads.delete(k)
              else workloads.set(k, m.obj)
            }
            rev = m.rev
            changed = true
          }
        }
      }

      const now = Date.now()
      const feed = drafts.length
        ? [...drafts.reverse().map((d) => ({ ...d, id: ++feedSeq, at: now })), ...st.feed].slice(0, FEED_SIZE)
        : st.feed

      set({
        rev, metrics, nodes, pods, workloads, namespaces, services, routes, volumes, feed,
        version: changed ? st.version + 1 : st.version,
        connection: 'live',
      })
    },

    setConnection: (connection) => set({ connection }),
    resetRev: () => set({ rev: 0 }),
    setMe: (me) => set({ me }),

    select(sel) {
      if (!sel) return set({ selection: null })
      const st = get()
      const name = {
        pod: () => st.pods.get(sel.key)?.name,
        node: () => st.nodes.get(sel.key)?.name,
        service: () => st.services.get(sel.key)?.name,
        route: () => st.routes.get(sel.key)?.name,
        volume: () => st.volumes.get(sel.key)?.name,
        gate: () => sel.key,
      }[sel.type]()
      // Le YAML choisi via la chaîne de propriétaires ne survit pas au changement de pod.
      set({ selection: { ...sel, name: name ?? sel.key }, yamlTarget: null })
    },

    toggleNsFilter: (ns) => set((s) => ({ nsFilter: s.nsFilter === ns ? null : ns })),
    setPodView: (patch) => {
      const podView = { ...get().podView, ...patch }
      saveView(podView)
      set({ podView })
    },
    setHover: (hover) => {
      const cur = get().hover
      if (cur?.uid === hover?.uid && cur?.x === hover?.x && cur?.y === hover?.y) return
      set({ hover })
    },
    setHoverNet: (hoverNet) => {
      if (get().hoverNet !== hoverNet) set({ hoverNet })
    },
    setInspectorTab: (inspectorTab) => set({ inspectorTab }),
    openYaml: (yamlTarget) => set({ inspectorTab: 'yaml', yamlTarget }),
    notify: (text, level = '') => {
      const id = ++feedSeq
      set((s) => ({
        toasts: [...s.toasts, { id, text, level }].slice(-4),
        feed: [{ id, at: Date.now(), text, level }, ...s.feed].slice(0, FEED_SIZE),
      }))
    },
    dismissToast: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
    setView: (view) => set({ view }),
    focusOn: (x, z) => set((s) => ({ focus: { x, z, seq: (s.focus?.seq ?? 0) + 1 } })),

    reset: () => set(initial()),
  })),
)
