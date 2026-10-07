import { useEffect, useRef, useState } from 'react'
import { world } from '../scene/world'
import { useCluster } from '../store/cluster'
import { search, type SearchResult } from './searchRank'

/** Position à viser dans la ville pour un résultat. */
function positionOf(r: SearchResult): { x: number; z: number } | null {
  if (r.type === 'workload') return r.podUid ? world.positionOf('pod', r.podUid) : null
  return world.positionOf(r.type, r.key)
}

/** Recherche (/) : centre la caméra sur l'objet et l'ouvre dans l'inspecteur. */
export function Search() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const input = useRef<HTMLInputElement>(null)
  useCluster((s) => s.version)
  const results = open ? search(query, useCluster.getState()) : []

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      if (e.key === '/' && !t.closest('input, textarea, select, .monaco-host, .term-host, [contenteditable]')) {
        e.preventDefault()
        setOpen(true)
      }
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [])
  useEffect(() => { if (open) input.current?.focus() }, [open])
  useEffect(() => setActive(0), [query])

  const choose = (r: SearchResult) => {
    const st = useCluster.getState()
    if (r.type === 'workload') st.select({ type: 'pod', key: r.podUid! })
    else st.select({ type: r.type, key: r.key })
    const at = positionOf(r)
    if (at) st.focusOn(at.x, at.z)
    setOpen(false)
    setQuery('')
  }

  if (!open) {
    return (
      <button className="search-btn" onClick={() => setOpen(true)} aria-label="Rechercher (raccourci /)">
        Rechercher <kbd>/</kbd>
      </button>
    )
  }
  return (
    <div className="search" role="search">
      <input
        ref={input}
        type="search"
        role="combobox"
        aria-expanded={results.length > 0}
        aria-controls="search-results"
        aria-activedescendant={results[active] ? `search-r${active}` : undefined}
        aria-label="Rechercher un pod, un node, un workload, un Service, une route ou un PVC"
        placeholder="Pod, node, Service, route, PVC…"
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onBlur={() => setTimeout(() => setOpen(false), 150)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') { setOpen(false); setQuery('') }
          if (e.key === 'ArrowDown') { e.preventDefault(); setActive((a) => Math.min(a + 1, results.length - 1)) }
          if (e.key === 'ArrowUp') { e.preventDefault(); setActive((a) => Math.max(a - 1, 0)) }
          if (e.key === 'Enter' && results[active]) choose(results[active])
        }}
      />
      {query.trim() && (
        <ul id="search-results" role="listbox" className="search-results">
          {results.map((r, i) => (
            <li key={r.type + r.key} id={`search-r${i}`} role="option" aria-selected={i === active}
              onMouseDown={(e) => { e.preventDefault(); choose(r) }} onMouseEnter={() => setActive(i)}>
              <span className="nm">{r.label}</span>
              <span className="later">{r.detail}</span>
            </li>
          ))}
          {!results.length && <li className="later">Aucun résultat.</li>}
        </ul>
      )}
    </div>
  )
}
