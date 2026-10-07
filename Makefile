.PHONY: deploy load-up load-down kind-oidc helm-kind-oidc dev-demo dex-up dex-down run-dev e2e-auth helm-kind image image-push scan web build test test-go test-web demo dev e2e embed-dir clean kind-up kind-down scenarios run-kind test-integration crds

BIN := bin/atlas
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE ?= ghcr.io/no-inspi/atlas-k8s

# Réglages locaux du déploiement (registre, contexte, valeurs), hors dépôt.
-include deploy.local.mk

embed-dir:
	@mkdir -p web/dist && touch web/dist/.gitkeep

web:
	cd web && npm ci && npm run build
	@touch web/dist/.gitkeep

build: web
	go build -o $(BIN) ./cmd/atlas

test: test-go test-web

test-go: embed-dir
	go vet ./...
	go test -race ./...
	go test -count=1 ./deploy/helm  # les templates Helm échappent au cache de go test

test-web:
	cd web && npm test

demo: build
	$(BIN) --demo

# Backend démo sur :8080 et Vite (HMR) sur :5173, qui proxifie /api.
dev-demo: embed-dir
	go build -o $(BIN) ./cmd/atlas
	$(BIN) --demo & PID=$$!; trap "kill $$PID" EXIT INT TERM; cd web && npm run dev

# --- Développement avec authentification (kind + Dex) ----------------------
# make kind-up scenarios une fois, puis make dev : http://localhost:5173,
# alice@example.com ou bob@example.com, mot de passe « password ».

DEX_IMAGE := ghcr.io/dexidp/dex:v2.45.0
# Clé de dev, publique et sans valeur : ne jamais la réutiliser ailleurs.
DEV_COOKIE_KEY := ZGV2LWNvb2tpZS1rZXktbm90LWZvci1wcm9kLTAwMDA=
OIDC_DEV_FLAGS = --auth-mode=oidc --context $(KIND_CTX) --cluster-name kind-atlas \
	--oidc-issuer-url http://localhost:5556/dex --oidc-client-id cluster-atlas \
	--oidc-client-secret dev-secret-not-for-production --oidc-scopes openid,email,profile,groups

dex-up:
	kubectl --context $(KIND_CTX) apply -f hack/dev-rbac.yaml
	docker rm -f atlas-dex >/dev/null 2>&1 || true
	docker run -d --name atlas-dex -p 5556:5556 -v $(CURDIR)/hack/dex/config.yaml:/etc/dex/config.yaml:ro \
	  $(DEX_IMAGE) dex serve /etc/dex/config.yaml
	@until curl -sf http://localhost:5556/dex/.well-known/openid-configuration >/dev/null; do sleep 1; done

dex-down:
	docker rm -f atlas-dex

dev: embed-dir dex-up
	go build -o $(BIN) ./cmd/atlas
	ATLAS_COOKIE_KEY=$(DEV_COOKIE_KEY) $(BIN) $(OIDC_DEV_FLAGS) --public-url http://localhost:5173 & PID=$$!; \
	  trap "kill $$PID" EXIT INT TERM; cd web && npm run dev

# Binaire compilé en mode OIDC sur :8080 (utilisé par make e2e-auth).
run-dev: build dex-up
	ATLAS_COOKIE_KEY=$(DEV_COOKIE_KEY) $(BIN) $(OIDC_DEV_FLAGS) --public-url http://localhost:8080

e2e-auth: build dex-up
	cd web && npx playwright test -c playwright.auth.config.ts

e2e: build
	cd web && npx playwright test

