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

## 2026-02-02 - Argument Injection in yt-dlp Wrapper
**Vulnerability/Issue:** The `yt-dlp` wrapper in `internal/importer/ytdlp.go` passed user-provided URLs directly as arguments to `exec.CommandContext` without the `--` delimiter. Although validation prevented flags starting with `-`, a parser bypass or malformed URL could theoretically be interpreted as a flag, leading to argument injection.
**Resolution:** Added `--` delimiter before the URL argument in all `yt-dlp` command invocations (`ValidateURL`, `ExtractMetadata`, `Download`, `DownloadThumbnail`) to explicitly mark the end of options.
**Learning:** Always use `--` to separate options from positional arguments when invoking external commands with user input, even if the input is validated. Validations can be bypassed or might be insufficient for all edge cases (e.g. parser quirks).
**Prevention:** Enforce the use of `--` in all `exec.Command` calls that pass user input as arguments, especially for tools like `ffmpeg`, `yt-dlp`, etc. Add regression tests that verify the command structure.
