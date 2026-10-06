.PHONY: image image-push scan web build test test-go test-web demo dev e2e embed-dir clean kind-up kind-down scenarios run-kind test-integration

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

test-web:
	cd web && npm test

demo: build
	$(BIN) --demo

# Backend démo sur :8080 et Vite (HMR) sur :5173, qui proxifie /api.
dev: embed-dir
	go build -o $(BIN) ./cmd/atlas
	$(BIN) --demo & PID=$$!; trap "kill $$PID" EXIT INT TERM; cd web && npm run dev

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
