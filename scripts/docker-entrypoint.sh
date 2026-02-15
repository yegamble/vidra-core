#!/bin/sh
set -e

echo "=== Athena Docker Entrypoint ==="

# Detect resource limits (cgroup v2, then v1, then host)
detect_ram_gb() {
    # Try cgroup v2
    if [ -f /sys/fs/cgroup/memory.max ]; then
        MAX=$(cat /sys/fs/cgroup/memory.max)
        if [ "$MAX" != "max" ]; then
            echo $((MAX / 1024 / 1024 / 1024))
            return
        fi
    fi

    # Try cgroup v1
    if [ -f /sys/fs/cgroup/memory/memory.limit_in_bytes ]; then
        MAX=$(cat /sys/fs/cgroup/memory/memory.limit_in_bytes)
        # cgroup v1 uses very large number for unlimited
        if [ "$MAX" -lt 9223372036854775807 ]; then
            echo $((MAX / 1024 / 1024 / 1024))
            return
        fi
    fi

    # Fallback to /proc/meminfo (host RAM)
    if [ -f /proc/meminfo ]; then
        KB=$(grep MemTotal /proc/meminfo | awk '{print $2}')
        echo $((KB / 1024 / 1024))
        return
    fi

    echo "1"  # Default 1GB if detection fails
}

detect_cpu_cores() {
    # Try cgroup v2
    if [ -f /sys/fs/cgroup/cpu.max ]; then
        CPUINFO=$(cat /sys/fs/cgroup/cpu.max)
        QUOTA=$(echo "$CPUINFO" | awk '{print $1}')
        PERIOD=$(echo "$CPUINFO" | awk '{print $2}')
        if [ "$QUOTA" != "max" ] && [ -n "$PERIOD" ]; then
            CORES=$((QUOTA / PERIOD))
            if [ "$CORES" -lt 1 ]; then
                CORES=1
            fi
            echo "$CORES"
            return
        fi
    fi

    # Try cgroup v1
    if [ -f /sys/fs/cgroup/cpu/cpu.cfs_quota_us ]; then
        QUOTA=$(cat /sys/fs/cgroup/cpu/cpu.cfs_quota_us)
        PERIOD=$(cat /sys/fs/cgroup/cpu/cpu.cfs_period_us)
        if [ "$QUOTA" -gt 0 ] && [ "$PERIOD" -gt 0 ]; then
            CORES=$((QUOTA / PERIOD))
            if [ "$CORES" -lt 1 ]; then
                CORES=1
            fi
            echo "$CORES"
            return
        fi
    fi

    # Fallback to nproc
    nproc
}

RAM_GB=$(detect_ram_gb)
CPU_CORES=$(detect_cpu_cores)

echo "--- Resource Detection ---"
echo "RAM: ${RAM_GB}GB"
echo "CPU Cores: ${CPU_CORES}"

# Provide recommendations based on resources
if [ "$RAM_GB" -lt 2 ] || [ "$CPU_CORES" -lt 2 ]; then
    echo "⚠️  Minimal tier detected (${RAM_GB}GB RAM, ${CPU_CORES} cores)"
    echo "   Recommendation: Core services only (Postgres + Redis + App)"
    echo "   Optional services (IPFS, ClamAV, Whisper) may be unstable or slow"
elif [ "$RAM_GB" -ge 8 ] && [ "$CPU_CORES" -ge 4 ]; then
    echo "✓ Full tier detected (${RAM_GB}GB RAM, ${CPU_CORES} cores)"
    echo "   Recommendation: All services can run comfortably"
else
    echo "✓ Standard tier detected (${RAM_GB}GB RAM, ${CPU_CORES} cores)"
    echo "   Recommendation: Core + ClamAV. IPFS optional. Whisper requires 8GB+ RAM."
fi

echo ""
echo "--- Service Configuration ---"