clean:
	rm -rf bin web/dist/*
	@touch web/dist/.gitkeep

# --- Cluster kind de développement -----------------------------------------

KIND_CTX := kind-atlas
# Garde : sans contexte explicite, kubectl viserait le contexte courant.
ifeq ($(strip $(KIND_CTX)),)
$(error KIND_CTX est vide : les cibles kind exigent un contexte explicite)
endif

kind-up:
	kind create cluster --config hack/kind.yaml
	kubectl --context $(KIND_CTX) taint nodes atlas-worker4 nvidia.com/gpu=present:NoSchedule --overwrite
	hack/metrics-server.sh $(KIND_CTX)

kind-down:
	kind delete cluster --name atlas

# CRD tierces des scénarios, téléchargées une fois à une version épinglée
# (hack/crds/.cache/, ignoré par git) et vérifiées par sha256 ; le test
# d'intégration les réapplique. Changer une version impose de changer son hash.
GATEWAY_API_VERSION ?= v1.3.0
GATEWAY_API_SHA256 ?= 78796d5c51450fc55d8dc8092ba8137f8c807982d7508d7875d5c537a24082b9
TRAEFIK_CRD_VERSION ?= v3.5.6
TRAEFIK_CRD_SHA256 ?= 1f0a915765915aac3293274db2344145fa41b28c1162d7c1a0ce833b0efb890b
GATEWAY_API_CRDS := hack/crds/.cache/gateway-api-$(GATEWAY_API_VERSION)-standard.yaml
TRAEFIK_CRDS := hack/crds/.cache/traefik-$(TRAEFIK_CRD_VERSION)-crds.yaml
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo sha256sum || echo shasum -a 256)

# fetch URL SHA256 : télécharge dans $@.tmp, vérifie le hash, puis renomme.
define fetch
	mkdir -p $(dir $@)
	curl -fsSL -o $@.tmp $(1) && test "$$($(SHA256) $@.tmp | cut -d' ' -f1)" = "$(2)" \
		|| { rm -f $@.tmp; echo "échec du téléchargement ou hash inattendu : $(1)"; exit 1; }
	mv $@.tmp $@
endef

$(GATEWAY_API_CRDS):
	$(call fetch,https://github.com/kubernetes-sigs/gateway-api/releases/download/$(GATEWAY_API_VERSION)/standard-install.yaml,$(GATEWAY_API_SHA256))

# https://doc.traefik.io/traefik/reference/install-configuration/providers/kubernetes/kubernetes-crd/
$(TRAEFIK_CRDS):
	$(call fetch,https://raw.githubusercontent.com/traefik/traefik/$(TRAEFIK_CRD_VERSION)/docs/content/reference/dynamic-configuration/kubernetes-crd-definition-v1.yml,$(TRAEFIK_CRD_SHA256))

crds: $(GATEWAY_API_CRDS) $(TRAEFIK_CRDS)

scenarios: crds
	kubectl --context $(KIND_CTX) apply -f hack/scenarios/
	kubectl --context $(KIND_CTX) apply --server-side -f $(TRAEFIK_CRDS)
	kubectl --context $(KIND_CTX) apply --server-side -f $(GATEWAY_API_CRDS)
	kubectl --context $(KIND_CTX) wait --for condition=established --timeout=60s \
		crd/ingressroutes.traefik.io crd/ingressroutetcps.traefik.io crd/ingressrouteudps.traefik.io crd/traefikservices.traefik.io \
		crd/gateways.gateway.networking.k8s.io crd/httproutes.gateway.networking.k8s.io crd/grpcroutes.gateway.networking.k8s.io
	kubectl --context $(KIND_CTX) apply -f hack/scenarios-traefik/
	kubectl --context $(KIND_CTX) apply -f hack/scenarios-gateway/gateways.yaml
	hack/scenarios-gateway/status.sh $(KIND_CTX)

# Atlas contre le cluster kind, sans authentification (jalon 4 : OIDC via Dex).
run-kind: build
	$(BIN) --auth-mode=none --context $(KIND_CTX) --cluster-name kind-atlas

test-integration: embed-dir
	go test -tags integration -count=1 -v ./internal/kube -run Live

# --- Image et chart --------------------------------------------------------

image:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) .

# Image multi-arch (amd64/arm64), poussée sur le registre.
image-push:
	docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) --push .

scan: image
	trivy image --severity CRITICAL --exit-code 1 --ignore-unfixed $(IMAGE):$(VERSION)

# Installe le chart sur le cluster kind avec l'image locale (sans OIDC : pas d'exposition).
helm-kind: image
	kind load docker-image $(IMAGE):$(VERSION) --name atlas
	helm upgrade --install cluster-atlas deploy/helm/cluster-atlas --kube-context $(KIND_CTX) \
	  -n cluster-atlas --create-namespace --wait --timeout 3m \
	  --set image.tag=$(VERSION) --set image.pullPolicy=Never \
	  --set auth.mode=none --set clusterName=kind-atlas

# Parcours OIDC complet dans le cluster : http://atlas.localtest.me
# (make kind-down kind-up scenarios kind-oidc helm-kind-oidc).
kind-oidc:
	hack/oidc/setup.sh

helm-kind-oidc: image
	kind load docker-image $(IMAGE):$(VERSION) --name atlas
	helm upgrade --install cluster-atlas deploy/helm/cluster-atlas --kube-context $(KIND_CTX) \
	  -n cluster-atlas --create-namespace --wait --timeout 3m \
	  -f hack/oidc/values-kind.yaml --set image.tag=$(VERSION)

# Test de charge : 100 nodes kwok et 3 000 pods (NODES=…, PODS=… pour changer).
load-up:
	hack/load/kwok-up.sh

load-down:
	hack/load/kwok-down.sh

# Déploiement depuis le poste : image construite pour PLATFORM, poussée sur IMAGE,
# puis helm upgrade sur KUBE_CONTEXT avec VALUES (voir deploy.local.mk.example).
PLATFORM ?= linux/amd64
NAMESPACE ?= cluster-atlas
deploy:
	@test -n "$(KUBE_CONTEXT)" || { echo "KUBE_CONTEXT manquant (deploy.local.mk)"; exit 1; }
	@case "$(VERSION)" in *-dirty) echo "arbre de travail modifié : commitez avant de déployer"; exit 1;; esac
	docker buildx build --platform $(PLATFORM) --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) --push .
	helm upgrade --install cluster-atlas deploy/helm/cluster-atlas --kube-context $(KUBE_CONTEXT) \
	  -n $(NAMESPACE) --create-namespace --wait --timeout 3m \
	  $(if $(VALUES),-f $(VALUES)) --set image.repository=$(IMAGE) --set image.tag=$(VERSION)
