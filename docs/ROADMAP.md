# Plant Stock Roadmap

**Goal:** a web app where any independent garden or nursery can manage its plants, team, and photos, and sell
online. Root Access HTX is the first nursery.

## Decisions
| Topic | Decision |
|---|---|
| Backend | Go with Gin. Feature packages (`internal/auth`, `internal/organization`, `internal/inventory`) plus shared `internal/platform`; SQL via sqlc (`internal/db`) |
| Frontend | React + TypeScript (Vite), served by its own nginx container |
| Data | PostgreSQL (main database), Valkey (cache), AWS S3 (plant photos), AWS SQS (background jobs) |
| Local dev | Docker Compose: Postgres, Valkey, LocalStack (S3 + SQS). Go and Vite run on the host |
| Production | Two containers (API, web) on Kubernetes, one domain: the ingress routes `/api` to the API |
| Tenancy | One database, `organization_id` on every nursery-owned row. Postgres Row-Level Security keeps nurseries apart: nursery work runs as role `nms_tenant` via `db.InTenant`, and a test fails if a table with `organization_id` lacks RLS. Shared reference data (e.g. plant categories) has no `organization_id` and is read-only to nurseries |
| Sign-in | Clerk handles identity only (email + password/code, Google). Invite-only (Restricted sign-up mode) |
| Access | Roles (Owner, Admin, Staff, Viewer) map to permissions; endpoints check permissions. Only Owners see financials |
| Onboarding | Platform admin creates each nursery and invites its first Owner. Self-serve signup and billing come later |
| Selling | Each nursery connects its own Shopify store. A storefront hosted by Plant Stock is a much later phase |
| Source of truth | Plant Stock's database is the master record for plants, photos, and sales; Shopify is one sales channel |
| Employees | Team members are people with logins. No tracking of employees who don't sign in |
| Mobile | Responsive web app/PWA; staff add plants and photos from their phones |
| Inventory | Tissue-culture batches → one plant per barcode (`RAH-000123`) → grown on through the nursery's pot sizes → for sale. A product is a plant name + variegation; it has one SKU per sellable pot size (Shopify product + variants). Marking plants for sale creates them; prices are set after. Stock is counted from for-sale plants, never typed in |
| Plant names | Shared list (family → genus → species → cultivar, plus variegations), read-only; each nursery can add its own names on top |
| Photos | Phones upload straight to S3 (LocalStack locally) with short-lived signed links; private bucket, per-nursery paths. Plant photos form a growth timeline; a product's images are chosen from them |
| History | Every change to a plant (received, moved, for sale, discarded, corrected, photos) is recorded by Postgres triggers, with who by name. History can't be edited |

## Phases

### 1. Foundation
- [x] Scaffold: Go API (Gin) and React app, each with a production Dockerfile; Compose with Postgres, Valkey, LocalStack
- [x] Config, Postgres connection, migrations (pgx, sqlc, goose; migrations run at API startup)
- [x] Organizations, users, memberships, invitations; Clerk sign-in; tenant isolation (Row-Level Security); permissions
- [x] Platform admin: create/suspend nurseries, fix memberships, user lookup; no access to business data (API; admins listed in `PLATFORM_ADMIN_EMAILS`)
- [x] Web app: sign-in, accept invitations, nursery switcher, Team page, settings, Platform section
      (ported from the earlier .NET branch to the Go API)
- [x] CI: GitHub Actions running build and tests

### 2. Inventory: tissue culture → for sale
- [x] Plant names: shared list (281 names, 6 variegations); nurseries add their own
- [x] Pot sizes per nursery, each sellable or grow-on only (e.g. the Tissue Culture Cup)
- [x] Receive batches: one plant per barcode, printable QR labels (letter paper, Avery 5160 layout)
- [x] Grow on: repot whole batches or scanned plants; discard with a reason; correct a plant's or a batch's name or variegation; plant notes; edit batch details
- [x] Scan labels with the phone camera (or type / handheld scanner) and act on the scanned plants
- [x] Products and SKUs created by marking plants for sale; prices, title, description, product type, tags; live stock per SKU
- [x] Photos from phones straight to S3; growth timeline per plant; ordered product images chosen from plant photos
- [x] Plant history (audit log) recorded by Postgres triggers, with who by name
- [x] Dashboard: for-sale value, plants to mark for sale or price, growth pipeline by pot size
- [ ] Reports: profit per plant, sales over time (Owner only), once sales arrive (Phase 3)
- [ ] Price-change history (audit log for products and prices)
- [ ] Non-plant products (soil mix, merch) with stock counts
- [ ] The plant names still marked CHECK in `backend/internal/db/seed/plant_names.csv`
- [ ] Customers picking a specific plant from its photos

### 3. Shopify, per nursery
- [x] Shopify app with OAuth install, so each nursery connects its own store; tokens encrypted at rest
- [x] Background jobs: outbox table + SQS worker (`cmd/worker`), with retries and a dead-letter queue
- [x] Push products, SKUs (variants), images, prices, and stock to Shopify; SKUs without a price stay off it
- [x] Orders arrive (checked each minute, plus webhooks once deployed) and hold specific plants until pickup; cancelled orders put them back on sale; Orders page lists what to pull
- [ ] Pickup by scanning the held plants (sold, and fulfilled in Shopify)
- [ ] Refunds: returned plants wait for a check before going back on sale
- [ ] Register Shopify webhooks (orders, app/uninstalled, privacy topics) for the deployed URL
- [ ] Nightly sync check that finds and fixes drift between Plant Stock and Shopify

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
- [ ] S3 bucket for photos, private, with CORS allowing the web app's origin; HTTPS everywhere (phone cameras need it)
- [ ] Replace LocalStack if Plant Stock becomes commercial (its free plan is non-commercial)
