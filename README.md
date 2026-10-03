# Root Access Inventory

Private plant inventory app for Root Access HTX: tracks each plant from growing to sold,
syncs listings to Shopify, and reports profit per plant.

## Stack
- **API:** ASP.NET Core (.NET 10), controllers, EF Core + Npgsql — `backend/RootAccess.Inventory.Api`
- **Tests:** xUnit — `backend/RootAccess.Inventory.Tests`
- **Frontend:** React + TypeScript + Vite — `frontend/`
- **Database:** PostgreSQL 17 (docker-compose, host port 5433)

## Local development
```sh
docker compose up -d                                         # Postgres on localhost:5433
dotnet run --project backend/RootAccess.Inventory.Api --launch-profile http   # API on :5030
cd frontend && npm install && npm run dev                    # Vite on :5173, proxies /api to :5030
```
Open http://localhost:5173. Health check: `GET /api/health`.

Run tests: `dotnet test RootAccess.Inventory.slnx`
