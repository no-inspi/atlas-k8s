.PHONY: web build test test-go test-web demo dev e2e embed-dir clean

BIN := bin/atlas

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
	go run ./cmd/atlas --demo & cd web && npm run dev; kill %1

e2e: build
	cd web && npx playwright test

clean:
	rm -rf bin web/dist/*
	@touch web/dist/.gitkeep
