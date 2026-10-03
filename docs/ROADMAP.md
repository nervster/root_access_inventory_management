# NMS Roadmap

**Goal:** a web app where any independent garden or nursery can manage its plants, team, and photos, and sell
online. Root Access HTX is the first nursery.

## Decisions
| Topic | Decision |
|---|---|
| Backend | Go with Gin. Feature packages (`internal/plants`, `internal/organizations`, …) plus shared `internal/platform` |
| Frontend | React + TypeScript (Vite), served by its own nginx container |
| Data | PostgreSQL (main database), Valkey (cache), AWS S3 (plant photos), AWS SQS (background jobs) |
| Local dev | Docker Compose: Postgres, Valkey, LocalStack (S3 + SQS). Go and Vite run on the host |
| Production | Two containers (API, web) on Kubernetes, one domain: the ingress routes `/api` to the API |
| Tenancy | One database, `organization_id` on every tenant-owned row, enforced in one place so no query can forget it |
| Sign-in | Clerk handles identity only (email + password/code, Google). Invite-only (Restricted sign-up mode) |
| Access | Roles (Owner, Admin, Staff, Viewer) map to permissions; endpoints check permissions. Only Owners see financials |
| Onboarding | Platform admin creates each nursery and invites its first Owner. Self-serve signup and billing come later |
| Selling | Each nursery connects its own Shopify store. A storefront hosted by NMS is a much later phase |
| Source of truth | NMS's database is the master record for plants, photos, and sales; Shopify is one sales channel |
| Employees | Team members are people with logins. No tracking of employees who don't sign in |
| Mobile | Responsive web app/PWA; staff add plants and photos from their phones |

## Phases

### 1. Foundation (in progress)
- [x] Scaffold: Go API (Gin) and React app, each with a production Dockerfile; Compose with Postgres, Valkey, LocalStack
- [x] Config, Postgres connection, migrations (pgx, sqlc, goose; migrations run at API startup)
- [ ] Organizations, users, memberships, invitations; Clerk sign-in; tenant isolation; permissions
- [ ] Platform admin: create/suspend nurseries, fix memberships, user lookup; no access to business data
- [ ] Web app: sign-in, accept invitations, nursery switcher, Team page, settings, Platform section
      (a working React version exists on the earlier .NET branch and can be reused)
- [ ] CI: GitHub Actions running build and tests

### 2. Inventory
- [ ] Plants (SKU unique per nursery, species, variety, source, acquired date, cost, status), sales, listings,
      all tenant-owned
- [ ] Photo uploads from phones straight to S3; growth timeline per plant (per-nursery paths)
- [ ] Plant list, detail, and add/edit screens built for phones
- [ ] Reports: profit per plant, sales over time (Owner only)
- [ ] Audit log: who changed what, when

### 3. Shopify, per nursery
- [ ] Shopify app with OAuth install, so each nursery connects its own store; tokens encrypted at rest
- [ ] Push listings, photos, prices, and stock (quantity 1 per unique plant) to Shopify
- [ ] Order and refund webhooks mark plants sold or available again (idempotent), processed via SQS
- [ ] Nightly sync check that finds and fixes drift between NMS and Shopify

### 4. Pickup and lockers (optional per nursery)
- [ ] Pickup PIN emails to customers after an order
- [ ] Locations (greenhouses, locker sites)
- [ ] Lockers with temperature, humidity, and PIN control

### 5. Growth (later)
- [ ] Self-serve signup for nurseries, with trials and Stripe billing
- [ ] Custom roles per nursery (the permission model already supports it)
- [ ] 2FA, more sign-in options
- [ ] Hosted storefront with Stripe Connect payouts (much later)

### Deployment (alongside Phase 1–2)
- [ ] Kubernetes manifests (API, web, ingress), managed Postgres, managed Valkey/Redis, real S3 and SQS
- [ ] Production Clerk instance on our own domain; production email provider
- [ ] Replace LocalStack if NMS becomes commercial (its free plan is non-commercial)
