## 2025-05-17 - XML Injection in OEmbed
**Vulnerability/Issue:** Found manual string concatenation used to build XML responses in the OEmbed endpoint (`internal/httpapi/handlers/admin/instance.go`). The code only escaped HTML characters in one field, leaving others (like Title) vulnerable to XML injection if they contained special characters like `<` or `>`.
**Resolution:** Replaced manual string concatenation with `encoding/xml` struct marshaling. Created `OEmbedResponse` struct with proper XML tags and used `xml.NewEncoder(w).Encode(resp)` to ensure all fields are automatically and correctly escaped.
**Learning:** Manual XML construction is error-prone and often leads to injection vulnerabilities. Using standard library encoders (`encoding/xml`) is safer as they handle escaping by default.
**Prevention:** Enforce use of `encoding/xml` for any XML output. Search for any other manual string concatenations building XML or HTML.

## 2026-01-25 - Authenticated Privilege Escalation in User Creation
**Vulnerability/Issue:** The endpoint `POST /api/v1/users/` allowed any authenticated user to create a new user with any role, including "admin", because it trusted the `role` field in the request body without checking if the caller had administrative privileges.
**Resolution:** Added `middleware.RequireRole("admin")` to the route in `internal/httpapi/routes.go` to ensure only administrators can access this endpoint.
**Learning:** Endpoints that allow resource creation with privileged attributes (like roles) must explicitly verify the caller's permissions. Authentication alone (verifying identity) is not sufficient for authorization (verifying permissions).
**Prevention:** Review all `POST/PUT/DELETE` endpoints, especially those under `/api/v1/` or `/admin/`, to ensure they have appropriate `RequireRole` or similar authorization checks. Use specific security tests to verify access controls.
