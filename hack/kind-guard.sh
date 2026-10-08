#!/usr/bin/env sh
# Garde-fou des cibles et scripts qui écrivent dans le cluster kind : refuse
# (exit 1, avant tout kubectl qui écrit) un contexte qui ne commence pas par
# kind- ou dont le serveur n'est pas en boucle locale (127.0.0.1, localhost,
# [::1]). Usage : hack/kind-guard.sh CONTEXTE
set -eu
ctx="${1:-}"
refuse() {
  echo "kind-guard : contexte « $ctx » refusé : $1" >&2
  exit 1
}
case "$ctx" in
  kind-*) ;;
  *) refuse "seul un contexte kind-* est accepté" ;;
esac
server=$(kubectl config view --minify --context "$ctx" -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null) \
  || refuse "contexte introuvable dans la kubeconfig"
# https://HÔTE:PORT/chemin → HÔTE (crochets gardés pour IPv6).
host="${server#*://}"
host="${host%%/*}"
case "$host" in
  \[*) host="${host%%]*}]" ;;
  *) host="${host%%:*}" ;;
esac
case "$host" in
  127.0.0.1 | localhost | "[::1]") ;;
  *) refuse "serveur « $server » hors boucle locale" ;;
esac
