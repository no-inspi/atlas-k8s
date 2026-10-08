#!/usr/bin/env sh
# Installe metrics-server sur le cluster kind (certificats kubelet auto-signés).
set -eu
CTX="${1:-kind-atlas}"
"$(dirname "$0")/kind-guard.sh" "$CTX"
kubectl --context "$CTX" apply -f https://github.com/kubernetes-sigs/metrics-server/releases/download/v0.7.2/components.yaml
kubectl --context "$CTX" -n kube-system patch deployment metrics-server --type=json \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]'
kubectl --context "$CTX" -n kube-system rollout status deployment metrics-server --timeout=120s
