import type * as THREE from 'three'

/** Correspondance instance → objet Kubernetes, renseignée par Pods, Buildings et Network. */
export const pickables = {
  pods: { meshes: [] as THREE.InstancedMesh[], uids: [] as string[] },
  nodes: { meshes: [] as THREE.InstancedMesh[], names: [] as string[] },
  /** Relais, portes et citernes : une liste de clés « type:clé » par mesh. */
  net: [] as { mesh: THREE.InstancedMesh; keys: string[] }[],
}
