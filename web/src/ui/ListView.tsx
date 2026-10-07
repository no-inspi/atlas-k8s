import { useEffect, useMemo, useRef, useState } from 'react'
import { postureFor } from '../scene/posture'
import { useCluster } from '../store/cluster'
import { buildTree, flatten, keyAction, type TreeNode } from './tree'

const BADGE: Record<string, string> = { ok: 's-ok', warn: 's-warn', err: 's-err', mute: 's-mute', done: 's-mute' }

// Couleurs alignées sur gateSignal : non programmé = rouge ; refusée, cassée, listener non prêt = orange.
const NET_STATUS: Record<string, string> = {
  down: 's-err', Lost: 's-err', Failed: 's-err', 'Service introuvable': 's-err', 'non programmé': 's-err',
  degraded: 's-warn', Pending: 's-warn', 'route cassée': 's-warn', 'route refusée': 's-warn', 'listener non prêt': 's-warn',
  Released: 's-mute', Available: 's-mute',
}

function statusClass(n: TreeNode): string {
  if (!n.status) return ''
  if (n.select?.type === 'node') return 's-warn'
  if (n.select && n.select.type !== 'pod') return NET_STATUS[n.status] ?? 's-warn'
  return BADGE[postureFor({ displayStatus: n.status, ready: n.status === 'Running' }).antenna]
}

/**
 * Vue Liste : tout le cluster dans un arbre accessible au clavier (rôle
 * « tree »), qui ouvre le même inspecteur que la vue 3D.
 */
export function ListView() {
  const version = useCluster((s) => s.version)
  const select = useCluster((s) => s.select)
  const selection = useCluster((s) => s.selection)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(['group:namespaces', 'group:nodes']))
  const [focused, setFocused] = useState('group:namespaces')
  const refs = useRef(new Map<string, HTMLDivElement>())
  const tree = useMemo(() => buildTree(useCluster.getState()), [version]) // eslint-disable-line react-hooks/exhaustive-deps
  const items = useMemo(() => flatten(tree, expanded), [tree, expanded])

  useEffect(() => { refs.current.get(focused)?.focus({ preventScroll: false }) }, [focused])

  const apply = (action: ReturnType<typeof keyAction>) => {
    if (!action) return
    const toggle = (id: string, on: boolean) => setExpanded((e) => { const n = new Set(e); if (on) n.add(id); else n.delete(id); return n })
    switch (action.type) {
      case 'focus': setFocused(action.id); break
      case 'expand': toggle(action.id, true); break
      case 'collapse': toggle(action.id, false); break
      case 'activate': {
        const it = items.find((i) => i.node.id === action.id)
        if (it?.node.select) select(it.node.select)
      }
    }
  }

  return (
    <nav className="list-view" aria-label="Vue Liste du cluster">
      <div role="tree" aria-label="Cluster" className="tree">
        {items.map(({ node: n, level }) => {
          const hasChildren = !!n.children?.length
          const selected = !!n.select && selection?.type === n.select.type && selection.key === n.select.key
          return (
            <div
              key={n.id}
              ref={(el) => { if (el) refs.current.set(n.id, el); else refs.current.delete(n.id) }}
              role="treeitem"
              aria-level={level}
              aria-expanded={hasChildren ? expanded.has(n.id) : undefined}
              aria-selected={selected}
              tabIndex={n.id === focused ? 0 : -1}
              className="tree-item"
              style={{ paddingLeft: 8 + (level - 1) * 18 }}
              onKeyDown={(e) => {
                const a = keyAction(e.key, items, n.id, expanded)
                if (a) { e.preventDefault(); apply(a) }
              }}
              onClick={() => {
                setFocused(n.id)
                apply(keyAction('Enter', items, n.id, expanded))
              }}
            >
              <span className="twisty" aria-hidden="true">{hasChildren ? (expanded.has(n.id) ? '▾' : '▸') : ''}</span>
              <span className="nm">{n.label}</span>
              {n.status && <span className={statusClass(n)}>{n.status}</span>}
              {n.detail && <span className="later">{n.detail}</span>}
            </div>
          )
        })}
      </div>
    </nav>
  )
}
