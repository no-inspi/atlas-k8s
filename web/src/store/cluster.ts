import { create } from 'zustand'
import { subscribeWithSelector } from 'zustand/middleware'
import { workloadKey, type Me, type Message, type Metrics, type Namespace, type Node, type Pod, type Workload } from '../api/types'
import { feedForNode, feedForPod, type FeedDraft, type FeedItem } from './feed'

export type Selection = { type: 'pod' | 'node'; key: string; name: string } | null
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
  metrics: Metrics
  me: Me | null
  selection: Selection
  nsFilter: string | null
  feed: FeedItem[]

  applyMessages(msgs: Message[]): void
  setConnection(c: Connection): void
  setMe(me: Me): void
  select(sel: { type: 'pod' | 'node'; key: string } | null): void
  toggleNsFilter(ns: string): void
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
  metrics: { pods: {}, nodes: {} } as Metrics,
  me: null,
  selection: null as Selection,
  nsFilter: null,
  feed: [] as FeedItem[],
})

export const useCluster = create<ClusterState>()(
  subscribeWithSelector((set, get) => ({
    ...initial(),

    applyMessages(msgs) {
      const st = get()
      let { rev, metrics, nodes, pods, workloads, namespaces } = st
      let changed = false
      const drafts: FeedDraft[] = []

      for (const m of msgs) {
        switch (m.type) {
          case 'snapshot':
            nodes = new Map((m.nodes ?? []).map((n) => [n.name, n]))
            pods = new Map((m.pods ?? []).map((p) => [p.uid, p]))
            workloads = new Map((m.workloads ?? []).map((w) => [workloadKey(w), w]))
            namespaces = new Map((m.namespaces ?? []).map((n) => [n.name, n]))
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
        rev, metrics, nodes, pods, workloads, namespaces, feed,
        version: changed ? st.version + 1 : st.version,
        connection: 'live',
      })
    },

    setConnection: (connection) => set({ connection }),
    setMe: (me) => set({ me }),

    select(sel) {
      if (!sel) return set({ selection: null })
      const { pods, nodes } = get()
      const name = sel.type === 'pod' ? pods.get(sel.key)?.name : nodes.get(sel.key)?.name
      set({ selection: { ...sel, name: name ?? sel.key } })
    },

    toggleNsFilter: (ns) => set((s) => ({ nsFilter: s.nsFilter === ns ? null : ns })),

    reset: () => set(initial()),
  })),
)
