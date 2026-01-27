## 2025-05-17 - XML Injection in OEmbed
**Vulnerability/Issue:** Found manual string concatenation used to build XML responses in the OEmbed endpoint (`internal/httpapi/handlers/admin/instance.go`). The code only escaped HTML characters in one field, leaving others (like Title) vulnerable to XML injection if they contained special characters like `<` or `>`.
**Resolution:** Replaced manual string concatenation with `encoding/xml` struct marshaling. Created `OEmbedResponse` struct with proper XML tags and used `xml.NewEncoder(w).Encode(resp)` to ensure all fields are automatically and correctly escaped.
**Learning:** Manual XML construction is error-prone and often leads to injection vulnerabilities. Using standard library encoders (`encoding/xml`) is safer as they handle escaping by default.
**Prevention:** Enforce use of `encoding/xml` for any XML output. Search for any other manual string concatenations building XML or HTML.

## 2025-05-17 - Unbounded Request Body Read in Uploads
**Vulnerability/Issue:** Identified potential Denial of Service (DoS) via memory exhaustion in `UploadChunkHandler` and `VideoUploadChunkHandler`. The handlers used `io.ReadAll(r.Body)` without any size limit, allowing an attacker to send an extremely large request body and crash the server with OOM.
**Resolution:** Implemented `http.MaxBytesReader` to strictly limit the request body size to the configured `ChunkSize` + 1KB buffer. Also updated `InitiateUploadHandler` to validate requested chunk sizes against the server configuration and handle zero-value configs safely.
**Learning:** Always assume input streams (like `r.Body`) are infinite or malicious. `io.ReadAll` is dangerous on untrusted input.
**Prevention:** Enforce `http.MaxBytesReader` on any handler that reads the full request body. Ensure configuration values (like `ChunkSize`) have safe defaults and valid ranges.
