import { useEffect, useState, type ReactNode } from 'react'
import { getEvents, type KubeEvent } from '../api/inspect'
import { routeKey, type Gateway, type PersistentVolume, type Route, type Service, type Tri, type Volume } from '../api/types'
import { clusterColors } from '../scene/colors'
import { gateSignal } from '../scene/health'
import { postureFor } from '../scene/posture'
import { useCluster } from '../store/cluster'
import { gatesOfRoute, isGatewayGate, readyCount, routeBroken, routeRefused, routesTo, type Gate } from '../store/net'
import { fmtMem, fmtShare } from '../ui/format'
import { BADGE, goPod } from './common'

const goService = (ns: string, name: string) => () => useCluster.getState().select({ type: 'service', key: `${ns}/${name}` })
const goRoute = (r: Route) => () => useCluster.getState().select({ type: 'route', key: routeKey(r) })

/** Porte d'une route : l'inspecteur du Gateway s'il est visible, sinon la porte déduite. */
const goGate = (name: string) => () => {
  const st = useCluster.getState()
  st.select(st.gateways.has(name) ? { type: 'gateway', key: name } : { type: 'gate', key: name })
}

const STATE: Record<string, [string, string]> = {
  ok: ['', ''], missing: ['s-err', 'Service introuvable'], refused: ['s-err', 'Refusée'], indirect: ['s-mute', 'TraefikService'],
}

const TRI: Record<Tri, [string, string]> = { true: ['s-ok', 'oui'], false: ['s-err', 'non'], unknown: ['s-mute', 'inconnu'] }

function TriText({ v }: { v: Tri }) {
  const [cls, label] = TRI[v] ?? TRI.unknown
  return <span className={cls}>{label}</span>
}

/** Événements qui expliquent presque toujours un PVC bloqué en Pending. */
const BLOCKING = new Set(['ProvisioningFailed', 'WaitForFirstConsumer', 'ExternalProvisioning', 'FailedBinding'])

function PodItem({ uid, extra }: { uid: string; extra?: ReactNode }) {
  const st = useCluster.getState()
  const p = st.pods.get(uid)
  return (
    <li>
      <button onClick={goPod(uid)} disabled={!p}>
        <i aria-hidden="true" style={{ background: p ? clusterColors(st).get(p.namespace) : undefined }} />
        <span className="nm">{p?.name ?? 'pod non visible'}</span>
        {extra}
      </button>
    </li>
  )
}

/** Routes d'une porte ou d'un Gateway, avec leur état. */
function RouteList({ routes, testid }: { routes: Route[]; testid: string }) {
  if (!routes.length) return <p className="note">Aucune route visible ne s'attache à cette porte.</p>
  return (
    <ul className="podlist" data-testid={testid}>
      {routes.map((r) => (
        <li key={routeKey(r)}>
          <button onClick={goRoute(r)}>
            <span className="nm">{r.name}</span>
            <span className="later">{r.source} · {r.namespace}</span>
            {routeRefused(r) ? <span className="s-err">Refusée</span> : routeBroken(r) && <span className="s-err">Service introuvable</span>}
          </button>
        </li>
      ))}
    </ul>
  )
}

export function ServiceOverview({ s }: { s: Service }) {
  const via = routesTo(useCluster.getState().routes.values(), s)
  return (
    <div className="p-body">
      <dl className="kv">
        <dt>Type</dt><dd>{s.type}{s.headless ? ' (headless)' : ''}</dd>
        {s.clusterIP && <><dt>Cluster IP</dt><dd>{s.clusterIP}</dd></>}
        {s.loadBalancer?.length ? <><dt>LoadBalancer</dt><dd>{s.loadBalancer.join(', ')}</dd></> : null}
        {s.externalName && <><dt>Nom externe</dt><dd>{s.externalName}</dd></>}
      </dl>
      {s.ports.length > 0 && (
        <>
          <h3>Ports</h3>
          <div className="tags">
            {s.ports.map((p) => (
              <span key={`${p.port}/${p.protocol}`} className="tag">
                {p.name ? `${p.name} ` : ''}{p.port}{p.targetPort ? ` → ${p.targetPort}` : ''}/{p.protocol}{p.nodePort ? ` · node ${p.nodePort}` : ''}
              </span>
            ))}
          </div>
        </>
      )}
      {s.type !== 'ExternalName' && (
        <>
          <h3>Endpoints ({readyCount(s)}/{s.endpoints.length} ready)</h3>
          {s.endpoints.length ? (
            <ul className="podlist" data-testid="endpoints">
              {s.endpoints.map((e) => (
                <PodItem key={e.podUID} uid={e.podUID} extra={<span className={e.ready ? 's-ok' : 's-warn'}>{e.ready ? 'ready' : 'non ready'}</span>} />
              ))}
            </ul>
          ) : <p className="note s-err">Aucun pod derrière ce Service : vérifiez son selector.</p>}
        </>
      )}
      <h3>Routes ({via.length})</h3>
      {via.length ? (
        <ul className="podlist">
          {via.map((r) => (
            <li key={routeKey(r)}><button onClick={goRoute(r)}><span className="nm">{r.name}</span><span className="later">{r.source} · porte {gatesOfRoute(r).join(', ')}</span></button></li>
          ))}
        </ul>
      ) : <p className="note">Aucune route (Ingress, IngressRoute, HTTPRoute…) ne vise ce Service.</p>}
    </div>
  )
}

