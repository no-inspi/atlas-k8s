#!/usr/bin/env bash
# Déploie Atlas sur un cluster distant depuis le poste : construit l'image du
# commit courant, la pousse, puis fait le helm upgrade.
#
# Usage : hack/deploy.sh CIBLE [--dry-run] [--yes]
#
# La cible est décrite par deploy/CIBLE.env (ignoré par git, voir
# deploy/cible.env.example) :
#   KUBE_CONTEXT      contexte kubeconfig
#   EXPECTED_CLUSTER  cluster attendu derrière ce contexte (garde-fou)
#   IMAGE             dépôt de l'image (sans tag)
#   VALUES            fichier de valeurs Helm
#   NAMESPACE         défaut : cluster-atlas
#   PLATFORM          défaut : linux/amd64
#
# --dry-run : vérifications et helm upgrade --dry-run=server, sans image poussée.
# --yes     : pas de confirmation interactive.
set -euo pipefail

cd "$(dirname "$0")/.."

die() { echo "deploy : $*" >&2; exit 1; }

target="" dry_run=0 yes=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) dry_run=1 ;;
    --yes) yes=1 ;;
    -*) die "option inconnue : $arg" ;;
    *) [ -z "$target" ] || die "une seule cible"; target="$arg" ;;
  esac
done
[ -n "$target" ] || die "usage : hack/deploy.sh CIBLE [--dry-run] [--yes]"

env_file="deploy/$target.env"
[ -f "$env_file" ] || die "$env_file introuvable (copier deploy/cible.env.example)"
# shellcheck source=/dev/null
. "$env_file"
NAMESPACE="${NAMESPACE:-cluster-atlas}"
PLATFORM="${PLATFORM:-linux/amd64}"
for v in KUBE_CONTEXT EXPECTED_CLUSTER IMAGE VALUES; do
  [ -n "${!v:-}" ] || die "$v manquant dans $env_file"
done
[ -f "$VALUES" ] || die "fichier de valeurs $VALUES introuvable"

for bin in kubectl helm docker; do
  command -v "$bin" >/dev/null || die "$bin introuvable"
done

# Garde-fou : le contexte doit pointer sur le cluster attendu.
cluster=$(kubectl config view -o jsonpath="{.contexts[?(@.name==\"$KUBE_CONTEXT\")].context.cluster}")
[ -n "$cluster" ] || die "contexte $KUBE_CONTEXT absent de la kubeconfig"
[ "$cluster" = "$EXPECTED_CLUSTER" ] \
  || die "le contexte $KUBE_CONTEXT pointe sur $cluster, pas sur $EXPECTED_CLUSTER"

# Le tag de l'image est le commit : refuse un arbre modifié.
version=$(git describe --tags --always --dirty)
case "$version" in
  *-dirty) die "arbre de travail modifié : commitez avant de déployer" ;;
esac

# Accès au cluster (un jeton gcloud expiré échoue ici, avant le build).
kubectl --context "$KUBE_CONTEXT" version --request-timeout=15s >/dev/null 2>&1 \
  || die "cluster injoignable via $KUBE_CONTEXT (sur GKE : gcloud auth login)"
kubectl --context "$KUBE_CONTEXT" auth can-i create clusterroles --request-timeout=15s >/dev/null 2>&1 \
  || die "droits insuffisants sur $KUBE_CONTEXT : le chart crée un ClusterRole"

# Les imagePullSecrets des valeurs doivent exister dans le namespace.
if kubectl --context "$KUBE_CONTEXT" get namespace "$NAMESPACE" >/dev/null 2>&1; then
  for s in $(helm template x deploy/helm/cluster-atlas -f "$VALUES" --show-only templates/deployment.yaml \
      | sed -n '/imagePullSecrets:/,/containers:/s/.*- name: *//p'); do
    kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get secret "$s" >/dev/null 2>&1 \
      || die "secret $s absent de $NAMESPACE : kubectl --context $KUBE_CONTEXT -n $NAMESPACE create secret docker-registry $s --docker-server=… --docker-username=… --docker-password=…"
  done
else
  echo "deploy : namespace $NAMESPACE absent, il sera créé (pensez aux imagePullSecrets éventuels)"
fi

current=$(helm --kube-context "$KUBE_CONTEXT" -n "$NAMESPACE" get values cluster-atlas -a -o json 2>/dev/null \
  | sed -n 's/.*"tag":"\([^"]*\)".*/\1/p' || true)
cat <<EOF
cible      $target
contexte   $KUBE_CONTEXT ($EXPECTED_CLUSTER)
namespace  $NAMESPACE
image      $IMAGE:$version ($PLATFORM)
valeurs    $VALUES
en place   ${current:-aucune release}
EOF

if [ "$dry_run" = 1 ]; then
  helm upgrade --install cluster-atlas deploy/helm/cluster-atlas --kube-context "$KUBE_CONTEXT" \
    -n "$NAMESPACE" --dry-run=server -f "$VALUES" \
    --set image.repository="$IMAGE" --set image.tag="$version" >/dev/null
  echo "dry-run OK : rien n'a été poussé ni appliqué"
  exit 0
fi

if [ "$yes" != 1 ]; then
  read -r -p "Déployer sur $KUBE_CONTEXT ? Taper le nom de la cible pour confirmer : " answer
  [ "$answer" = "$target" ] || die "annulé"
fi

# Image déjà poussée pour ce commit : pas de rebuild.
if docker buildx imagetools inspect "$IMAGE:$version" >/dev/null 2>&1; then
  echo "image $IMAGE:$version déjà présente, build ignoré"
else
  docker buildx build --platform "$PLATFORM" --build-arg VERSION="$version" -t "$IMAGE:$version" --push .
fi

helm upgrade --install cluster-atlas deploy/helm/cluster-atlas --kube-context "$KUBE_CONTEXT" \
  -n "$NAMESPACE" --create-namespace --wait --timeout 5m --atomic \
  -f "$VALUES" --set image.repository="$IMAGE" --set image.tag="$version"

kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get pods -l app.kubernetes.io/instance=cluster-atlas
