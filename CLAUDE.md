# CLAUDE.md

## Git workflow

- `staging` is the integration branch; `main` is production.
- Never push directly to `staging` or `main` — all changes go through PRs.
- Create branches as `feature/<short-name>` off `staging`.
- PRs target `staging`. Only `staging` gets merged into `main`.
