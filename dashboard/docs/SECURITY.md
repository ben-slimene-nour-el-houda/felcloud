# Security notes

- **No secrets in `VITE_*`.** Everything with that prefix ships in the JavaScript bundle.
- **Bearer token** (optional) is entered by the operator and stored in `sessionStorage` for the tab only.
  `sessionStorage` is readable by any script on the page, so an XSS bug would expose it. For production,
  prefer an HTTP-only, `SameSite` session cookie issued by a same-origin reverse proxy; the client already
  uses `credentials: 'same-origin'`.
- **Tokens are never logged** and never placed in URLs. Error text from the server is sanitised: bearer
  tokens, `token=`/`password=`-style pairs and JWT-shaped strings are redacted, stack traces are replaced
  with a generic message, and messages are length-limited.
- **No `dangerouslySetInnerHTML`.** All server text is rendered as text.
- **External links** (benchmark evidence) use `rel="noreferrer noopener"`.
- **Mutations** are never auto-retried; an `Idempotency-Key` accompanies each so a deliberate retry is safe
  when the backend honours it.
- **Authorisation is the backend's job.** Hiding or disabling a button is a convenience, not a control.
  The API must enforce roles for failover, rollback, teardown and disruptive benchmarks.
- **CORS / CSP**: prefer same-origin hosting. Suggested CSP: `default-src 'self'; style-src 'self'; img-src 'self' data:`.
- **Dependencies**: run `npm audit` and keep the lockfile committed.
