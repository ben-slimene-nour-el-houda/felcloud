# Architecture

## Layers

```
pages/  ──►  hooks/queries + operations/  ──►  Api interface  ──►  live client (HTTP)  |  demo simulator
 UI           server state + async jobs        api/contracts.ts      api/client.ts          demo/simulator.ts
```

- **`src/api/contracts.ts`** — Zod schemas and the `Api` interface. Every response is validated; a
  mismatch becomes an `invalid_response` error, never a success.
- **`src/api/client.ts`** — one fetch wrapper: base URL, timeout, cancellation, optional bearer token,
  error normalisation, no automatic retries for mutations, `Idempotency-Key` header, 202 handling.
- **`src/api/endpoints/*`** — thin per-area functions built on the client.
- **`src/api/index.ts`** — `createApi(config)` picks live or demo **from explicit configuration only**; the
  simulator is dynamically imported so live mode never executes it.
- **`src/demo/`** — fixtures and a simulator implementing the same `Api`. Isolated from live code paths.
- **`src/hooks/queries.ts`** — TanStack Query hooks plus `useCapability` (drives disabled-with-reason actions).
- **`src/operations/OperationsProvider.tsx`** — tracks every mutation: start → (202) poll job → verify →
  toast. Job IDs are persisted in `sessionStorage` so state survives a page reload; a vanished job is shown
  as "Outcome unknown".
- **`src/components/`** — AppShell (Sidebar, TopBar, MobileNavigation), Modal/ConfirmDialog/DetailPanel
  (focus trap, Escape, focus restore, background made inert), DataTable (table → record cards on mobile),
  QueryView (loading / error / stale / empty kept distinct), GatewayTopology, LeaseLifecycle, and more.

## Key decisions

- **Plain CSS with design tokens** (`src/styles/index.css`), one styling system, no UI framework.
- **Capabilities in `/health`** let the console disable unsupported actions with an explanation.
- **Verification after jobs**: success is reported only when authoritative state confirms it.
- **Server-side filtering/pagination**: pages send `q`, `status`, `sort`, `page`, `page_size`.
- **Polling, not streaming**: jobs and health are polled; an event stream can be added behind the same API.

## Environment modes

|                    | Live                                        | Demo                   |
| ------------------ | ------------------------------------------- | ---------------------- |
| Selected by        | default                                     | `VITE_DEMO_MODE=true`  |
| Data source        | HTTP API                                    | `src/demo/data.ts`     |
| Banner             | none                                        | persistent demo banner |
| Operation outcomes | real                                        | labelled "Simulated"   |
| API down           | explicit warning, last-known data timestamp | n/a                    |