# Check which services are external vs local
check_service_mode() {
    SERVICE_NAME=$1
    MODE_VAR="${SERVICE_NAME}_MODE"
    eval MODE=\$$MODE_VAR

    if [ "$MODE" = "external" ]; then
        echo "  $SERVICE_NAME: EXTERNAL (not waiting for local container)"
        return 1  # Return non-zero to indicate external
    else
        echo "  $SERVICE_NAME: LOCAL DOCKER (will wait for readiness)"
        return 0  # Return zero to indicate local
    fi
}

# Check each service
check_service_mode "POSTGRES" && WAIT_POSTGRES=1 || WAIT_POSTGRES=0
check_service_mode "REDIS" && WAIT_REDIS=1 || WAIT_REDIS=0

# Optional services
if [ "${ENABLE_IPFS:-false}" = "true" ]; then
    check_service_mode "IPFS" && WAIT_IPFS=1 || WAIT_IPFS=0
else
    echo "  IPFS: DISABLED"
    WAIT_IPFS=0
fi

if [ "${ENABLE_CLAMAV:-false}" = "true" ]; then
    echo "  CLAMAV: ENABLED (local Docker)"
    WAIT_CLAMAV=1
else
    echo "  CLAMAV: DISABLED"
    WAIT_CLAMAV=0
fi

if [ "${ENABLE_WHISPER:-false}" = "true" ]; then
    echo "  WHISPER: ENABLED (local Docker)"
    WAIT_WHISPER=1
else
    echo "  WHISPER: DISABLED"
    WAIT_WHISPER=0
fi

echo ""
echo "--- Waiting for Services ---"

# Wait for Postgres if local
if [ "$WAIT_POSTGRES" = "1" ]; then
    echo "Waiting for PostgreSQL..."
    POSTGRES_HOST=$(echo "$DATABASE_URL" | sed 's|.*@\([^:]*\):.*|\1|')
    POSTGRES_PORT=$(echo "$DATABASE_URL" | sed 's|.*:\([0-9]*\)/.*|\1|')

    until pg_isready -h "${POSTGRES_HOST:-postgres}" -p "${POSTGRES_PORT:-5432}" -U "${POSTGRES_USER:-athena_user}" >/dev/null 2>&1; do
        echo "  PostgreSQL is unavailable - sleeping"
        sleep 2
    done
    echo "  PostgreSQL is ready!"
fi

# Wait for Redis if local
if [ "$WAIT_REDIS" = "1" ]; then
    echo "Waiting for Redis..."
    REDIS_HOST=$(echo "$REDIS_URL" | sed 's|redis://\([^:]*\):.*|\1|')
    REDIS_PORT=$(echo "$REDIS_URL" | sed 's|.*:\([0-9]*\)/.*|\1|')

    until redis-cli -h "${REDIS_HOST:-redis}" -p "${REDIS_PORT:-6379}" ping >/dev/null 2>&1; do
        echo "  Redis is unavailable - sleeping"
        sleep 2
    done
    echo "  Redis is ready!"
fi

# Wait for IPFS if enabled and local
if [ "$WAIT_IPFS" = "1" ]; then
    echo "Waiting for IPFS..."
    IPFS_HOST=$(echo "${IPFS_API:-http://ipfs:5001}" | sed 's|http://\([^:]*\):.*|\1|')
    IPFS_PORT=$(echo "${IPFS_API:-http://ipfs:5001}" | sed 's|.*:\([0-9]*\)|\1|')

    until wget --spider -q "${IPFS_API:-http://ipfs:5001}/api/v0/version" 2>/dev/null; do
        echo "  IPFS is unavailable - sleeping"
        sleep 2
    done
    echo "  IPFS is ready!"
fi

echo ""
echo "--- Starting Application ---"

# Auto-migration is handled by the Go app on startup (via embedded Goose)
# Set AUTO_MIGRATE=false in environment to disable if needed

echo "Executing: ./server"
exec ./server
