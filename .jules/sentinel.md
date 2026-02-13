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

## 2026-01-31 - SQL Injection in Video Search
**Vulnerability/Issue:** Found SQL injection in `internal/repository/video_repository.go` where user input (`req.Query`) was directly concatenated into the `ORDER BY` clause when sorting by "relevance".
**Resolution:** Replaced string concatenation with parameter binding (`plainto_tsquery('english', $X)`). Updated `countQuery` execution logic to correctly account for the extra argument added for sorting. Added regression test `internal/repository/video_repository_security_test.go`.
**Learning:** Even when using parameterized queries for `WHERE` clauses, `ORDER BY` clauses built dynamically can be vulnerable if they include user input in function arguments or expressions.
**Prevention:** Always use placeholders for any user input, even in complex expressions like `ts_rank`. Avoid manual string building for SQL wherever possible.
