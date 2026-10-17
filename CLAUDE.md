# CLAUDE.md

Plant Stock: inventory and online sales (through each nursery's own Shopify store) for plant nurseries.
Root Access HTX is the first nursery. Go API (Gin, sqlc, goose) + React/TypeScript (Vite) + Postgres.
README.md has the layout and commands; docs/ROADMAP.md has the decisions and what's built.

## Git workflow

- `staging` is the integration branch (and the default branch); `main` is production.
- Never push directly to `staging` or `main` — all changes go through PRs.
- Create branches as `feature/<short-name>` off `staging`.
- PRs target `staging`. Only `staging` gets merged into `main`.
- Never commit secrets or `.env` files (only `.env.example`). Don't deploy: merging into `staging` deploys it.

## Before opening a PR

Run what CI runs, and fix what fails:
```sh
cd backend && gofmt -l . && go tool sqlc diff && go vet ./... && make test
cd frontend && npm run lint && npm run build
```
Tests need Postgres and Mailpit (`docker compose up -d` locally; the GitHub workflows start them).
Add or update tests for the change; tests run against real Postgres, so test the SQL, not mocks.

In the PR: what changed and why, how you checked it, and `Closes #<issue>` for an issue. Say plainly if
something couldn't be checked (e.g. against the real Shopify store).

## Backend conventions

- Feature packages in `backend/internal/` (inventory, orders, organization, shopify, auth); shared code in
  `internal/platform/` (web helpers `web.Fail`/`web.FailInternal`, `web.Optional[T]` for PATCH fields, jobs,
  email, storage).
- Schema changes: a new numbered goose migration (`make migration name=...`) with Up and Down; never edit an
  applied one. SQL lives in `internal/db/queries/<domain>/`; run `make generate` after changing it and never edit
  `internal/db/dbgen/`.
- Tenancy: every nursery-owned table has `organization_id`, Row-Level Security, and composite foreign keys
  `(organization_id, id)`; nursery work runs inside `db.InTenant`. `TestEveryTenantTableHasRowLevelSecurity`
  fails if a table is missed.
- Endpoints check permissions (`organization.RequirePermission`), not roles. Only Owners see financials.
- Money is integer cents. Times are `timestamptz`.
- Plant history is written by Postgres triggers (`plant_events`); it's never edited.
- People are shown by name (email if no name), copied at write time (e.g. `changed_by_name`), so it survives
  members leaving.
- Background work goes through the `jobs` table (`jobs.Enqueue` inside the transaction); job handlers must be
  safe to run twice.
- Shopify: Plant Stock is the master record. Check shopify.dev for current API behaviour rather than memory;
  the API version is `APIVersion` in `internal/shopify/app.go`.

## Frontend conventions

- Pages in `frontend/src/pages`, shared pieces in `components/`, API calls and types in `api/` (TanStack Query).
- Phone-first: staff use it on phones in the greenhouse (bottom nav, camera scanning).
- No `window.prompt`: it's silently blocked in some browsers. Use an inline form.

## Style

- User-visible text says "Plant Stock". Internal names keep `nms` (database, role, buckets): don't rename them.
- Comments explain why, in plain words, matching the surrounding code; no comments that restate the code.
- Keep changes to what the issue asks. If it needs a product decision the issue doesn't settle, ask in a
  comment instead of guessing.
