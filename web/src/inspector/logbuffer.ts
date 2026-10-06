// Tampon des logs de l'onglet Logs : 5 000 lignes au plus, détection du niveau.

export interface LogLine {
  ts?: string
  text: string
}

export type Level = 'error' | 'warn' | 'info' | 'debug' | ''

// Mots-clés en majuscules (sensibles à la casse : « errors_total » n'est pas un
// niveau), ou clé de niveau explicite en logfmt (level=…) ou JSON ("level":"…").
const levelKey = (names: string) => new RegExp(`\\blevel=(${names})\\b|"level"\\s*:\\s*"(${names})"`, 'i')
const LEVELS: [Exclude<Level, ''>, RegExp, RegExp][] = [
  ['error', /\b(ERROR|ERR|FATAL|CRITICAL|CRIT|PANIC)\b|\bpanic:/, levelKey('error|err|fatal|critical|crit|panic')],
  ['warn', /\bWARN(ING)?\b/, levelKey('warn|warning')],
  ['info', /\bINFO\b/, levelKey('info')],
  ['debug', /\b(DEBUG|TRACE)\b/, levelKey('debug|trace')],
]

/** Niveau détecté par regex (ERROR, WARN, INFO, DEBUG, formats logfmt et JSON). */
export function levelOf(text: string): Level {
  for (const [level, word, key] of LEVELS) if (word.test(text) || key.test(text)) return level
  return ''
}

export class LogBuffer {
  lines: LogLine[] = []
  /** Nombre total de lignes reçues depuis l'ouverture. */
  total = 0

  constructor(readonly max = 5000) {}

  push(lines: LogLine[]) {
    this.total += lines.length
    this.lines.push(...lines)
    if (this.lines.length > this.max) this.lines.splice(0, this.lines.length - this.max)
  }

  clear() {
    this.lines = []
    this.total = 0
  }

  filter(query: string): LogLine[] {
    if (!query) return this.lines
    const q = query.toLowerCase()
    return this.lines.filter((l) => l.text.toLowerCase().includes(q))
  }
}

export function toText(lines: LogLine[]): string {
  return lines.map((l) => (l.ts ? `${l.ts} ${l.text}` : l.text)).join('\n') + '\n'
}
