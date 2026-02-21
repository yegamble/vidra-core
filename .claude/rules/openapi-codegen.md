## OpenAPI Code Generation

Generated types and server interfaces live in `internal/generated/`. **Never edit these files directly** — they are regenerated from `api/*.yaml` specs.

### Workflow

```bash
# Edit the spec
vim api/openapi.yaml          # Or api/openapi_payments.yaml, etc.

# Regenerate
make generate-openapi         # Reads oapi-codegen.yaml, writes to internal/generated/

# Verify no drift (run in CI)
make verify-openapi           # Fails if generated code doesn't match spec
```

### Config (`oapi-codegen.yaml`)

```yaml
package: generated
output: internal/generated
generate:
  models: true
  chi-server: true
  client: true
```

### Multiple spec files

`api/` contains one YAML per domain area:

```
api/openapi.yaml              # Core (auth, users, videos)
api/openapi_payments.yaml     # IOTA payments
api/openapi_livestreaming.yaml
api/openapi_analytics.yaml
# ... etc.
```

All specs feed into the same `internal/generated/` output.

### When adding a new endpoint

1. Add operation to the appropriate `api/openapi_*.yaml`
2. Run `make generate-openapi`
3. Implement the new `ServerInterface` method in `internal/httpapi/handlers/`
4. Run `make verify-openapi` to confirm no drift
