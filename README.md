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
  internal/db/      Postgres: migrations/ (goose), queries/<domain>/ (SQL) → sqlc generates dbgen/ (never edit)
  Dockerfile        production image (static binary on distroless, non-root)
frontend/
  src/              React app
  Dockerfile        production image (nginx serving the build, proxying /api)
docker-compose.yml  local Postgres, Valkey, LocalStack (+ optional app containers)
```

## Prerequisites
- Go 1.27+ ([go.dev/dl](https://go.dev/dl/), macOS ARM64 `.pkg`)
- Node 22+, Docker Desktop
- `cp .env.example .env`, then fill it in (it's git-ignored; the Go API reads it on startup):
  - `CLERK_SECRET_KEY`: Clerk dashboard → API Keys. Also add the session token claims in Clerk
    (Sessions → Customize session token): `{"email": "{{user.primary_email_address}}", "name": "{{user.full_name}}"}`
  - `PLATFORM_ADMIN_EMAILS`: your sign-in email, so you can create nurseries (`/api/platform/...`)
  - `VITE_CLERK_PUBLISHABLE_KEY`: Clerk publishable key (`pk_test_…`), only for building the web container
- `frontend/.env.development.local` (git-ignored) with `VITE_CLERK_PUBLISHABLE_KEY=pk_test_…` for `npm run dev`
  - `LOCALSTACK_AUTH_TOKEN`: a free [LocalStack](https://app.localstack.cloud) account, for the local S3/SQS stand-in

## Local development
```sh
docker compose up -d                       # Postgres :5433, Valkey :6379, LocalStack :4566, Mailpit :8025
cd backend && make run                     # API on :8080 (`make` lists all backend commands)
cd frontend && npm install && npm run dev  # Vite on :5173, proxies /api to :8080
```
Open http://localhost:5173. Health check: `GET /api/health`. Email the app sends (e.g. invitations) lands in
Mailpit at http://localhost:8025, never in real inboxes. Signed-in user: `GET /api/me`
(send the Clerk session token as `Authorization: Bearer <token>`).

The API runs database migrations when it starts. To change the schema or queries (from `backend/`):
```sh
make migration name=create_plants          # new numbered file in internal/db/migrations; fill in Up and Down
# edit SQL in internal/db/queries/<domain>/ (new folder? add it to sqlc.yaml)
make generate                              # regenerate internal/db/dbgen
make migrate-status                        # what has run locally; make migrate-down rolls back the last one
```

Run the production containers locally (web on :8081, API on :8080):
```sh
docker compose --profile app up -d --build
```

## Tests
```sh
cd backend && make test                    # needs `docker compose up -d`; uses a separate nms_test database
```
GitHub Actions ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs the backend checks and tests, the
frontend lint and build, and both Docker builds on every pull request into `staging` or `main`.
