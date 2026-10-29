# FelCloud Dashboard

Operator console for **CSTAM-FELCLOUD — The Resilient IP Optimizer** (team **CloudPulse**).
It is the control surface for a platform that provisions isolated team sandboxes, manages private IP
leases, monitors a pair of redundant gateways (HAProxy + Keepalived/VRRP), operates routing
configuration, and exposes events, logs and benchmark evidence.

> The dashboard is a frontend for a **compatible API**. The intended architecture (Python control plane,
> OpenStack, transactional IPAM, gateway pair) is the product goal, not proof that a deployed backend
> already implements every operation. See [docs/IMPLEMENTATION_STATUS.md](docs/IMPLEMENTATION_STATUS.md).

## Pages

| Page                  | Purpose                                                                                                  |
| --------------------- | -------------------------------------------------------------------------------------------------------- |
| Overview              | Is the platform healthy? Four metrics, gateway topology, items needing attention, recent activity        |
| Sandboxes             | Search/filter/sort/paginate, provision (async jobs), detail drawer, confirmed teardown                   |
| IP Address Management | Pool statistics, lease table, lifecycle explanation, reconciliation                                      |
| Gateways              | Two-node topology, VIP owner, per-service health, confirmed failover with verification, failover history |
| Traffic & Routes      | Routes with health, rollout stage (configured / validated / deployed / reachable), evidence              |
| Configuration         | Generations, validate and deploy as separate steps, per-gateway state, diff, rollback                    |
| Observability         | Events, HAProxy access logs, Keepalived/system logs, operation history                                   |
| Benchmarks            | Backend-supported suites, run form, history, comparison (only when comparable), CSV/JSON export          |

## Prerequisites

- Node.js 20.19+ (developed on Node 22) and npm 10+

## Setup

```bash
npm install
cp .env.example .env     # optional; defaults work with the dev proxy
npm run dev              # http://localhost:5173
```

## Scripts

| Command                           | What it does                                                |
| --------------------------------- | ----------------------------------------------------------- |
| `npm run dev`                     | Vite dev server (proxies `/api` to `http://localhost:8000`) |
| `npm run build`                   | Typecheck, then production build into `dist/`               |
| `npm run preview`                 | Serve the production build locally                          |
| `npm run lint`                    | ESLint                                                      |
| `npm run typecheck`               | `tsc --noEmit` (strict)                                     |
| `npm run test`                    | Vitest + React Testing Library                              |
| `npm run format` / `format:check` | Prettier                                                    |

## Environment variables

All `VITE_*` variables are bundled into browser code and are **public**. Never put secrets in them.

| Variable                 | Default                 | Meaning                               |
| ------------------------ | ----------------------- | ------------------------------------- |
| `VITE_API_BASE_URL`      | `/api`                  | Base URL of the control-plane API     |
| `VITE_DEMO_MODE`         | `false`                 | `true` enables the built-in simulator |
| `VITE_API_TIMEOUT_MS`    | `10000`                 | Per-request timeout                   |
| `VITE_ENVIRONMENT_LABEL` | `Development`           | Display label in the sidebar          |
| `VITE_DEV_PROXY_TARGET`  | `http://localhost:8000` | Dev server only; set in the shell     |

## Connecting to the Python API

1. Run your control plane (default `http://localhost:8000`) exposing the endpoints in
   [docs/API_CONTRACT.md](docs/API_CONTRACT.md) under `/api`.
2. `npm run dev` — the Vite proxy forwards `/api/*`, so no CORS setup is needed in development.
3. In production serve the built files and `/api` from the same origin (reverse proxy), or set
   `VITE_API_BASE_URL` to the API origin and enable CORS for it.
4. If the API needs a bearer token, use the account button in the top bar. The token is kept in
   `sessionStorage` for the tab only (see [docs/SECURITY.md](docs/SECURITY.md)).

The API's `GET /health` response advertises **capabilities**. Any action the API does not advertise is
shown disabled with an explanation instead of a fake success.

## Demo mode

```bash
VITE_DEMO_MODE=true npm run dev
```

A persistent banner reads **DEMO DATA — NOT CONNECTED TO INFRASTRUCTURE**. Data is deterministic
(`src/demo/data.ts`) and actions run through a local simulator (`src/demo/simulator.ts`); every
operation is labelled **Simulated**. Provisioning a sandbox whose name contains `fail` demonstrates a
failed job. Demo mode is **never** enabled automatically after a failed request, and the simulator is
loaded lazily so live builds do not run it.

## Honesty rules the UI follows

- No random metrics, no fabricated history, no hard-coded gateway roles, no timer-driven "success".
- Async operations (HTTP 202) are polled through `GET /jobs/{id}`; progress shows backend-reported steps only.
- A job finishing is not enough: failover, deploy, rollback, provisioning and teardown are **verified**
  by re-reading state, and "unverified" is shown when verification does not confirm the result.
- Missing data reads "Not reported by API", never a guessed value.

## ZIP contents

```
felcloud-dashboard/
  package.json, package-lock.json, index.html, vite.config.ts, tsconfig.json
  eslint.config.js, .prettierrc, .prettierignore, .gitignore, .env.example, README.md
  public/    favicon
  src/       api/ (client, contracts, errors, endpoints/), demo/, components/, pages/, hooks/,
             operations/, lib/, styles/
  tests/     Vitest suites and helpers
  docs/      ARCHITECTURE, API_CONTRACT, SECURITY, TESTING, IMPLEMENTATION_STATUS
```

Excluded: `node_modules/`, `dist/`, `.git/`, real `.env` files and caches.

## Current limitations

- Endpoints in `docs/API_CONTRACT.md` are a **proposed** contract; align them with your real OpenAPI spec.
- Responsive layouts and the accessibility behaviour were implemented to the specification, but were
  **not** checked in a real browser in the build environment (see `docs/TESTING.md`).
- Light theme only. Authentication is a bearer token; a login flow is not included.
