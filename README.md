# NMS — Nursery Management System

A multi-tenant web app for independent gardens and nurseries to manage their plant inventory, team,
and photos, and to sell online through their own Shopify store. Root Access HTX (Houston) is the
first nursery on the platform.

See [docs/ROADMAP.md](docs/ROADMAP.md) for decisions, what's built, and what's next.

## Stack
- **API:** Go + [Gin](https://gin-gonic.com) — `backend/`
- **Web:** React + TypeScript + Vite — `frontend/`
- **Data:** PostgreSQL 17, Valkey (cache), AWS S3 (photos), AWS SQS (background jobs)
- **Auth:** [Clerk](https://clerk.com), sign-in only (invite-only)
- **Runs as containers:** one image for the API, one for the web app; Kubernetes in production

## Layout
```
backend/
  cmd/api/          entry point: main.go wires everything together
  internal/         feature packages (organizations/, plants/, …) and shared platform/ code
  Dockerfile        production image (static binary on distroless, non-root)
frontend/
  src/              React app
  Dockerfile        production image (nginx serving the build, proxying /api)
docker-compose.yml  local Postgres, Valkey, LocalStack (+ optional app containers)
```

## Prerequisites
- Go 1.27+ ([go.dev/dl](https://go.dev/dl/), macOS ARM64 `.pkg`)
- Node 22+, Docker Desktop
- A free [LocalStack](https://app.localstack.cloud) account for the local S3/SQS stand-in:
  `cp .env.example .env` and set `LOCALSTACK_AUTH_TOKEN`

## Local development
```sh
docker compose up -d                       # Postgres :5433, Valkey :6379, LocalStack :4566
cd backend && go run ./cmd/api             # API on :8080
cd frontend && npm install && npm run dev  # Vite on :5173, proxies /api to :8080
```
Open http://localhost:5173. Health check: `GET /api/health`.

Run the production containers locally (web on :8081, API on :8080):
```sh
docker compose --profile app up -d --build
```

## Tests
```sh
cd backend && go test ./...
```
