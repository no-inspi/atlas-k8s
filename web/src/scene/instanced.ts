import * as THREE from 'three'

/**
 * Une pièce instanciée (jambe, tête, toit…) : une seule InstancedMesh pour
 * toutes les occurrences, donc un seul draw call. Les instances non utilisées
 * sont réduites à une échelle nulle.
 */
export class Part {
  readonly mesh: THREE.InstancedMesh
  private opacity?: THREE.InstancedBufferAttribute
  private used = 0

  constructor(
    geometry: THREE.BufferGeometry,
    material: THREE.Material,
    readonly capacity: number,
    opts: { castShadow?: boolean; receiveShadow?: boolean; opacity?: boolean; name?: string } = {},
  ) {
    const geo = geometry.clone()
    if (opts.opacity) {
      this.opacity = new THREE.InstancedBufferAttribute(new Float32Array(capacity).fill(1), 1)
      this.opacity.setUsage(THREE.DynamicDrawUsage)
      geo.setAttribute('aOpacity', this.opacity)
    }
    this.mesh = new THREE.InstancedMesh(geo, material, capacity)
    this.mesh.instanceMatrix.setUsage(THREE.DynamicDrawUsage)
    this.mesh.castShadow = !!opts.castShadow
    this.mesh.receiveShadow = !!opts.receiveShadow
    this.mesh.frustumCulled = false // les instances bougent : la sphère englobante serait périmée
    this.mesh.name = opts.name ?? ''
    this.mesh.count = 0
  }

  setMatrix(i: number, m: THREE.Matrix4) {
    this.mesh.setMatrixAt(i, m)
  }

  setColor(i: number, c: THREE.Color) {
    this.mesh.setColorAt(i, c)
  }

  setOpacity(i: number, a: number) {
    if (this.opacity) this.opacity.setX(i, a)
  }

  hide(i: number) {
    this.mesh.setMatrixAt(i, ZERO)
  }

  /** À appeler après avoir écrit les `count` premières instances. */
  commit(count: number) {
    this.used = Math.min(count, this.capacity)
    this.mesh.count = this.used
    this.mesh.instanceMatrix.needsUpdate = true
    if (this.mesh.instanceColor) this.mesh.instanceColor.needsUpdate = true
    if (this.opacity) this.opacity.needsUpdate = true
    // Le raycast d'une InstancedMesh s'appuie sur sa sphère englobante.
    this.mesh.boundingSphere = null
  }

  dispose() {
    this.mesh.geometry.dispose()
    this.mesh.dispose()
  }
}

const ZERO = new THREE.Matrix4().makeScale(0, 0, 0)

/**
 * Rend l'opacité d'un matériau pilotable par instance via l'attribut
 * `aOpacity` (filtre de namespace à 10 %, fumée).
 */
function withInstanceOpacity<T extends THREE.Material>(m: T, key: string): T {
  m.transparent = true
  m.onBeforeCompile = (shader) => {
    shader.vertexShader = shader.vertexShader
      .replace('#include <common>', '#include <common>\nattribute float aOpacity;\nvarying float vOpacity;')
      .replace('#include <begin_vertex>', '#include <begin_vertex>\nvOpacity = aOpacity;')
    shader.fragmentShader = shader.fragmentShader
      .replace('#include <common>', '#include <common>\nvarying float vOpacity;')
      .replace('#include <color_fragment>', '#include <color_fragment>\ndiffuseColor.a *= vOpacity;')
  }
  m.customProgramCacheKey = () => key
  return m
}

export const opacityMaterial = (params: THREE.MeshStandardMaterialParameters = {}) =>
  withInstanceOpacity(new THREE.MeshStandardMaterial(params), 'instance-opacity-standard')

export const opacityBasicMaterial = (params: THREE.MeshBasicMaterialParameters = {}) =>
  withInstanceOpacity(new THREE.MeshBasicMaterial(params), 'instance-opacity-basic')

/** Capacité arrondie au palier supérieur pour éviter de recréer les meshes à chaque pod. */
export const roundCapacity = (n: number, step = 256) => Math.max(step, Math.ceil(n / step) * step)
