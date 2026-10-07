import { useEffect, useState, type ReactNode } from 'react'
import { getEvents, type KubeEvent } from '../api/inspect'
import { routeKey, type Route, type Service, type Volume } from '../api/types'
import { clusterColors } from '../scene/colors'
import { postureFor } from '../scene/posture'
import { useCluster } from '../store/cluster'
import { readyCount, routeBroken, routesTo, type Gate } from '../store/net'
import { fmtMem } from '../ui/format'
import { BADGE, goPod } from './common'

const goService = (ns: string, name: string) => () => useCluster.getState().select({ type: 'service', key: `${ns}/${name}` })
const goRoute = (r: Route) => () => useCluster.getState().select({ type: 'route', key: routeKey(r) })
const goGate = (name: string) => () => useCluster.getState().select({ type: 'gate', key: name })

const STATE: Record<string, [string, string]> = {
  ok: ['', ''], missing: ['s-err', 'Service introuvable'], indirect: ['s-mute', 'TraefikService'],
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
            <li key={routeKey(r)}><button onClick={goRoute(r)}><span className="nm">{r.name}</span><span className="later">{r.source} · porte {r.gate}</span></button></li>
          ))}
        </ul>
      ) : <p className="note">Aucune Ingress ni IngressRoute ne vise ce Service.</p>}
    </div>
  )
}

export function RouteOverview({ r }: { r: Route }) {
  const services = useCluster.getState().services
  return (
    <div className="p-body">
      <dl className="kv">
        <dt>Porte</dt><dd><button className="link" onClick={goGate(r.gate)}>{r.gate}</button></dd>
        <dt>Source</dt><dd>{r.source} ({r.group})</dd>
        {r.addresses?.length ? <><dt>Adresses</dt><dd>{r.addresses.join(', ')}</dd></> : null}
      </dl>
      <h3>Règles ({r.rules.length})</h3>
      <table className="evt" data-testid="rules">
        <thead>
          <tr><th scope="col">Hôte · chemin</th><th scope="col">Backend</th></tr>
        </thead>
        <tbody>
          {r.rules.map((rule, i) => {
            const b = rule.backend
            const [cls, label] = STATE[b.state] ?? STATE.ok
            return (
              <tr key={i} className={b.state === 'missing' ? 'warning' : ''}>
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
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

export function GateOverview({ g }: { g: Gate }) {
  return (
    <div className="p-body">
      <p className="note">Contrôleur d'entrée « {g.name} » : chaque route ci-dessous entre dans la ville par cette porte.</p>
      <ul className="podlist" data-testid="gate-routes">
        {g.routes.map((r) => (
          <li key={routeKey(r)}>
            <button onClick={goRoute(r)}>
              <span className="nm">{r.name}</span>
              <span className="later">{r.source} · {r.namespace}</span>
              {routeBroken(r) && <span className="s-err">Service introuvable</span>}
            </button>
          </li>
        ))}
      </ul>
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
