# Production image for the NMS API.

# 1. Build a static Go binary.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY . .
# Downloads only the modules the API imports (not dev tools like sqlc and goose).
# The cache mounts keep modules and build output between builds.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# 2. Run it on a minimal image: no shell, no package manager, non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
ENV GIN_MODE=release PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
