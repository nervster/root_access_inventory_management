# Production image for the NMS API.

# 1. Build a static Go binary.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# 2. Run it on a minimal image: no shell, no package manager, non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api /api
ENV GIN_MODE=release PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
