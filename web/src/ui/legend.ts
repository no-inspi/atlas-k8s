// Chips visibles de la légende : les namespaces les plus peuplés, plus celui
// qui est filtré ; les autres passent derrière un bouton « +N ».

export const LEGEND_LIMIT = 10

export function legendItems(counts: ReadonlyMap<string, number>, filter: string | null, limit = LEGEND_LIMIT) {
  const byCount = [...counts.keys()].sort((a, b) => (counts.get(b)! - counts.get(a)!) || a.localeCompare(b))
  const visible = byCount.slice(0, limit)
  if (filter && counts.has(filter) && !visible.includes(filter)) visible.push(filter)
  const shown = new Set(visible)
  return { visible: visible.sort(), hidden: byCount.filter((n) => !shown.has(n)).sort() }
}
