import { gateway, route } from '../store/fixtures'
import { gatesOf } from '../store/net'
import { gatewayBadge } from './NetOverview'

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
