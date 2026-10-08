#!/usr/bin/env sh
# Test de charge (spec : « 3 000 pods sur des nodes kwok ») : NODES faux nodes
# gérés par kwok et PODS pods « pause » qui y tournent, sans consommer de CPU.
set -eu
CTX="${CTX:-kind-atlas}"
"$(dirname "$0")/../kind-guard.sh" "$CTX"
NODES="${NODES:-100}"
PODS="${PODS:-3000}"
KWOK="${KWOK:-v0.8.0}"
SERVICES_PER_DEPLOY="${SERVICES_PER_DEPLOY:-4}"
PVCS="${PVCS:-150}"
GATEWAYS="${GATEWAYS:-10}"
PVS="${PVS:-30}"
k() { kubectl --context "$CTX" "$@"; }

k apply -f "https://github.com/kubernetes-sigs/kwok/releases/download/$KWOK/kwok.yaml"
k apply -f "https://github.com/kubernetes-sigs/kwok/releases/download/$KWOK/stage-fast.yaml"
k -n kube-system rollout status deployment kwok-controller --timeout=180s

# Nodes : 60 % standard, 30 % spot, 10 % GPU, répartis sur trois zones.
i=1
{
  while [ "$i" -le "$NODES" ]; do
    case $((i % 10)) in
      0) pool=kwok-gpu; extra='"nvidia.com/gpu": "1",' ;;
      7|8|9) pool=kwok-spot; extra='' ;;
      *) pool=kwok-default; extra='' ;;
    esac
    spot=false; [ "$pool" = kwok-spot ] && spot=true
    cat <<YAML
---
apiVersion: v1
kind: Node
metadata:
  name: kwok-node-$(printf %03d "$i")
  annotations: { kwok.x-k8s.io/node: fake }
  labels:
    type: kwok
    cloud.google.com/gke-nodepool: $pool
    cloud.google.com/gke-spot: "$spot"
    node.kubernetes.io/instance-type: n2-standard-32
    topology.kubernetes.io/zone: europe-west1-$(echo b c d | cut -d' ' -f$((i % 3 + 1)))
spec:
  taints: [{ key: kwok.x-k8s.io/node, value: fake, effect: NoSchedule }]
status:
  allocatable: { cpu: "32", memory: 128Gi, pods: "110", $extra }
  capacity: { cpu: "32", memory: 128Gi, pods: "110", $extra }
YAML
    i=$((i + 1))
  done
} | k apply -f - >/dev/null
echo "$NODES nodes kwok créés"

# Pods : des Deployments de 30 replicas, épinglés sur les nodes kwok.
k create namespace load-test --dry-run=client -o yaml | k apply -f - >/dev/null
DEPLOYS=$(( (PODS + 29) / 30 ))
d=1
{
  while [ "$d" -le "$DEPLOYS" ]; do
    cat <<YAML
---
apiVersion: apps/v1
kind: Deployment
metadata: { name: load-$(printf %03d "$d"), namespace: load-test }
spec:
  replicas: 30
  selector: { matchLabels: { app: load-$(printf %03d "$d") } }
  template:
    metadata: { labels: { app: load-$(printf %03d "$d") } }
    spec:
      nodeSelector: { type: kwok }
      tolerations: [{ key: kwok.x-k8s.io/node, operator: Exists, effect: NoSchedule }]
      containers:
        - name: app
          image: registry.k8s.io/pause:3.10
          resources: { requests: { cpu: 100m, memory: 64Mi } }
YAML
    s=1
    while [ "$s" -le "$SERVICES_PER_DEPLOY" ]; do
      cat <<YAML
---
apiVersion: v1
kind: Service
metadata: { name: load-$(printf %03d "$d")-$s, namespace: load-test }
spec:
  selector: { app: load-$(printf %03d "$d") }
  ports: [{ port: 80 }]
YAML
      s=$((s + 1))
    done
    d=$((d + 1))
  done
} | k apply -f - >/dev/null
# PVC sans consommateur (Pending) : des citernes dans les entrepôts.
v=1
{
  while [ "$v" -le "$PVCS" ]; do
    cat <<YAML
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: { name: data-$(printf %03d "$v"), namespace: load-test }
spec: { accessModes: [ReadWriteOnce], resources: { requests: { storage: 1Gi } } }
YAML
    v=$((v + 1))
  done
} | k apply -f - >/dev/null
# Gateway API (si ses CRD sont installées, par make scenarios) : GATEWAYS
# Gateways et une HTTPRoute 90/10 par Deployment, réparties sur eux.
if k get crd httproutes.gateway.networking.k8s.io >/dev/null 2>&1; then
  {
    g=1
    while [ "$g" -le "$GATEWAYS" ]; do
      cat <<YAML
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata: { name: load-gw-$(printf %02d "$g"), namespace: load-test }
spec:
  gatewayClassName: atlas-scenarios
  listeners: [{ name: http, protocol: HTTP, port: 80 }]
YAML
      g=$((g + 1))
    done
    d=1
    while [ "$d" -le "$DEPLOYS" ]; do
      cat <<YAML
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: { name: load-$(printf %03d "$d"), namespace: load-test }
spec:
  parentRefs: [{ name: load-gw-$(printf %02d $(( (d - 1) % GATEWAYS + 1 ))) }]
  hostnames: [load-$(printf %03d "$d").localtest.me]
  rules:
    - backendRefs:
        - { name: load-$(printf %03d "$d")-1, port: 80, weight: 90 }
        - { name: load-$(printf %03d "$d")-2, port: 80, weight: 10 }
YAML
      d=$((d + 1))
    done
  } | k apply -f - >/dev/null
  echo "$GATEWAYS Gateways et $DEPLOYS HTTPRoutes créés dans load-test"
fi
# PV sans PVC (Available) : des citernes vides dans les entrepôts.
p=1
{
  while [ "$p" -le "$PVS" ]; do
    cat <<YAML
---
apiVersion: v1
kind: PersistentVolume
metadata: { name: load-pv-$(printf %03d "$p"), labels: { atlas-load: "true" } }
spec:
  capacity: { storage: 1Gi }
  accessModes: [ReadWriteOnce]
  storageClassName: load-orphans
  hostPath: { path: /tmp/load-pv-$(printf %03d "$p") }
YAML
    p=$((p + 1))
  done
} | k apply -f - >/dev/null
echo "$PVS PV orphelins créés"
echo "$((DEPLOYS * SERVICES_PER_DEPLOY)) Services et $PVCS PVC créés dans load-test"
echo "$((DEPLOYS * 30)) pods demandés dans load-test ($DEPLOYS Deployments) ; attente…"
until [ "$(k -n load-test get pods --field-selector=status.phase=Running --no-headers 2>/dev/null | wc -l | tr -d ' ')" -ge "$PODS" ]; do sleep 5; done
echo "$PODS pods Running sur $NODES nodes kwok"
