# Plant Stock Roadmap

**Goal:** a web app where any independent garden or nursery can manage its plants, team, and photos, and sell
online. Root Access HTX is the first nursery.

## Decisions
| Topic | Decision |
|---|---|
| Backend | Go with Gin. Feature packages (`internal/auth`, `internal/organization`, `internal/inventory`) plus shared `internal/platform`; SQL via sqlc (`internal/db`) |
| Frontend | React + TypeScript (Vite), served by its own nginx container |
| Data | PostgreSQL (main database, and the background-job queue: the worker claims jobs from the `jobs` table; a dedicated queue can come later behind the same interface), Valkey (cache), S3-compatible storage for plant photos (Google Cloud Storage when deployed) |
| Local dev | Docker Compose: Postgres, Valkey, SeaweedFS (S3-compatible photo storage), Mailpit. Go and Vite run on the host |
| Production | Google Cloud: two sites, each an e2-micro VM running the Docker Compose stack (web, API, worker, Postgres) with its own Cloud Storage bucket. Staging (`stock-staging.rootaccesshtx.com`, dev Shopify store, Clerk test instance) first; production (`stock.rootaccesshtx.com`, the real store) at launch. Only production may connect the real store. The free tier covers one e2-micro, so the second costs about $7/month. Kubernetes or managed services later, when needed |
| Tenancy | One database, `organization_id` on every nursery-owned row. Postgres Row-Level Security keeps nurseries apart: nursery work runs as role `nms_tenant` via `db.InTenant`, and a test fails if a table with `organization_id` lacks RLS. Shared reference data (e.g. plant categories) has no `organization_id` and is read-only to nurseries |
| Sign-in | Clerk handles identity only (email + password/code, Google). Invite-only (Restricted sign-up mode) |
| Access | Roles (Owner, Admin, Staff, Viewer) map to permissions; endpoints check permissions. Only Owners see financials |
| Onboarding | Platform admin creates each nursery and invites its first Owner. Self-serve signup and billing come later |
| Selling | Each nursery connects its own Shopify store. A storefront hosted by Plant Stock is a much later phase |
| Source of truth | Plant Stock's database is the master record for plants, photos, and sales; Shopify is one sales channel |
| Employees | Team members are people with logins. No tracking of employees who don't sign in |
| Mobile | Responsive web app/PWA; staff add plants and photos from their phones |
| Inventory | Tissue-culture batches → one plant per barcode (`RAH-ADF-000123`: nursery, plant's short code, number; the code is fixed when received) → grown on through the nursery's pot sizes → for sale. A product is a plant name + variegation; it has one SKU per sellable pot size (Shopify product + variants). Marking plants for sale creates them; prices are set after. Stock is counted from for-sale plants, never typed in |
| Plant names | Shared list (family → genus → species → cultivar, plus variegations), read-only; each nursery can add its own names on top |
| Photos | Phones upload straight to storage (Google Cloud Storage via its S3-compatible API; SeaweedFS locally) with short-lived signed links; private bucket, per-nursery paths. Plant photos form a growth timeline; a product's images are chosen from them |
| Pickup | Online orders are picked up from a locker in a slot the customer books in the cart: one locker × one date × one window. A booking covers that window only, so back-to-back windows can go to different customers. A window can be booked only if it starts at least 8 clock hours from now (any day), and staff must have the order in the locker by the start of its window ("ready by"), to give them time to prepare and clean the plants. For now there's one locker (up to 4 plants); bigger orders are a regular in-person pickup and are offered no locker slots. The locker has a manual PIN pad: Plant Stock generates a 4-digit code per order, staff set it when loading, and reset it for the next order |
| Pickup slot rules | Plant Stock owns them, not the Shopify theme: the windows per day, closed days, the 8-hour lead time, the 30-day booking limit, lockers, and bookings. A theme has no database (the date and window it saves belong to one shopper's cart), so only Plant Stock can know a slot is already taken and that two customers never get the same one. Each nursery manages them on one **pickup calendar**, open by default: the usual windows are set once, and staff close only the exceptions, a whole day or a single window (e.g. no morning pickup on Saturday). Every customer pickup shows on it too. The storefront offers only what that calendar has open and unbooked |
| Booking from the cart | The theme (`root_access_shopify_theme`) talks to Plant Stock through a Shopify App Proxy on the Plant Stock app: a URL on the store's own domain, so no CORS setup or keys in the theme, and Shopify signs each request so Plant Stock can check it. The cart loads every free slot for the next 30 days in one call. Picking one holds it for 30 minutes, refreshed when the shopper clicks Checkout and released when they change slot or empty the cart, with a limit on holds per cart. A refresh fails if the window now starts less than 8 hours away. The cart saves `Pickup date`, `Pickup window`, the hold ID, and an optional `Pickup note`; the imported order turns the hold into a booking, and unused holds expire. Plant Stock can't block Shopify's checkout itself on our plan (Shopify Functions in custom apps need Shopify Plus, to confirm in Shopify's docs), so holds make double-booking rare rather than impossible: if two orders still land in one slot, staff contact the customer. If Plant Stock is unreachable, the cart says pickup times are unavailable and blocks checkout |
| Customer emails | Sent by Shopify, from each nursery's own notification templates (Settings → Notifications), not by Plant Stock, which keeps no customer contact details. Plant Stock adds what the emails need to the Shopify order (e.g. the locker code as a metafield) and triggers them (e.g. marking an order ready for pickup sends Shopify's "Ready for local pickup" email). Emails to the nursery's team come from Plant Stock |
| Development | Work is tracked as GitHub issues; adding the `claude` label hands one to Claude in GitHub Actions, which opens a PR into `staging` (see README) |
| History | Every change to a plant (received, moved, for sale, discarded, corrected, photos) is recorded by Postgres triggers, with who by name. History can't be edited |

