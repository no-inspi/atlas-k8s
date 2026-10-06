import { useEffect } from 'react'
import { useCluster, type Toast } from '../store/cluster'

function ToastItem({ t }: { t: Toast }) {
  const dismiss = useCluster((s) => s.dismissToast)
  useEffect(() => {
    const id = setTimeout(() => dismiss(t.id), t.level === 'e' ? 9000 : 5000)
    return () => clearTimeout(id)
  }, [t.id, t.level, dismiss])
  return (
    <div className={`toast ${t.level}`} role={t.level === 'e' ? 'alert' : 'status'}>
      <span>{t.text}</span>
      <button className="close small" onClick={() => dismiss(t.id)} aria-label="Fermer">✕</button>
    </div>
  )
}

export function Toasts() {
  const toasts = useCluster((s) => s.toasts)
  return <div className="toasts" aria-live="polite">{toasts.map((t) => <ToastItem key={t.id} t={t} />)}</div>
}
