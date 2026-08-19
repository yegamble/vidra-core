# Postman / Newman collection

A curated Postman v2.1 collection + environment for exercising a **live**
`vidra-core` backend against the documented API contract. It is generated from
[`api/openapi.yaml`](../../api/openapi.yaml) and covers the system probes and the
auth happy/error paths with real assertions (status codes, response shape, and
token chaining), so it doubles as a fast end-to-end smoke of a running instance.

This is **documentation / manual QA tooling — not a required CI gate.** The
canonical automated gate is `make ci` (see the repo README).

## Files

| File | Purpose |
| --- | --- |
| `generate.mjs` | Zero-dependency generator (Node stdlib only). |
| `vidra-core.postman_collection.json` | The generated collection (System / Auth / Admin folders). |
| `vidra-core.postman_environment.json` | Environment with `baseUrl` (default `http://localhost:8080`). |

## Regenerate

```bash
make postman           # or: node docs/postman/generate.mjs
```

The generator fails if any request path has drifted out of `api/openapi.yaml`,
so the collection can never silently diverge from the documented contract.

## Run against a live backend (Newman)

Bring a backend up (`make up`, or the compose stack), then:

```bash
# Import the two JSON files into the Postman app, OR run headless with newman:
npx --yes newman run docs/postman/vidra-core.postman_collection.json \
  -e docs/postman/vidra-core.postman_environment.json

# Point at a different host/port (e.g. the compose API on :8088):
npx --yes newman run docs/postman/vidra-core.postman_collection.json \
  --env-var baseUrl=http://localhost:8088
```

The Auth folder registers a randomised account (safe to re-run against a
persistent DB), captures its bearer token, and reuses it for `GET /auth/me` and
`GET /admin/system`. Registered accounts are always plain users — the admin is
created only by the first-run owner claim (`POST /api/v1/setup/claim-owner`
with the boot-logged setup token). On a brand-new instance claim the owner
before running the collection: until then every register answers `403
owner_claim_required` and the Auth folder fails. The admin request asserts
`200` (when the presented token happens to belong to an admin) **or** `403`
(the normal outcome for the randomised registrant), so the run stays green
regardless of DB state; on `200` it also checks the effective `rate_limits` block.

## Full every-endpoint collection

This curated collection is intentionally small. For a collection covering *every*
documented operation, convert the spec directly:

```bash
npx --yes openapi-to-postmanv2 -s api/openapi.yaml \
  -o docs/postman/vidra-core.full.postman_collection.json -p
```
