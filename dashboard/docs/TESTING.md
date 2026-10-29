# Testing

```bash
npm test          # Vitest + React Testing Library (jsdom)
npm run lint && npm run typecheck && npm run build
```

Tests mock **only at the API boundary** (`tests/helpers.tsx#fakeApi`) or inject a `fetch` into the live
client. Demo fixtures are used as a source of well-formed responses and never reach live code paths.

| Spec item                                                                                                                                 | Test                                      |
| ----------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------- |
| Navigation, mobile nav a11y, banner only in demo, no silent fallback, API-down state, empty vs failure                                    | `tests/app-shell.test.tsx`                |
| Form validation, duplicate-submit guard, 202 lifecycle, failed ≠ active, teardown confirmation, search/status filters, state after reload | `tests/sandboxes.test.tsx`                |
| Lease labels and counts, lease filter                                                                                                     | `tests/ipam-gateways.test.tsx`            |
| Gateway role from API, failover verified only after check, disabled-with-reason                                                           | `tests/ipam-gateways.test.tsx`            |
| Validation ≠ deployment, partial deployment, "Not reported by API", rollback confirmation                                                 | `tests/configuration-benchmarks.test.tsx` |
| Benchmarks rendered as returned, unavailable state, non-comparable runs, CSV                                                              | `tests/configuration-benchmarks.test.tsx` |
| Error sanitisation, invalid response, 202 vs 201, no mutation retry, bearer only when configured                                          | `tests/api-client.test.ts`                |

## Not covered by automated tests

- Real-browser layout at 360/390/768/1024/1440/1920 px, touch-target size and contrast measurement.
  Check these manually (or add Playwright) before release.
- Screen-reader announcement quality (live regions are present but were not tested with a screen reader).
- Behaviour against a real backend.
