## 2025-05-17 - XML Injection in OEmbed
**Vulnerability/Issue:** Found manual string concatenation used to build XML responses in the OEmbed endpoint (`internal/httpapi/handlers/admin/instance.go`). The code only escaped HTML characters in one field, leaving others (like Title) vulnerable to XML injection if they contained special characters like `<` or `>`.
**Resolution:** Replaced manual string concatenation with `encoding/xml` struct marshaling. Created `OEmbedResponse` struct with proper XML tags and used `xml.NewEncoder(w).Encode(resp)` to ensure all fields are automatically and correctly escaped.
**Learning:** Manual XML construction is error-prone and often leads to injection vulnerabilities. Using standard library encoders (`encoding/xml`) is safer as they handle escaping by default.
**Prevention:** Enforce use of `encoding/xml` for any XML output. Search for any other manual string concatenations building XML or HTML.

## 2025-05-18 - Insecure CORS Defaults and Invalid Configuration
**Vulnerability/Issue:** The CORS middleware hardcoded `Access-Control-Allow-Origin: *` while also setting `Access-Control-Allow-Credentials: true`. This combination is invalid (browsers reject it) and insecure if it were to work (allowing any site to make credentialed requests). It also ignored the `CORSAllowedOrigins` configuration.
**Resolution:** Refactored `internal/middleware/cors.go` to accept the configured allowed origins. Implemented logic to check the request's `Origin` header against the allowlist. If a wildcard `*` is configured, the middleware now reflects the request's `Origin` header to support credentials (while still insecure if configured with `*`, it is at least valid and configurable). Added `Vary: Origin` header.
**Learning:** Hardcoding security headers is dangerous. `Access-Control-Allow-Origin: *` must strictly NOT be combined with `Access-Control-Allow-Credentials: true`.
**Prevention:** Ensure all middleware respects application configuration. Use strict origin checking instead of wildcards for production.
