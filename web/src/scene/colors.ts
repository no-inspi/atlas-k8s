// Couleur des robots par namespace : 12 teintes lisibles en clair comme en
// sombre, sans rouge (réservé aux pods en erreur). Les 5 premières viennent
// du prototype.
export const NS_PALETTE = [
  '#3D6FB6', '#2E9C8F', '#8A62C4', '#D98E2B', '#7D8A94', '#5E9C3F',
  '#4FA3D1', '#8C6E54', '#A8A23A', '#D46FB0', '#5763A8', '#B5654A',
] as const

function fnv1a(s: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

/**
 * Teinte de chaque namespace : hash stable du nom, puis sondage linéaire dans
 * l'ordre alphabétique pour que deux namespaces visibles ne partagent pas une
 * teinte tant qu'il en reste. L'annotation atlas.io/color d'un namespace
 * (overrides) l'emporte.
 */
export function nsColors(namespaces: Iterable<string>, overrides: ReadonlyMap<string, string> = new Map()): Map<string, string> {
  const out = new Map<string, string>()
  const used = new Set<number>()
  for (const ns of [...new Set(namespaces)].sort()) {
    const forced = overrides.get(ns)
    if (forced) {
      out.set(ns, forced)
      continue
    }
    let i = fnv1a(ns) % NS_PALETTE.length
    if (used.size < NS_PALETTE.length) while (used.has(i)) i = (i + 1) % NS_PALETTE.length
    used.add(i)
    out.set(ns, NS_PALETTE[i])
  }
  return out
}

/** Couleurs des namespaces présents (pods, Services, volumes), annotations comprises. */
export function clusterColors(st: {
  pods: ReadonlyMap<string, { namespace: string }>
  namespaces: ReadonlyMap<string, { color?: string }>
  services?: ReadonlyMap<string, { namespace: string }>
  volumes?: ReadonlyMap<string, { namespace: string }>
}) {
  const overrides = new Map<string, string>()
  for (const [name, ns] of st.namespaces) if (ns.color) overrides.set(name, ns.color)
  const names = [...st.pods.values(), ...(st.services?.values() ?? []), ...(st.volumes?.values() ?? [])].map((o) => o.namespace)
  return nsColors(names, overrides)
}
