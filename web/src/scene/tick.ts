// Animations lentes (paquets, gouttes, voyants) : 15 images/s au lieu de 60,
// pour que la ville au repos ne garde pas le GPU occupé.
let pending = false

export function tick(invalidate: () => void) {
  if (pending) return
  pending = true
  setTimeout(() => {
    pending = false
    invalidate()
  }, 66)
}
