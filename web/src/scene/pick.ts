import type * as THREE from 'three'

/** Correspondance instance → objet Kubernetes, renseignée par Pods et Buildings. */
export const pickables = {
  pods: { meshes: [] as THREE.InstancedMesh[], uids: [] as string[] },
  nodes: { meshes: [] as THREE.InstancedMesh[], names: [] as string[] },
}
