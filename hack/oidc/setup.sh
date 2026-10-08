#!/usr/bin/env sh
# Prépare le cluster kind pour un parcours OIDC complet par URL :
# ingress-nginx sur le port 80, Dex dans le cluster, réécriture CoreDNS.
set -eu
CTX="${CTX:-kind-atlas}"
"$(dirname "$0")/../kind-guard.sh" "$CTX"
k() { kubectl --context "$CTX" "$@"; }
HERE="$(cd "$(dirname "$0")" && pwd)"

k apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.13.3/deploy/static/provider/kind/deploy.yaml
k -n ingress-nginx rollout status deployment ingress-nginx-controller --timeout=180s
# Le webhook d'admission peut mettre quelques secondes à répondre.
until k -n ingress-nginx get endpoints ingress-nginx-controller-admission -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null | grep -q .; do sleep 2; done

# Même configuration que le Dex local, avec l'issuer et l'URL de retour du cluster.
sed -e 's|issuer: http://localhost:5556/dex|issuer: http://dex.localtest.me/dex|' \
    -e 's|      - http://localhost:5173/auth/callback|      - http://atlas.localtest.me/auth/callback|' \
    "$HERE/../dex/config.yaml" > /tmp/atlas-dex-config.yaml
k apply -f "$HERE/dex.yaml"
k -n dex create configmap dex --from-file=config.yaml=/tmp/atlas-dex-config.yaml --dry-run=client -o yaml | k apply -f -
k -n dex rollout restart deployment dex
k -n dex rollout status deployment dex --timeout=120s

# Les pods résolvent dex.localtest.me vers l'ingress (et non 127.0.0.1).
if ! k -n kube-system get configmap coredns -o jsonpath='{.data.Corefile}' | grep -q 'dex.localtest.me'; then
  k -n kube-system get configmap coredns -o jsonpath='{.data.Corefile}' \
    | awk '/^[[:space:]]*kubernetes cluster.local/ { print "    rewrite name dex.localtest.me ingress-nginx-controller.ingress-nginx.svc.cluster.local" } { print }' \
    > /tmp/atlas-corefile
  k -n kube-system create configmap coredns --from-file=Corefile=/tmp/atlas-corefile --dry-run=client -o yaml | k apply -f -
  k -n kube-system rollout restart deployment coredns
  k -n kube-system rollout status deployment coredns --timeout=120s
fi
k apply -f "$HERE/../dev-rbac.yaml"
