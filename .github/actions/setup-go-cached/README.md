# Setup Go with Optimal Caching

A composite GitHub Action that sets up Go with optimized module and build caching for self-hosted runners, with configurable GOPROXY fallback support for restricted network environments.

## Features

- ✅ Sets up Go with specified version
- ✅ Optimized caching for Go modules and build artifacts
- ✅ Supports both bare-metal and containerized runners
- ✅ Configurable GOPROXY fallback (non-invasive)
- ✅ Respects existing GOPROXY configuration
- ✅ Works in corporate/restricted network environments

## Usage

### Basic Usage

```yaml
- uses: ./.github/actions/setup-go-cached
```

### Specify Go Version

```yaml
- uses: ./.github/actions/setup-go-cached
  with:
    go-version: '1.24'
```

### Custom Cache Dependency Path

```yaml
- uses: ./.github/actions/setup-go-cached
  with:
    cache-dependency-path: '**/go.sum'
```

### Configure GOPROXY Fallback

```yaml
# Use default fallback (goproxy.io)
- uses: ./.github/actions/setup-go-cached
  with:
    goproxy-fallback: 'https://goproxy.io,direct'

# Use corporate internal proxy
- uses: ./.github/actions/setup-go-cached
  with:
    goproxy-fallback: 'https://internal-proxy.corp.com,direct'

# Disable fallback (use Go defaults only)
- uses: ./.github/actions/setup-go-cached
  with:
    goproxy-fallback: ''
```

### Force Specific GOPROXY (Highest Priority)

```yaml
- uses: ./.github/actions/setup-go-cached
  env:
    GOPROXY: 'https://my-proxy.example.com,direct'
```

## Inputs

| Input | Description | Required | Default |
|-------|-------------|----------|---------|
| `go-version` | Go version to install | No | `1.24` |
| `cache-dependency-path` | Path to go.sum for cache key | No | `go.sum` |
| `goproxy-fallback` | Fallback GOPROXY if not already set | No | `https://goproxy.io,direct` |

## GOPROXY Configuration Behavior

The action respects the following priority order:

1. **Existing `GOPROXY` environment variable** (highest priority)
   - If already set, the action uses it without modification
   - Set via workflow `env:` or job-level configuration

2. **Fallback via `goproxy-fallback` input**
   - Applied only if `GOPROXY` is not already set
   - Default: `https://goproxy.io,direct`
   - Can be customized or disabled (set to empty string)

3. **Go defaults**
   - Used if both above are unset/empty
   - Default: `https://proxy.golang.org,direct`

### Why GOPROXY Fallback?

The default Go proxy (`proxy.golang.org`) redirects to Google Cloud Storage (`storage.googleapis.com`), which may:
- Be blocked in corporate networks
- Experience DNS resolution issues in certain CI environments
- Be unavailable in restricted network zones

The fallback ensures reliable module downloads while respecting organizational requirements.

## Examples

### Corporate Environment with Internal Proxy

```yaml
jobs:
  build:
    runs-on: ubuntu-latest
    env:
      # Force all Go builds to use corporate proxy
      GOPROXY: 'https://nexus.corp.com/repository/go-proxy,direct'

    steps:
      - uses: actions/checkout@v4

      - uses: ./.github/actions/setup-go-cached
        # GOPROXY env var takes precedence, goproxy-fallback is ignored

      - run: go build ./...
```

### Multi-Environment Workflow

```yaml
jobs:
  build-internal:
    runs-on: self-hosted
    env:
      GOPROXY: 'https://internal-proxy.corp.com,direct'
    steps:
      - uses: ./.github/actions/setup-go-cached

  build-external:
    runs-on: ubuntu-latest
    # No GOPROXY set, uses goproxy-fallback default
    steps:
      - uses: ./.github/actions/setup-go-cached
```

### Air-Gapped Environment

```yaml
jobs:
  build:
    runs-on: self-hosted
    steps:
      - uses: ./.github/actions/setup-go-cached
        with:
          # Disable all proxies, assume modules are vendored
          goproxy-fallback: ''

      - run: go build -mod=vendor ./...
```

## Best Practices

### 1. Version Pinning

```yaml
- uses: ./.github/actions/setup-go-cached
  with:
    go-version: '1.24.0'  # Pin exact version for reproducibility
```

### 2. Cache Key Optimization

```yaml
- uses: ./.github/actions/setup-go-cached
  with:
    cache-dependency-path: |
      **/go.sum
      **/go.mod
```

### 3. Matrix Builds

```yaml
strategy:
  matrix:
    go-version: ['1.23', '1.24']

steps:
  - uses: ./.github/actions/setup-go-cached
    with:
      go-version: ${{ matrix.go-version }}
```

### 4. Environment-Specific Configuration

```yaml
- uses: ./.github/actions/setup-go-cached
  with:
    goproxy-fallback: ${{ secrets.GOPROXY_URL || 'https://goproxy.io,direct' }}
```

## Troubleshooting

### Module Download Failures

If you see errors like:
```
go: downloading ... failed: dial tcp: lookup storage.googleapis.com: no such host
```

**Solutions:**
1. Set `GOPROXY` environment variable to use a different proxy
2. Configure `goproxy-fallback` input to use an accessible proxy
3. Use vendored modules (`go mod vendor` + `go build -mod=vendor`)

### Corporate Proxy Issues

If your organization requires a specific proxy:

```yaml
env:
  GOPROXY: 'https://your-proxy.corp.com,direct'
  # Also set these if needed
  GONOPROXY: 'private.corp.com/*'
  GOPRIVATE: 'private.corp.com/*'
```

### Cache Not Working

Verify:
1. `go.sum` file exists and is committed
2. `cache-dependency-path` points to correct location
3. Runner has sufficient disk space

### GOPROXY Not Applied

Check priority:
1. Is `GOPROXY` env var already set? (takes precedence)
2. Is `goproxy-fallback` input empty? (disables fallback)
3. Check action logs for "Using existing GOPROXY" message

## Technical Details

### Cache Locations

The action configures caching for:
- `GOMODCACHE`: Go module cache (typically `~/go/pkg/mod`)
- `GOCACHE`: Go build cache (typically `~/.cache/go-build`)

### Environment Detection

The action automatically detects:
- Container vs bare-metal execution
- Available cache directories
- Existing GOPROXY configuration

### Error Handling

- Uses `set -euo pipefail` for strict error handling
- Validates environment variables before use
- Provides informative error messages
- Fails fast on configuration issues

## Contributing

When modifying this action:

1. **Test in multiple environments:**
   - Bare-metal runners
   - Containerized runners
   - Corporate networks
   - Air-gapped environments

2. **Maintain backward compatibility:**
   - Don't change default behaviors
   - Add new inputs instead of modifying existing

3. **Document changes:**
   - Update this README
   - Update action.yml descriptions
   - Add examples for new features

4. **Follow best practices:**
   - Use `set -euo pipefail` in scripts
   - Validate all inputs
   - Provide clear error messages
   - Respect existing configuration

## License

This action is part of the Athena project and follows the same license.

## See Also

- [Go Environment Variables](https://go.dev/ref/mod#environment-variables)
- [Module Proxies](https://go.dev/ref/mod#module-proxy)
- [GitHub Actions: Creating Composite Actions](https://docs.github.com/en/actions/creating-actions/creating-a-composite-action)
