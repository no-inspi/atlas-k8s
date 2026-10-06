import type * as THREE from 'three'

/** Correspondance instance → objet Kubernetes, renseignée par Robots et Buildings. */
export const pickables = {
  robots: { meshes: [] as THREE.InstancedMesh[], uids: [] as string[] },
  nodes: { meshes: [] as THREE.InstancedMesh[], names: [] as string[] },
}