## Phases

### 1. Foundation
- [x] Scaffold: Go API (Gin) and React app, each with a production Dockerfile; Compose with Postgres, Valkey, and local stand-ins
- [x] Config, Postgres connection, migrations (pgx, sqlc, goose; migrations run at API startup)
- [x] Organizations, users, memberships, invitations; Clerk sign-in; tenant isolation (Row-Level Security); permissions
- [x] Platform admin: create/suspend nurseries, fix memberships, user lookup; no access to business data (API; admins listed in `PLATFORM_ADMIN_EMAILS`)
- [x] Web app: sign-in, accept invitations, nursery switcher, Team page, settings, Platform section
      (ported from the earlier .NET branch to the Go API)
- [x] CI: GitHub Actions running build and tests

### 2. Inventory: tissue culture → for sale
- [x] Plant names: shared list (281 names, 6 variegations); nurseries add their own
- [x] Pot sizes per nursery, each sellable or grow-on only (e.g. the Tissue Culture Cup)
- [x] Receive batches: one plant per barcode (`RAH-ADF-000123`), with QR labels on Letter paper (Avery 5160) or 50 × 30 mm rolls for the Niimbot M2: printed from the computer, straight over Bluetooth from Chrome, or exported as a spreadsheet for the Niimbot app
- [x] Grow on: repot whole batches or scanned plants; discard with a reason; correct a plant's or a batch's name or variegation; plant notes; edit batch details
- [x] Scan labels with the phone camera (or type / handheld scanner) and act on the scanned plants
- [x] Products and SKUs created by marking plants for sale; prices, title, description, product type, tags; live stock per SKU
- [x] Photos from phones straight to S3; growth timeline per plant; ordered product images chosen from plant photos
- [x] Plant history (audit log) recorded by Postgres triggers, with who by name
- [x] Dashboard: for-sale value, plants to mark for sale or price, growth pipeline by pot size
- [ ] Reports: profit per plant, sales over time (Owner only), once sales arrive (Phase 3)
- [x] Price and product change history: each SKU's price, SKUs added and removed, and title, description, type and tag edits, recorded by Postgres triggers with who by name; shown on each product
- [ ] Non-plant products (soil mix, merch) with stock counts
- [ ] The plant names still marked CHECK in `backend/internal/db/seed/plant_names.csv`
- [ ] Customers picking a specific plant from its photos

