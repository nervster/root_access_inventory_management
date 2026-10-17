# Production image for the Plant Stock API and background-job worker (run /worker for the worker).

# 1. Build a static Go binary. Go cross-compiles, so this stage runs natively even when building the
#    VM's amd64 image on an ARM Mac (docker build --platform linux/amd64).
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY . .
# Downloads only the modules the API imports (not dev tools like sqlc and goose).
# The cache mounts keep modules and build output between builds.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/ ./cmd/api ./cmd/worker

# 2. Run it on a minimal image: no shell, no package manager, non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /out/worker /
ENV GIN_MODE=release PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
