---
name: install-script-ux-patterns
description: |
  Design patterns for beginner-friendly installation scripts. Use when: (1) creating
  install.sh or setup scripts, (2) improving installation UX, (3) supporting multiple
  user personas (beginners to power users). Covers auto-detection, error handling,
  graceful defaults, and Docker/native mode support.
author: Claude Code
version: 1.0.0
---

# Install Script UX Patterns

## Problem

Installation scripts often fail for beginners due to:

- Requiring manual path configuration
- Cryptic error messages when run from wrong directory
- Missing configuration files blocking setup
- Assuming power-user knowledge of prerequisites

## Context / Trigger Conditions

Use these patterns when:

- Creating `install.sh`, `setup.sh`, or similar automation scripts
- Supporting both Docker and native installation modes
- Designing setup wizards or first-run experiences
- Targeting diverse user personas (beginners to power users)
- Working on projects with complex initial configuration

## Solution

### 1. Auto-Detection Over Configuration

**Pattern**: Detect context automatically instead of requiring user input.

```bash
# Auto-detect repository root from script location
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
INSTALL_DIR="${INSTALL_DIR:-$(dirname "$SCRIPT_DIR")}"

# Check if already in repository
if [ -f "$INSTALL_DIR/docker-compose.yml" ]; then
    echo "✓ Found existing installation at $INSTALL_DIR"
elif [ -d "$INSTALL_DIR/.git" ]; then
    echo "✓ Found git repository at $INSTALL_DIR"
    cd "$INSTALL_DIR" && git pull
else
    # Clone if needed
    git clone https://github.com/org/repo.git "$INSTALL_DIR"
fi
```

**Why**: Beginners don't know where to run scripts. Auto-detection "just works".

### 2. Graceful Error Handling

**Pattern**: Check prerequisites and fail with actionable error messages.

```bash
# Check for non-empty directory (could cause conflicts)
if [ "$(ls -A "$INSTALL_DIR" 2>/dev/null)" ] && \
   [ ! -f "$INSTALL_DIR/docker-compose.yml" ] && \
   [ ! -d "$INSTALL_DIR/.git" ]; then
    echo "❌ Error: Directory not empty and not a valid installation"
    echo "   Please use an empty directory or specify INSTALL_DIR"
    exit 1
fi

# Check Docker availability
if ! command -v docker >/dev/null 2>&1; then
    if [[ "$OSTYPE" == "darwin"* ]]; then
        echo "❌ Docker not found. Install Docker Desktop for Mac:"
        echo "   https://docs.docker.com/desktop/install/mac-install/"
    else
        echo "📦 Installing Docker..."
        curl -fsSL https://get.docker.com | sh
    fi
    exit 1
fi
```

**Why**: Clear error messages with solutions reduce support burden.

### 3. Beginner-Friendly Defaults

**Pattern**: Generate safe defaults that enable immediate use.

```bash
# Generate .env with setup mode enabled
cat > .env <<EOF
# Setup mode - complete setup via web wizard at http://localhost:8080/setup/welcome
SETUP_COMPLETED=false
PORT=8080

# Beginner-friendly defaults (disable advanced features)
REQUIRE_IPFS=false
ENABLE_IPFS_CLUSTER=false

# Optional secrets (wizard will configure these)
JWT_SECRET=
DATABASE_URL=
REDIS_URL=
EOF

echo "✓ Created .env with setup mode enabled"
echo "   Run 'docker compose up' and visit http://localhost:8080/setup/welcome"
```

**Why**: Beginners can get started without understanding every config option.

### 4. Optional Configuration for Power Users

**Pattern**: Support advanced users who want pre-configured values.

```bash
# Check if .env already exists (preserve user config)
if [ -f "$INSTALL_DIR/.env" ]; then
    echo "✓ Found existing .env, preserving configuration"
else
    generate_default_env  # Only create if missing
fi

# Allow environment variable overrides
DATABASE_URL="${DATABASE_URL:-}"
REDIS_URL="${REDIS_URL:-}"
JWT_SECRET="${JWT_SECRET:-}"

# Support INSTALL_DIR override
INSTALL_DIR="${INSTALL_DIR:-$HOME/vidra}"
```

