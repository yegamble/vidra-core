## 2025-05-17 - XML Injection in OEmbed
**Vulnerability/Issue:** Found manual string concatenation used to build XML responses in the OEmbed endpoint (`internal/httpapi/handlers/admin/instance.go`). The code only escaped HTML characters in one field, leaving others (like Title) vulnerable to XML injection if they contained special characters like `<` or `>`.
**Resolution:** Replaced manual string concatenation with `encoding/xml` struct marshaling. Created `OEmbedResponse` struct with proper XML tags and used `xml.NewEncoder(w).Encode(resp)` to ensure all fields are automatically and correctly escaped.
**Learning:** Manual XML construction is error-prone and often leads to injection vulnerabilities. Using standard library encoders (`encoding/xml`) is safer as they handle escaping by default.
**Prevention:** Enforce use of `encoding/xml` for any XML output. Search for any other manual string concatenations building XML or HTML.

## 2026-01-29 - Privilege Escalation in User Creation
**Vulnerability/Issue:** The `POST /api/v1/users/` endpoint allowed any authenticated user to create new users, including admin users, because it lacked role-based access control (RBAC). It only checked for authentication (`middleware.Auth`).
**Resolution:** Added `middleware.RequireRole("admin")` to the route definition in `internal/httpapi/routes.go`.
**Learning:** Routes intended for administrative tasks (like manual user creation) must explicitly enforce role checks. "Auth" middleware only confirms identity, not authority.
**Prevention:** Audit all routes under `internal/httpapi/routes.go` to ensure that sensitive actions (create, delete, update global configs) are protected by `RequireRole("admin")` or similar authorization logic.

## 2026-02-01 - Information Disclosure in Internal Metrics Endpoints
**Vulnerability/Issue:** Found several endpoints (`/api/v1/ipfs/metrics`, `/api/v1/ipfs/gateways`, `/api/v1/encoding/status`) that were accessible without authentication or with optional authentication, exposing internal system metrics and job counts. This could lead to information disclosure or potential Denial of Service (DoS) if abused.
**Resolution:** Added `middleware.Auth` and `middleware.RequireRole("admin")` to these routes in `internal/httpapi/routes.go`. Created a dedicated security test `internal/httpapi/endpoint_security_test.go` to verify access controls.
**Learning:** Internal operational endpoints (metrics, status, health of sub-components) often get overlooked during security reviews because they are "read-only". However, they can leak sensitive infrastructure details.
**Prevention:** Default to secure (deny all) for new route groups. Explicitly review all `OptionalAuth` usages to ensure they don't expose sensitive data to unauthenticated users.
