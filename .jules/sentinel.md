## 2025-05-17 - XML Injection in OEmbed
**Vulnerability/Issue:** Found manual string concatenation used to build XML responses in the OEmbed endpoint (`internal/httpapi/handlers/admin/instance.go`). The code only escaped HTML characters in one field, leaving others (like Title) vulnerable to XML injection if they contained special characters like `<` or `>`.
**Resolution:** Replaced manual string concatenation with `encoding/xml` struct marshaling. Created `OEmbedResponse` struct with proper XML tags and used `xml.NewEncoder(w).Encode(resp)` to ensure all fields are automatically and correctly escaped.
**Learning:** Manual XML construction is error-prone and often leads to injection vulnerabilities. Using standard library encoders (`encoding/xml`) is safer as they handle escaping by default.
**Prevention:** Enforce use of `encoding/xml` for any XML output. Search for any other manual string concatenations building XML or HTML.

## 2025-05-20 - Insecure Default JWT Secret
**Vulnerability/Issue:** The application relied on a hardcoded, publicly known default JWT secret (`your-super-secret-jwt-key-change-in-production`) in `docker-compose.yml` and `.env.example`. If deployed without setting `JWT_SECRET`, the application would be vulnerable to token forgery.
**Resolution:** Modified `internal/config/config.go` to explicitly check if `JWT_SECRET` matches the known insecure default or if it is too short (<32 chars). In `production` environment, the application now refuses to start. In other environments, it logs a critical warning.
**Learning:** Default configuration values in code/examples often make their way to production. Relying on documentation ("change in production") is insufficient. "Fail Secure" (refusing to start) is better than "Fail Open" (running insecurely).
**Prevention:** Add startup checks for all critical security secrets (JWT keys, DB passwords, etc.) to ensure they are not using default or weak values.
