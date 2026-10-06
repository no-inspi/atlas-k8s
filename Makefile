.PHONY: kind-oidc helm-kind-oidc dev-demo dex-up dex-down run-dev e2e-auth helm-kind image image-push scan web build test test-go test-web demo dev e2e embed-dir clean kind-up kind-down scenarios run-kind test-integration

BIN := bin/atlas
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
IMAGE ?= ghcr.io/no-inspi/cluster-atlas

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

kind-up:
	kind create cluster --config hack/kind.yaml
	kubectl --context $(KIND_CTX) taint nodes atlas-worker4 nvidia.com/gpu=present:NoSchedule --overwrite
	hack/metrics-server.sh $(KIND_CTX)

kind-down:
	kind delete cluster --name atlas

scenarios:
	kubectl --context $(KIND_CTX) apply -f hack/scenarios/

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
