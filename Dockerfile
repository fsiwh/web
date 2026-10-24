FROM --platform=$BUILDPLATFORM nixos/nix:2.35.4@sha256:f159a58f390677f41e8e900a74b6e2f9c7f27b364122ae6acf7a652ce6c551c0 AS assets
WORKDIR /src
COPY flake.nix flake.lock go.mod ./
COPY view/styles ./view/styles
COPY view/*.ts ./view/
RUN nix --extra-experimental-features "nix-command flakes" build .#assets --out-link /assets && \
    cp -RL /assets /generated-assets

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go application.go ./
COPY internal ./internal
COPY model ./model
COPY site ./site
COPY storage ./storage
COPY view ./view
COPY --from=assets /generated-assets/ ./view/assets/
ARG TARGETOS TARGETARCH
ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION}" -o /out/web .

FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a AS app
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S -g 1001 web && adduser -S -u 1001 -G web web && \
    mkdir -p /data && chown web:web /data
WORKDIR /app
COPY --from=builder /out/web ./web
USER web:web
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/app/web", "-check-health", "http://127.0.0.1:8080/healthz"]
ENTRYPOINT ["/app/web"]
CMD ["-addr", "0.0.0.0:8080", "-database", "/data/sqlite.db"]
