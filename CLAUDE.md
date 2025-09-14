# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Repository overview
- Monorepo-style project with Go backend and Vue 3 frontend
- Backend: Go 1.23+, Gin HTTP server, DI via uber/dig, GORM (SQLite/MySQL/Postgres), optional Redis cache; binary embeds built frontend under web/dist
- Frontend: Vite + Vue 3 + TypeScript + Naive UI; built assets consumed by backend via go:embed
- Containerization: Multi-stage Dockerfile builds frontend then backend; docker-compose for runtime
- CI: GitHub Actions to build and release binaries per-OS on tag push

Common commands
Backend (source):
- Run dev (race): make dev
- Run app with frontend build: make run
- CLI command (when built or via go run): gpt-load migrate-keys [--from old] [--to new]

Frontend (web/):
- Dev server: cd web && npm run dev
- Build: cd web && npm run build
- Lint: cd web && npm run lint:check
- Type check: cd web && npm run type-check

Docker:
- Build image: docker build -t gpt-load:local --build-arg VERSION=$(git describe --tags --always 2>/dev/null || echo 0.0.0) .
- Run container: docker run -p 3001:3001 -e AUTH_KEY=your-strong-key -v "$(pwd)/data":/app/data gpt-load:local
- Compose: docker compose up -d (ensure .env present)

Testing & linting
- Go tests are not present in repo; no go test targets found
- Frontend: eslint and vue-tsc are configured (see scripts above)

Configuration model
- Static env (read at startup): AUTH_KEY, ENCRYPTION_KEY, DATABASE_DSN, REDIS_DSN, server timeouts, logging, CORS, proxy env (HTTP_PROXY/HTTPS_PROXY/NO_PROXY)
- Dynamic, hot-reload settings stored in DB with precedence: Group > System > Env
- For cluster mode, set identical AUTH_KEY/DATABASE_DSN/REDIS_DSN across nodes; followers set IS_SLAVE=true

High-level architecture
- Entry: main.go decides server vs command mode; embeds web/dist and index.html
- DI Container: internal/container.BuildContainer wires components and provides buildFS/indexPage
- HTTP stack: Gin router with middleware (recovery, error handler, logging, CORS, rate limit, security headers)
- Routes:
  - /health
  - /api: public login; protected endpoints for groups, keys, tasks, dashboard, logs, settings (Auth via AUTH_KEY)
  - /proxy/:group_name/*path: authenticated by proxy keys; forwards to provider-specific channels
  - Static front-end served from embedded web/dist with gzip and cache middleware; SPA fallback serves embedded index.html
- Services:
  - Key pool: background validation, blacklist/restore, retry policy
  - Group manager: per-group upstream config, weighted load balancing
  - Request logging: buffered write to DB; cleanup cron
  - i18n middleware and locales
- Storage: GORM DB (SQLite/MySQL/Postgres); cache store via Redis or in-memory
- Shutdown: Graceful server shutdown honoring SERVER_GRACEFUL_SHUTDOWN_TIMEOUT; then stop background services

Provider channels
- internal/channel: openai_channel.go, gemini_channel.go, anthropic_channel.go implement provider-specific request/response handling behind a base channel and factory

Release/Versioning
- Build embeds version via -X gpt-load/internal/version.Version; Dockerfile ARG VERSION used for frontend VITE_VERSION and backend ldflags
- GitHub Actions build frontend and OS-specific binaries on tag push and draft a release

Local development workflow
- For UI changes: run npm run dev in web/, backend will still serve API; for integrated testing, make run to build front-end into embed and run server
- For backend changes: go run ./main.go or make dev; ensure .env is configured (AUTH_KEY at minimum). Access http://localhost:3001 and API at /proxy

Security notes specific to this repo
- Never commit real provider API keys; use proxy keys configured via UI or env
- If enabling encryption, keep ENCRYPTION_KEY safe; migrations available via migrate-keys command

Known ports and endpoints
- Default port 3001; health check at /health; SPA at /

Useful file references
- main entry: main.go
- router: internal/router/router.go
- app lifecycle: internal/app/app.go
- DI container: internal/container/container.go
- channels: internal/channel/*
- config manager: internal/config/*
- key pool: internal/keypool/*
- handlers: internal/handler/*
- store: internal/store/*

Notes for future Claude Code runs
- Run frontend type/lint checks before committing UI changes
- When building binaries/images, pass VERSION appropriately so UI and backend display consistent version
