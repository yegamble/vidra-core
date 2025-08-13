Postman: Athena Auth API

Overview
- Collection: `postman/athena-auth.postman_collection.json`
- Environment (template): `postman/athena.local.postman_environment.json`

Usage
- Import the collection and the environment into Postman.
- Select the `Athena Local` environment. Ensure `baseUrl` points to your running server (default `http://localhost:8080`).
- Run requests in order for a happy-path flow:
  1) Register
  2) Login
  3) Refresh
  4) Logout
  5) Refresh After Logout (expects 401)
- Negative cases are provided: Invalid Login (401), Refresh Missing Token (400), Refresh Invalid Token (401), Logout Without Token (401).

Notes
- Register pre-request script auto-generates a unique `username` and `email` if not set.
- Tests capture `access_token`, `refresh_token`, and `user_id` into environment variables for reuse.
- Logout uses `Authorization: Bearer {{access_token}}`.

Run with Newman
- Prereqs: Docker and docker-compose installed.
- Quick run against an already-running server:
  - `make postman-newman` (uses `BASE_URL=http://localhost:8080` by default)
  - Override base URL: `make postman-newman BASE_URL=http://localhost:18080`
- End-to-end spin-up + run + teardown:
  - `make postman-e2e` (starts Postgres/Redis/app via `docker-compose.test.yml`, runs Newman, then tears down)
- JUnit results written to `postman/newman-results.xml` for CI consumption.
