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

## 2026-05-20 - Argument Injection in Command Wrappers
**Vulnerability/Issue:** The `yt-dlp` wrapper (`internal/importer/ytdlp.go`) passed user-supplied URLs directly to `exec.CommandContext` without the `--` delimiter. This allowed malicious input starting with `-` to be interpreted as command-line flags (argument injection), potentially enabling unexpected behavior or information disclosure.
**Resolution:** Inserted the `--` delimiter before the user-supplied URL argument in all `exec.CommandContext` calls. This forces the subsequent argument to be treated as a positional parameter (the URL) rather than a flag.
**Learning:** When wrapping external CLIs, always assume inputs can start with `-`. The double-dash `--` is a standard convention to terminate option parsing and treat remaining arguments as positional.
**Prevention:** Audit all usages of `exec.Command` and `exec.CommandContext`. Ensure that any user-controlled input is either strictly validated to not start with `-` or, preferably, preceded by `--` if the tool supports it.