### 3. Shopify, per nursery
- [x] Shopify app with OAuth install, so each nursery connects its own store; tokens encrypted at rest
- [x] Background jobs: a `jobs` table the worker (`cmd/worker`) claims from (leases, retries with growing waits, dead after 5 failures); SQS at first, then Postgres alone
- [x] Push products, SKUs (variants), images, prices, and stock to Shopify; SKUs without a price stay off it
- [x] Orders arrive (checked each minute, plus webhooks once deployed) and hold specific plants until pickup; cancelled orders put them back on sale; Orders page lists what to pull
- [ ] Pickup slot on the order: the cart's `Pickup date`, `Pickup window`, hold ID, and optional `Pickup note` are stored with the imported order
- [ ] Slot rules: windows per day, the 8-hour lead time, the 30-day limit, lockers
- [ ] Pickup calendar (one per nursery), open by default: set the usual windows once, then close exceptions, a whole day or a single window; each customer pickup shows on its date and window; open a booking to move it to another slot. What the cart can book comes from this calendar
- [ ] Bookings and holds, with a database rule of at most one live hold or booking per locker slot (so two requests at the same moment can't both win)
- [ ] App Proxy in `shopify/shopify.app.<site>.toml`, with Shopify's signature checked on every request
- [ ] `GET pickup-slots`: every free slot for the next 30 days in one response, by date, in Houston time; `POST pickup-holds`: hold a slot (or say it was just taken), refresh, release; a refresh at Checkout re-checks the 8-hour lead time
- [ ] Turn holds into bookings when orders are imported; flag clashes and orders with no valid slot
- [ ] Expire holds after 30 minutes; free the slot when an order is cancelled or its window ends without pickup
- [ ] New-order email to team members who turned it on in their settings, with the order's "ready by" time: the start of its booked window (push notifications later)
- [x] Ready for pickup, then picked up: team members who prepare orders (permission `orders.prepare`: Owners, Admins, Staff) mark each step on the Orders page; picked up fulfills the order in Shopify (without emailing the customer), then sells its plants. Bigger orders can be marked picked up in person
- [ ] Prepare orders: sorted by "ready by" (the start of each booked window), with overdue ones flagged; staff scan the held plants to confirm them before they go in the locker
- [ ] Missed windows: once a window ends without pickup, its slot is freed for another customer and the customer who missed it is emailed to choose a new window; an order that's never collected is cancelled and its plants go straight back on sale
- [ ] Customers change their pickup window after ordering (the workflow is still being decided)
- [x] Picked up is final (no undo): a sale is only reversed by a return
- [x] Returns (Shopify Returns; needs the `read_returns` scope and a reconnect): a refund only once the plant is physically back (handed over in person, or left at the drop-off table), never for a photo. Return requests form a queue; staff scan the returned plant (which one, when an order had several), it waits for a check, then goes back on sale or is discarded; the refund is issued in Shopify within 1 day of receiving the plant, and the queue flags any past that
- [x] Refunds without a return (Refund clicked instead of Return, or money only) are flagged on the Returns tab; a special case (the customer keeps the plant) is allowed once an Owner approves it with a note saying why, and approvals stay listed
- [x] Cancelled orders (cancelled in Shopify) put their held plants straight back on sale (they never left)
- [x] Shopify webhooks (orders, refunds, app/uninstalled, privacy topics), set in the app's config (`shopify/shopify.app.staging.toml`) for staging; privacy requests to delete customer data clear customers' return notes
- [x] Nightly check (3 am, nursery time; or Check now) that finds and fixes drift between Plant Stock and Shopify: missing products, titles, SKUs, prices, stock

### 4. Lockers and customer emails (optional per nursery)
- [ ] Locker code: a 4-digit code per order, shown to staff and written onto the Shopify order; marking the order ready for pickup sends Shopify's "Ready for local pickup" email with the code, pickup time and locker instructions (#38)
- [ ] Team notifications: event-based emails to the nursery's team (e.g. order placed, return requested), each nursery choosing which events, who gets them, and the wording
- [ ] Locations (greenhouses, locker sites), each with its lockers and compartments
- [ ] More lockers: an order can use several, and a locker can hold more than one order
- [ ] Customers mark their order picked up (from the email)
- [ ] Locker temperature and humidity
- [ ] Thank-you email when an order is picked up
- [ ] Pickup reminders before the window
- [ ] One-hour slots instead of windows, with the locker held until the order is picked up, then released for the next booking
- [ ] Push notifications to staff for new orders

### 5. Growth (later)
- [ ] Working hours per week, if staffing needs them (no longer used for "ready by", which is now the start of the booked window)
- [ ] A final pickup-slot check inside Shopify's checkout, if the store moves to Shopify Plus or the app goes public
- [ ] Self-serve signup for nurseries, with trials and Stripe billing
- [ ] Permission settings per nursery: each nursery chooses which roles get which permissions (e.g. who approves refunds without a return, who prepares orders, who sees financials) instead of the fixed defaults; custom roles too (the permission model already supports it)
- [ ] 2FA, more sign-in options
- [ ] Hosted storefront with Stripe Connect payouts (much later)

### Deployment (Google Cloud; when ready)
- [x] Job queue in Postgres (no SQS), so production needs only the database and photo storage
- [x] Photos ready for Google Cloud Storage (its S3-compatible API with HMAC keys; checked by the storage test against SeaweedFS, which replaced LocalStack locally)
- [x] Create the private Cloud Storage bucket with CORS for the web app's origin (gcloud), and its HMAC key; run the storage test against it (project `plant-stock-rah`, bucket `plant-stock-rah-files` in us-central1 for photos and, later, other files such as PDFs and reports, service account `photos`)
- [x] Staging site: e2-micro VM `plant-stock-staging` (setup script: Docker, automatic security updates, firewall), the Compose stack, automatic HTTPS, `stock-staging.rootaccesshtx.com` pointed at it; its own bucket `plant-stock-rah-staging-files`; its own dev Shopify store (`root-access-staging`) through the "Plant Stock Staging" app; Clerk development instance shared with local development (redirects follow the site); email through Resend from `no-reply@rootaccesshtx.com` (domain verified with DKIM and SPF records in Shopify's DNS)
- [ ] Production site at launch: the same scripts (`deploy/create-vm.sh production`, `deploy/setup-backups.sh production`), `stock.rootaccesshtx.com`, bucket `plant-stock-rah-files`
- [x] Nightly database backup to Cloud Storage (07:00 UTC, kept 30 days; the VM may only add backups), and a tested restore (`deploy/restore-db.sh`); on staging, and on production through its setup script
- [x] Deploys from GitHub Actions for staging: each merge into `staging` builds the images on GitHub and loads them onto the VM (`.github/workflows/deploy-staging.yml`; Google sign-in without a stored key, `deploy/setup-github-deploy.sh`)
- [ ] The same for production on merges into `main`, once the production site exists
- [ ] Production Clerk instance on the domain; a production Resend API key (the domain is already verified)
- [ ] Shopify app version with the real URLs and webhook subscriptions; connect the real Root Access store
- [ ] Monitoring: an uptime check on the production site, and an alert when the nightly backup fails
- [ ] Apply for startup cloud credits (Google for Startups; AWS Activate and Microsoft for Startups as alternatives)
- [ ] Later, as nurseries join: managed Postgres, more machines or Kubernetes, a dedicated queue if volume calls for it
