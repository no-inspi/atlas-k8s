const MI = 2 ** 20
const GI = 2 ** 30

/** Millicores → « 250m », « 1 », « 3.92 », comme kubectl. */
export function fmtCpu(m: number): string {
  if (m < 1000) return `${Math.round(m)}m`
  return String(Number((m / 1000).toFixed(2)))
}

/** Octets → « 256Mi » ou « 1.5Gi ». */
export function fmtMem(bytes: number): string {
  if (bytes >= GI) return `${Number((bytes / GI).toFixed(1))}Gi`
  return `${Math.round(bytes / MI)}Mi`
}

/** Âge compact d'un objet : 30s, 45m, 2h10, 3j2h. */
export function age(iso: string, now = Date.now()): string {
  const s = Math.max(1, Math.floor((now - Date.parse(iso)) / 1000))
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}`
  return `${Math.floor(s / 86400)}j${Math.floor((s % 86400) / 3600)}h`
}

/** Pourcentage borné entre 0 et 100 (0 si le total est nul). */
export function pct(value: number, total: number): number {
  if (!total) return 0
  return Math.min(100, Math.max(0, (value / total) * 100))
}

/** Part du trafic, en pour mille → « 90 % », « 33.3 % ». */
export function fmtShare(permille: number): string {
  return `${Math.round(permille) / 10} %`
}

/** Miroir : « miroir 10 % » ; sans pourcentage publié, 0 % (le serveur omet percent = 0). */
export function mirrorLabel(b: { percent?: number }): string {
  return `miroir ${b.percent ?? 0} %`
}
