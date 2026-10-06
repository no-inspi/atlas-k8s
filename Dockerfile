# syntax=docker/dockerfile:1.7
# Image unique : front compilé, embarqué dans un binaire Go statique, servi
# depuis distroless (pas de shell, utilisateur non-root 65532).

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/
COPY --from=web /src/web/dist web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/atlas ./cmd/atlas

FROM gcr.io/distroless/static:nonroot
COPY --from=go /out/atlas /atlas
USER 65532:65532
EXPOSE 8080 9090
ENTRYPOINT ["/atlas"]
