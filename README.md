# Plant Stock — inventory for plant nurseries

A multi-tenant web app for independent gardens and nurseries to manage their plant inventory, team,
and photos, and to sell online through their own Shopify store. Root Access HTX (Houston) is the
first nursery on the platform.

See [docs/ROADMAP.md](docs/ROADMAP.md) for decisions, what's built, and what's next.

## Stack
- **API:** Go + [Gin](https://gin-gonic.com) — `backend/`
- **Web:** React + TypeScript + Vite — `frontend/`
- **Data:** PostgreSQL 17 (also the background-job queue), Valkey (cache), S3-compatible storage (photos)
- **Auth:** [Clerk](https://clerk.com), sign-in only (invite-only)
- **Runs as containers:** one image for the API, one for the web app; Kubernetes in production

## Layout
```
backend/
  cmd/api/          entry point: main.go wires everything together
  internal/         feature packages (auth/, organization/, inventory/) and shared platform/ code
  internal/db/      Postgres: migrations/ (goose), queries/<domain>/ (SQL) → sqlc generates dbgen/ (never edit)
  Dockerfile        production image (static binary on distroless, non-root)
frontend/
  src/              React app
  Dockerfile        production image (nginx serving the build, proxying /api)
docker-compose.yml  local Postgres, Valkey, photo storage (SeaweedFS), Mailpit (+ optional app containers)
```

## Prerequisites
- Go 1.27+ ([go.dev/dl](https://go.dev/dl/), macOS ARM64 `.pkg`)
- Node 22+, Docker Desktop
- `cp .env.example .env`, then fill it in (it's git-ignored; the Go API reads it on startup):
  - `CLERK_SECRET_KEY`: Clerk dashboard → API Keys. Also add the session token claims in Clerk
    (Sessions → Customize session token): `{"email": "{{user.primary_email_address}}", "name": "{{user.full_name}}"}`
  - `PLATFORM_ADMIN_EMAILS`: your sign-in email, so you can create nurseries (`/api/platform/...`)
  - `VITE_CLERK_PUBLISHABLE_KEY`: Clerk publishable key (`pk_test_…`), only for building the web container
  - `STORAGE_ENDPOINT=http://localhost:8333`: keeps photos in the local SeaweedFS stand-in (deployed: Google Cloud
    Storage, see `.env.example`)
- `frontend/.env.development.local` (git-ignored) with `VITE_CLERK_PUBLISHABLE_KEY=pk_test_…` for `npm run dev`

## Local development
```sh
docker compose up -d                       # Postgres :5433, Valkey :6379, photo storage :8333, Mailpit :8025
cd backend && make run                     # API on :8080 (`make` lists all backend commands)
cd frontend && npm install && npm run dev  # Vite on :5173, proxies /api to :8080
```
Open http://localhost:5173. Health check: `GET /api/health`. Email the app sends (e.g. invitations) lands in
Mailpit at http://localhost:8025, never in real inboxes. Signed-in user: `GET /api/me`
(send the Clerk session token as `Authorization: Bearer <token>`). Photos go to the SeaweedFS stand-in; the
API creates the `nms-files` bucket there when it starts. Phone cameras (scanning, photos) need https, so locally use
the laptop's webcam at localhost.

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
Tests run against real Postgres (and the photo-storage stand-in for the storage test, which is skipped when
it isn't running), each inside a transaction that's rolled back.
GitHub Actions ([.github/workflows/ci.yml](.github/workflows/ci.yml)) runs the backend checks and tests, the
frontend lint and build, and both Docker builds on every pull request into `staging` or `main`.
