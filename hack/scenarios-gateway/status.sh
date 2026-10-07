#!/usr/bin/env sh
# Statut des objets Gateway API des scénarios : kind n'a pas de contrôleur
# Gateway, on écrit à la main ce qu'il publierait (sous-ressource status).
# Le contexte kubectl est toujours explicite (kind-atlas par défaut).
set -eu
CTX="${1:-kind-atlas}"
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
k() { kubectl --context "$CTX" "$@"; }

# cond TYPE STATUS RAISON [MESSAGE]
cond() {
  printf '{"type":"%s","status":"%s","reason":"%s","message":"%s","lastTransitionTime":"%s","observedGeneration":1}' "$1" "$2" "$3" "${4:-}" "$NOW"
}
# listener NOM ROUTES STATUS RAISON
listener() {
  printf '{"name":"%s","supportedKinds":[{"group":"gateway.networking.k8s.io","kind":"HTTPRoute"},{"group":"gateway.networking.k8s.io","kind":"GRPCRoute"}],"attachedRoutes":%s,"conditions":[%s]}' \
    "$1" "$2" "$(cond Programmed "$3" "$4")"
}
# parent NS NOM ACCEPTED RAISON
parent() {
  printf '{"parentRef":{"group":"gateway.networking.k8s.io","kind":"Gateway","namespace":"%s","name":"%s"},"controllerName":"example.com/atlas-scenarios","conditions":[%s,%s]}' \
    "$1" "$2" "$(cond Accepted "$3" "$4")" "$(cond ResolvedRefs True ResolvedRefs)"
}

k -n infra patch gateway public --subresource=status --type=merge -p "{\"status\":{
  \"addresses\":[{\"type\":\"IPAddress\",\"value\":\"172.18.0.100\"}],
  \"conditions\":[$(cond Accepted True Accepted),$(cond Programmed True Programmed)],
  \"listeners\":[$(listener http 1 True Programmed),$(listener https 0 True Programmed)]}}"
k -n infra patch gateway internal --subresource=status --type=merge -p "{\"status\":{
  \"conditions\":[$(cond Accepted True Accepted),$(cond Programmed False AddressNotAssigned 'No address has been assigned to the Gateway')],
  \"listeners\":[$(listener http 1 False Pending)]}}"
k -n kube-system patch gateway platform --subresource=status --type=merge -p "{\"status\":{
  \"conditions\":[$(cond Accepted True Accepted),$(cond Programmed True Programmed)],
  \"listeners\":[$(listener http 0 True Programmed)]}}"
k -n production patch httproute storefront --subresource=status --type=merge -p "{\"status\":{\"parents\":[$(parent infra public True Accepted)]}}"
k -n staging patch httproute preview --subresource=status --type=merge -p "{\"status\":{\"parents\":[$(parent infra public False NotAllowedByListeners)]}}"
echo "statut Gateway API écrit"