**Why**: Power users can automate installation with pre-set values.

### 5. Docker Compose Optional Environment Variables

**Pattern**: Make secrets optional for setup mode, required for production.

```yaml
# docker-compose.yml
services:
  app:
    environment:
      # Optional in setup mode (wizard configures these)
      JWT_SECRET: ${JWT_SECRET:-}
      DATABASE_URL: ${DATABASE_URL:-}
      REDIS_URL: ${REDIS_URL:-}

      # Always required
      PORT: ${PORT:-8080}
      SETUP_COMPLETED: ${SETUP_COMPLETED:-false}
```

**Application code detects mode:**

```go
func Load() (*Config, error) {
    setupCompleted := strings.ToLower(strings.TrimSpace(os.Getenv("SETUP_COMPLETED")))

    // Setup mode: allow partial config
    if setupCompleted != "true" && setupCompleted != "1" {
        return &Config{SetupMode: true, Port: getPort()}, nil
    }

    // Normal mode: require full config
    if os.Getenv("JWT_SECRET") == "" {
        return nil, errors.New("JWT_SECRET required in normal mode")
    }
    // ...
}
```

**Why**: Wizard can start without secrets, but production mode enforces them.

### 6. Three Installation Paths

**Pattern**: Support multiple user sophistication levels.

```bash
# Path 1: Zero-config Docker (beginners)
./install.sh && docker compose up
# → Wizard at localhost:8080/setup/welcome

# Path 2: Pre-configured Docker (power users)
export DATABASE_URL="postgres://..."
export REDIS_URL="redis://..."
export JWT_SECRET="your-secret"
export SETUP_COMPLETED=true
./install.sh && docker compose up

# Path 3: Native mode (developers)
./install.sh --native
# → Not yet implemented, future enhancement
```

**Why**: Different users have different needs and skill levels.

## Verification

After implementing these patterns:

- [ ] Script runs from any directory (auto-detects repo)
- [ ] Script works in empty directory (clones repo)
- [ ] Script preserves existing .env (doesn't overwrite)
- [ ] Docker missing → clear install instructions
- [ ] Non-empty directory → clear error message
- [ ] Generated .env → wizard loads successfully
- [ ] Power users can skip wizard with pre-set env vars

## Example: Real-World Usage

**Vidra Core project install script** (`scripts/install.sh`):

1. Auto-detects repo root using `$(dirname "$0")`
2. Handles 4 scenarios: existing compose file, existing git repo, non-empty dir, empty dir
3. Generates .env with `SETUP_COMPLETED=false` for wizard flow
4. Disables advanced features (IPFS) by default
5. Preserves existing .env if found
6. Checks Docker availability with OS-specific install instructions

**Result**: Users run `bash <(curl -Ls https://install.example.com/vidra)` and get a working setup wizard with zero configuration.

## Testing Strategy

To test these patterns:

1. **Shell tests with temp directories** (see `scripts/install_test.sh` pattern)
   - Source install.sh functions in isolation
   - Mock `docker` and `git` commands
   - Test each scenario in fresh temp directory

2. **Go integration tests** (see `internal/config/config_test.go` pattern)
   - Use `t.Setenv()` to isolate environment variables
   - Test setup mode detection with all env var combinations
   - Test wizard HTTP flow with `httptest.NewServer`

## References

- [Vidra Core install.sh implementation](../../scripts/install.sh)
- [Vidra Core config mode detection](../../internal/config/config.go)
- [Vidra Core setup wizard](../../internal/setup/)
- [12-Factor App: Config](https://12factor.net/config)
- [Docker Compose environment variables](https://docs.docker.com/compose/environment-variables/set-environment-variables/)
