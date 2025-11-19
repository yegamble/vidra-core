# GOPROXY Configuration Fix: Environment Variable vs Config File

## Problem Statement

The CI/CD pipeline was failing with the following error in `.github/actions/setup-go-cached/action.yml`:

```
go: cannot find go env config: neither $XDG_CONFIG_HOME nor $HOME are defined
Error: Process completed with exit code 1.
```

This occurred at lines 199-207 where we attempted to configure GOPROXY using `go env -w`.

## Root Cause Analysis

### Why `go env -w` Failed

The `go env -w` command writes persistent configuration to a file, which requires either:
- `$HOME` to be defined (config location: `~/.config/go/env`)
- `$XDG_CONFIG_HOME` to be defined (config location: `$XDG_CONFIG_HOME/go/env`)

In GitHub Actions composite actions, particularly in certain execution contexts or containers, these environment variables may not be set when the step executes, causing `go env -w` to fail.

### Why This Was Unnecessary

The `go env -w` command was **redundant** for our use case because:

1. **Environment Variable Precedence**: Go reads environment variables before checking config files
2. **Job Scope**: Each GitHub Actions job starts with a fresh environment - persistent config provides no benefit
3. **Already Set**: We were already setting `GOPROXY` via `$GITHUB_ENV` on the previous line
4. **No State Needed**: We don't need configuration to persist between jobs

## Solution

### Changes Made

**File**: `.github/actions/setup-go-cached/action.yml`

**Before** (lines 199-207):
```yaml
    # Configure Go proxy to avoid storage.googleapis.com DNS issues
    - name: Configure Go proxy
      shell: bash
      run: |
        set -euo pipefail
        echo "Configuring GOPROXY to use goproxy.io as primary proxy..."
        echo "GOPROXY=https://goproxy.io,direct" >> "$GITHUB_ENV"
        go env -w GOPROXY=https://goproxy.io,direct  # ❌ FAILS without $HOME
        echo "✓ GOPROXY=$(go env GOPROXY)"
```

**After**:
```yaml
    # Configure Go proxy to avoid storage.googleapis.com DNS issues
    - name: Configure Go proxy
      shell: bash
      run: |
        set -euo pipefail
        echo "Configuring GOPROXY to use goproxy.io as primary proxy..."
        echo "GOPROXY=https://goproxy.io,direct" >> "$GITHUB_ENV"
        echo "✓ GOPROXY set to: https://goproxy.io,direct"
```

**Additional Verification** (lines 208-215):
Added verification in the next step to confirm GOPROXY is active:
```yaml
    # Ensure module files are tidy and dependencies are downloadable
    - name: Tidy and download modules
      shell: bash
      run: |
        set -euo pipefail

        # Verify GOPROXY is set (should be set by previous step via $GITHUB_ENV)
        echo "Active GOPROXY: $(go env GOPROXY)"

        # ... rest of step
```

### How It Works

1. **Setting via `$GITHUB_ENV`**: Writing to `$GITHUB_ENV` makes the variable available to all subsequent steps in the job
2. **Environment Variable Precedence**: When Go commands run, they read `GOPROXY` from the environment first
3. **No Config File Needed**: Since the environment variable is set, Go never needs to consult its config file
4. **Works Everywhere**: This approach works regardless of whether `$HOME` is set

## Verification

### Testing Methodology

Created a test script that demonstrates:
1. `go env -w` fails without `$HOME` (original problem)
2. Setting `GOPROXY` as environment variable works without `$HOME`
3. Go actually uses the proxy from the environment variable

### Test Results

```bash
Test 1: Demonstrating 'go env -w' fails without HOME
-------------------------------------------------------
HOME is now: UNSET
Attempting 'go env -w GOPROXY=https://goproxy.io,direct'...
go: cannot find go env config: neither $XDG_CONFIG_HOME nor $HOME are defined
Expected result: go env -w failed without HOME ✓

Test 2: Demonstrating environment variable works without HOME
----------------------------------------------------------------
HOME is now: UNSET
Set GOPROXY environment variable to: https://goproxy.io,direct
Checking 'go env GOPROXY'...
Result: https://goproxy.io,direct
Success: GOPROXY correctly set via environment variable ✓

Test 3: Verify Go respects GOPROXY environment variable
--------------------------------------------------------
GOPROXY=https://goproxy.io,direct
Testing with a small module download (dry-run)...
Running 'go mod download -x' to see proxy usage...
# get https://goproxy.io/github.com/google/uuid/@v/v1.3.0.mod
# get https://goproxy.io/github.com/google/uuid/@v/v1.3.0.mod: 200 OK (0.518s)
✓ Module download uses configured GOPROXY
```

## Technical Details

### Go Environment Variable Resolution Order

Go resolves `GOPROXY` in the following order:
1. **Environment variable** `GOPROXY` (highest priority)
2. **Config file** (`~/.config/go/env` or `$XDG_CONFIG_HOME/go/env`)
3. **Default value** (`https://proxy.golang.org,direct`)

Since we set the environment variable, steps 2 and 3 are never needed.

### GitHub Actions `$GITHUB_ENV` Mechanism

When you write to `$GITHUB_ENV`:
```bash
echo "GOPROXY=https://goproxy.io,direct" >> "$GITHUB_ENV"
```

GitHub Actions:
1. Appends the line to a special file
2. Processes this file after the step completes
3. Exports the variable to the environment of all subsequent steps
4. The variable persists for the entire job duration

## Impact Assessment

### Workflows Affected
This fix improves reliability for all workflows using the `setup-go-cached` action:
- `.github/workflows/e2e-tests.yml`
- `.github/workflows/goose-migrate.yml`
- `.github/workflows/security-tests.yml`
- `.github/workflows/test.yml`
- `.github/workflows/video-import.yml`
- `.github/workflows/virus-scanner-tests.yml`

### Benefits
1. **Reliability**: No longer dependent on `$HOME` being set
2. **Simplicity**: Fewer commands, clearer intent
3. **Performance**: Slightly faster (one fewer command)
4. **Portability**: Works in containers, VMs, bare metal, etc.
5. **Clarity**: More obvious what's happening

### Risks
None. The new approach is strictly better:
- More reliable (fewer dependencies)
- Same functionality (Go still uses goproxy.io)
- Better error handling (no silent failures)

## Why Use goproxy.io?

We configure GOPROXY to use `https://goproxy.io,direct` to avoid DNS issues with the default proxy:
- Default: `https://proxy.golang.org,direct` → redirects to `storage.googleapis.com`
- Problem: DNS resolution issues with `storage.googleapis.com` in certain CI environments
- Solution: `https://goproxy.io,direct` → reliable alternative proxy
- Fallback: `,direct` means if proxy fails, fetch directly from source

## Related Documentation

- [Go Environment Variables](https://pkg.go.dev/cmd/go#hdr-Environment_variables)
- [GitHub Actions Environment Files](https://docs.github.com/en/actions/using-workflows/workflow-commands-for-github-actions#environment-files)
- [Previous Fix: DNS Resolution Issues](../ci-fixes/go-module-proxy-dns-fix.md) (if exists)

## Conclusion

This fix resolves the `$HOME` not defined error by using the correct GitHub Actions pattern for setting environment variables. The solution is simpler, more reliable, and works in all execution contexts without requiring persistent configuration files.