export function RouteOverview({ r }: { r: Route }) {
  const services = useCluster.getState().services
  const gates = gatesOfRoute(r)
  const shares = r.rules.some((x) => x.backend.weight !== undefined || x.backend.mirror)
  const via = r.rules.some((x) => x.backend.via)
  return (
    <div className="p-body">
      <dl className="kv">
        <dt>{gates.length > 1 ? 'Portes' : 'Porte'}</dt>
        <dd>
          {gates.map((g, i) => (
            <span key={`${g}#${i}`}>{i > 0 && ', '}<button className="link" onClick={goGate(g)}>{g}</button></span>
          ))}
        </dd>
        <dt>Source</dt><dd>{r.source} ({r.group})</dd>
        {r.addresses?.length ? <><dt>Adresses</dt><dd>{r.addresses.join(', ')}</dd></> : null}
      </dl>
      {r.parents?.length ? (
        <>
          <h3>Gateways ({r.parents.length})</h3>
          <table className="evt" data-testid="parents">
            <thead>
              <tr><th scope="col">Gateway</th><th scope="col">Acceptée</th><th scope="col">Références résolues</th><th scope="col">Raison</th></tr>
            </thead>
            <tbody>
              {r.parents.map((p, i) => {
                const bad = p.accepted === 'false' || p.resolvedRefs === 'false'
                // Index dans la clé : un même Gateway peut revenir (un parent par listener ou contrôleur).
                return (
                  <tr key={`${p.gateway}#${i}`} className={bad ? 'warning' : ''}>
                    <td><button className="link" onClick={goGate(p.gateway)}>{p.gateway}</button></td>
                    <td><TriText v={p.accepted} /></td>
                    <td><TriText v={p.resolvedRefs} /></td>
                    <td>{bad ? p.reason ?? '' : ''}</td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </>
      ) : null}
      <h3>Règles ({r.rules.length})</h3>
      {r.rules.length ? (
        <table className="evt" data-testid="rules">
          <thead>
            <tr>
              <th scope="col">Hôte · chemin</th><th scope="col">Backend</th>
              {shares && <th scope="col">Part</th>}
              {via && <th scope="col">Via</th>}
            </tr>
          </thead>
          <tbody>
            {r.rules.map((rule, i) => {
              const b = rule.backend
              const [cls, label] = STATE[b.state] ?? STATE.ok
              return (
                <tr key={i} className={b.state === 'missing' || b.state === 'refused' ? 'warning' : ''}>
                  <td>
                    {rule.host || '*'}{rule.path ?? ''}
                    {rule.match && <><br /><code>{rule.match}</code></>}
                  </td>
                  <td>
                    {services.has(`${b.namespace}/${b.service}`)
                      ? <button className="link" onClick={goService(b.namespace, b.service)}>{b.service}</button>
                      : b.service}
                    {b.port ? `:${b.port}` : ''}{b.namespace !== r.namespace ? ` (${b.namespace})` : ''}
                    {label && <> <span className={cls}>{label}</span></>}
                  </td>
                  {shares && <td>{b.mirror ? `miroir ${b.percent ?? 100} %` : b.weight !== undefined ? fmtShare(b.weight) : '—'}</td>}
                  {via && <td>{b.via ?? '—'}</td>}
                </tr>
              )
            })}
          </tbody>
        </table>
      ) : <p className="note">Aucune règle : cette route n'envoie de trafic vers aucun backend.</p>}
    </div>
  )
}

export function GateOverview({ g }: { g: Gate }) {
  return (
    <div className="p-body">
      <p className="note">
        {isGatewayGate(g.name)
          ? `Gateway « ${g.name} » : vous ne pouvez pas le lire, ou il n'existe plus ; son état est inconnu. Les routes ci-dessous s'y rattachent.`
          : `Contrôleur d'entrée « ${g.name} » : chaque route ci-dessous entre dans la ville par cette porte.`}
      </p>
      <RouteList routes={g.routes} testid="gate-routes" />
    </div>
  )
}

/** Badge d'un Gateway : non programmé, sans statut, ou listeners prêts (orange si une route ou un listener pèche). */
export function gatewayBadge(gw: Gateway, g: Gate): [string, string] {
  if (gw.programmed === 'false') return ['Non programmé', 's-err']
  if (gw.programmed === 'unknown') return ['Sans statut', BADGE[gateSignal(g)]]
  const ready = gw.listeners.filter((l) => l.ready === 'true').length
  return [`${ready}/${gw.listeners.length} listeners prêts`, BADGE[gateSignal(g)]]
}

export function GatewayOverview({ gw, g }: { gw: Gateway; g: Gate }) {
  return (
    <div className="p-body">
      {gw.programmed === 'false' && (
        <p className="note s-err" data-testid="gateway-why"><b>{gw.reason || 'Non programmé'}</b>{gw.message ? ` : ${gw.message}` : ''}</p>
      )}
      {gw.programmed === 'unknown' && <p className="note">Aucun contrôleur n'a encore écrit l'état de ce Gateway.</p>}
      <dl className="kv">
        <dt>Classe</dt><dd>{gw.class || '—'}</dd>
        <dt>Accepté</dt><dd><TriText v={gw.accepted} /></dd>
        <dt>Programmé</dt><dd><TriText v={gw.programmed} />{gw.reason && gw.programmed !== 'false' ? ` (${gw.reason})` : ''}</dd>
        {gw.addresses?.length ? <><dt>Adresses</dt><dd>{gw.addresses.join(', ')}</dd></> : null}
      </dl>
      <h3>Listeners ({gw.listeners.length})</h3>
      {gw.listeners.length ? (
        <table className="evt" data-testid="listeners">
          <thead>
            <tr><th scope="col">Listener</th><th scope="col">Protocole · port</th><th scope="col">Hôte</th><th scope="col">Routes</th><th scope="col">Prêt</th></tr>
          </thead>
          <tbody>
            {gw.listeners.map((l, i) => (
              <tr key={`${l.name}#${i}`} className={l.ready === 'false' ? 'warning' : ''}>
                <td>{l.name}</td>
                <td>{l.protocol} · {l.port}</td>
                <td>{l.hostname || '*'}</td>
                <td>{l.attachedRoutes}</td>
                <td><TriText v={l.ready} /></td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : <p className="note">Aucun listener déclaré : ce Gateway n'accepte aucun trafic.</p>}
      <h3>Routes ({g.routes.length})</h3>
      <RouteList routes={g.routes} testid="gateway-routes" />
    </div>
  )
}

const PV_NOTE: Record<PersistentVolume['phase'], string> = {
  Available: 'Disponible : aucun PVC ne le réclame.',
  Released: 'Libéré : son PVC a été supprimé. Avec la reclaim policy Retain, les données restent sur le disque jusqu’à la suppression du volume.',
  Failed: 'En échec : la récupération automatique du volume a échoué (voir les événements).',
  Bound: 'Lié à un PVC qui n’existe plus.',
}

export function PvOverview({ p }: { p: PersistentVolume }) {
  return (
    <div className="p-body">
      <p className={`note ${p.phase === 'Failed' ? 's-err' : ''}`} data-testid="pv-why">{PV_NOTE[p.phase] ?? p.phase}</p>
      <dl className="kv">
        <dt>Classe</dt><dd>{p.storageClass || '(aucune)'}</dd>
        <dt>Capacité</dt><dd>{p.capacity ? fmtMem(p.capacity) : '—'}</dd>
        <dt>Accès</dt><dd>{p.accessModes.join(', ') || '—'}</dd>
        <dt>Reclaim policy</dt><dd>{p.reclaimPolicy || '—'}</dd>
        <dt>Ancien PVC</dt><dd>{p.claimRef || '(aucun)'}</dd>
      </dl>
    </div>
  )
}

export function VolumeOverview({ v }: { v: Volume }) {
  const pods = useCluster.getState().pods
  const [why, setWhy] = useState<KubeEvent | null>(null)
  useEffect(() => {
    setWhy(null)
    if (v.phase !== 'Pending') return
    let live = true
    getEvents(v.namespace, v.name, 'persistentvolumeclaims')
      .then((evs) => live && setWhy(evs.find((e) => BLOCKING.has(e.reason)) ?? null))
      .catch(() => {})
    return () => { live = false }
  }, [v.namespace, v.name, v.phase])

  return (
    <div className="p-body">
      {why && (
        <p className={`note ${why.type === 'Warning' ? 's-err' : 's-warn'}`} data-testid="pvc-why"><b>{why.reason}</b> : {why.message}</p>
      )}
      <dl className="kv">
        <dt>Classe</dt><dd>{v.storageClass || '(aucune)'}</dd>
        <dt>Demandé</dt><dd>{fmtMem(v.requested)}</dd>
        <dt>Capacité</dt><dd>{v.capacity ? fmtMem(v.capacity) : '—'}</dd>
        <dt>Accès</dt><dd>{v.accessModes.join(', ') || '—'}</dd>
        {v.volumeName && <><dt>Volume</dt><dd>{v.volumeName}</dd></>}
      </dl>
      <h3>Pods ({v.pods.length})</h3>
      {v.pods.length ? (
        <ul className="podlist">
          {v.pods.map((uid) => {
            const p = pods.get(uid)
            return <PodItem key={uid} uid={uid} extra={p && <span className={BADGE[postureFor(p).antenna]}>{p.displayStatus}</span>} />
          })}
        </ul>
      ) : <p className="note">Aucun pod ne monte ce PVC.</p>}
    </div>
  )
}
