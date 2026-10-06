import { useEffect, useRef, type ReactNode } from 'react'

/** Fenêtre de confirmation modale (Échap ou clic hors de la fenêtre : annuler). */
export function Dialog({ title, children, onClose, actions }: {
  title: string
  children: ReactNode
  onClose: () => void
  actions: ReactNode
}) {
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    box.current?.querySelector<HTMLElement>('input, button.primary, button.danger')?.focus()
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') { e.stopPropagation(); onClose() } }
    document.addEventListener('keydown', onKey, true)
    return () => document.removeEventListener('keydown', onKey, true)
  }, [onClose])
  return (
    <div className="dialog-backdrop" onMouseDown={(e) => { if (e.target === e.currentTarget) onClose() }}>
      <div className="dialog" role="dialog" aria-modal="true" aria-label={title} ref={box}>
        <h2>{title}</h2>
        <div className="dialog-body">{children}</div>
        <div className="dialog-actions">{actions}</div>
      </div>
    </div>
  )
}
