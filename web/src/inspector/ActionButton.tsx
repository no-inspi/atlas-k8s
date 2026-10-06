import type { ReactNode } from 'react'
import { deniedText, type Check, type Decision } from './access'

/**
 * Bouton d'action grisé tant que la revue d'accès n'a pas répondu, et avec
 * l'explication de la spec quand l'utilisateur n'a pas le droit.
 */
export function ActionButton({ check, decision, onClick, danger, busy, children }: {
  check?: Check
  decision?: Decision | null
  onClick: () => void
  danger?: boolean
  busy?: boolean
  children: ReactNode
}) {
  const denied = !!check && decision !== undefined && decision !== null && !decision.allowed
  const pending = !!check && !decision
  return (
    <button
      className={`btn ${danger ? 'danger' : ''}`}
      disabled={denied || pending || busy}
      title={denied ? deniedText(check!) : undefined}
      aria-busy={busy || undefined}
      onClick={onClick}
    >
      {children}
    </button>
  )
}
