import { gateway, route } from '../store/fixtures'
import { gatesOf } from '../store/net'
import { gateBadge, gatewayBadge, pvNote, shareLabel } from './NetOverview'

const refused = route({
  namespace: 'staging', name: 'preview', source: 'HTTPRoute', gates: ['infra/public'],
  rules: [{ host: '', backend: { namespace: 'staging', service: 'web', kind: 'Service', state: 'refused' } }],
})

const gateFor = (gw: ReturnType<typeof gateway>, routes = [route({ gates: ['infra/public'] })]) =>
  gatesOf(routes, new Map([['infra/public', gw]])).find((g) => g.name === 'infra/public')!

describe('gatewayBadge', () => {
  it('compte les listeners prêts, en vert quand tout va bien', () => {
    const gw = gateway({ listeners: [
      { name: 'http', protocol: 'HTTP', port: 80, attachedRoutes: 1, ready: 'true' },
      { name: 'https', protocol: 'HTTPS', port: 443, attachedRoutes: 1, ready: 'true' },
    ] })
    expect(gatewayBadge(gw, gateFor(gw))).toEqual(['2/2 listeners prêts', 's-ok'])
  })

  it('passe en orange quand une route rattachée est refusée', () => {
    const gw = gateway()
    expect(gatewayBadge(gw, gateFor(gw, [refused]))).toEqual(['1/1 listeners prêts', 's-warn'])
  })

  it('signale un Gateway non programmé en rouge', () => {
    const gw = gateway({ programmed: 'false', reason: 'AddressNotAssigned' })
    expect(gatewayBadge(gw, gateFor(gw))).toEqual(['Non programmé', 's-err'])
  })

  it('montre un Gateway sans statut en gris', () => {
    const gw = gateway({ programmed: 'unknown' })
    expect(gatewayBadge(gw, gateFor(gw))).toEqual(['Sans statut', 's-mute'])
  })

  it('compte zéro listener sans planter', () => {
    const gw = gateway({ listeners: [] })
    expect(gatewayBadge(gw, gateFor(gw, []))).toEqual(['0/0 listeners prêts', 's-ok'])
  })
})

describe('shareLabel', () => {
  it('affiche la part en pour cent, un poids nul compris', () => {
    expect(shareLabel({ weight: 900 })).toBe('90 %')
    expect(shareLabel({ weight: 0 })).toBe('0 %')
  })
  it('affiche un miroir avec son pourcentage, 100 % par défaut', () => {
    expect(shareLabel({ mirror: true, percent: 10 })).toBe('miroir 10 %')
    expect(shareLabel({ mirror: true })).toBe('miroir 0 %')
  })
  it('affiche un tiret pour un backend unique', () => {
    expect(shareLabel({})).toBe('—')
  })
})

describe('gateBadge', () => {
  const broken = route({
    name: 'api', rules: [{ host: '', backend: { namespace: 'production', service: 'gone', kind: 'Service', state: 'missing' } }],
  })
  it('porte déduite saine : verte', () => {
    expect(gateBadge(gatesOf([route()])[0])).toEqual(['1 route(s)', 's-ok'])
  })
  it('porte déduite avec route cassée : orange', () => {
    expect(gateBadge(gatesOf([broken])[0])).toEqual(['1 route(s) cassée(s)', 's-warn'])
  })
  it('Gateway non visible avec route refusée : orange', () => {
    expect(gateBadge(gatesOf([refused])[0])).toEqual(['1 route(s) refusée(s)', 's-warn'])
  })
  it('Gateway non visible sans problème : gris', () => {
    expect(gateBadge(gatesOf([route({ gates: ['infra/public'] })])[0])).toEqual(['1 route(s)', 's-mute'])
  })
})

describe('pvNote', () => {
  it('distingue Retain et Delete pour un PV libéré', () => {
    expect(pvNote({ phase: 'Released', reclaimPolicy: 'Retain' })).toMatch(/à la main/)
    expect(pvNote({ phase: 'Released', reclaimPolicy: 'Delete' })).toMatch(/en attente ou a échoué/)
  })
})
