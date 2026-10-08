#!/usr/bin/env sh
# Nettoie le test de charge kwok.
set -eu
CTX="${CTX:-kind-atlas}"
"$(dirname "$0")/../kind-guard.sh" "$CTX"
kubectl --context "$CTX" delete namespace load-test --ignore-not-found --wait=true
kubectl --context "$CTX" delete pv -l atlas-load=true --ignore-not-found
kubectl --context "$CTX" delete nodes -l type=kwok --ignore-not-found
