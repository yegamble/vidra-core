#!/bin/bash
# Rollback Script for Blue/Green Deployments
# Instantly switches traffic back to the previous environment

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Configuration
NAMESPACE="${NAMESPACE:-vidra}"
SERVICE_NAME="vidra-api"

# Functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

detect_current_active() {
    kubectl get service ${SERVICE_NAME} -n ${NAMESPACE} \
        -o jsonpath='{.spec.selector.version}' 2>/dev/null || echo "unknown"
}

detect_deployments() {
    BLUE_PODS=$(kubectl get pods -l app=vidra,component=api,version=blue -n ${NAMESPACE} --no-headers 2>/dev/null | wc -l)
    GREEN_PODS=$(kubectl get pods -l app=vidra,component=api,version=green -n ${NAMESPACE} --no-headers 2>/dev/null | wc -l)

    echo "blue:${BLUE_PODS},green:${GREEN_PODS}"
}

# Main script
main() {
    echo ""
    echo "=========================================="
    echo "  VIDRA DEPLOYMENT ROLLBACK"
    echo "=========================================="
    echo ""

    # Check if kubectl is configured
    if ! kubectl cluster-info &> /dev/null; then
        log_error "kubectl is not configured or cluster is not reachable"
        exit 1
    fi

    # Detect current state
    log_info "Detecting current deployment state..."
    CURRENT_ACTIVE=$(detect_current_active)
    DEPLOYMENTS=$(detect_deployments)
    BLUE_PODS=$(echo $DEPLOYMENTS | cut -d',' -f1 | cut -d':' -f2)
    GREEN_PODS=$(echo $DEPLOYMENTS | cut -d',' -f2 | cut -d':' -f2)

    echo ""
    echo "Current State:"
    echo "  Active Environment: ${CURRENT_ACTIVE}"
    echo "  Blue Pods: ${BLUE_PODS}"
    echo "  Green Pods: ${GREEN_PODS}"
    echo ""

    # Determine rollback target
    if [ "$CURRENT_ACTIVE" = "blue" ]; then
        ROLLBACK_TO="green"
    elif [ "$CURRENT_ACTIVE" = "green" ]; then
        ROLLBACK_TO="blue"
    else
        log_error "Cannot determine current active environment"
        exit 1
    fi

    # Confirm rollback
    log_warn "This will switch traffic from ${CURRENT_ACTIVE} to ${ROLLBACK_TO}"
    read -p "Continue with rollback? (yes/no): " CONFIRM

    if [ "$CONFIRM" != "yes" ]; then
        log_info "Rollback cancelled"
        exit 0
    fi

    # Check if target environment has running pods
    if [ "$ROLLBACK_TO" = "blue" ] && [ "$BLUE_PODS" -eq 0 ]; then
        log_warn "Blue environment has no running pods. Scaling up..."
        kubectl scale deployment vidra-api-blue --replicas=3 -n ${NAMESPACE}
        log_info "Waiting for Blue pods to be ready..."
        kubectl wait --for=condition=ready pod -l version=blue --timeout=120s -n ${NAMESPACE}
    elif [ "$ROLLBACK_TO" = "green" ] && [ "$GREEN_PODS" -eq 0 ]; then
        log_warn "Green environment has no running pods. Scaling up..."
        kubectl scale deployment vidra-api-green --replicas=3 -n ${NAMESPACE}
        log_info "Waiting for Green pods to be ready..."
        kubectl wait --for=condition=ready pod -l version=green --timeout=120s -n ${NAMESPACE}
    fi

    # Health check on target environment
    log_info "Performing health check on ${ROLLBACK_TO} environment..."
    if ! kubectl run rollback-health-check \
        --image=curlimages/curl \
        --restart=Never \
        --rm \
        -i \
        -n ${NAMESPACE} \
        --timeout=30s \
        -- curl -f http://vidra-api-${ROLLBACK_TO}/health; then
        log_error "Health check failed on ${ROLLBACK_TO} environment"
        log_error "Rollback aborted for safety"
        exit 1
    fi

    # Perform rollback
    log_info "Switching traffic to ${ROLLBACK_TO}..."
    kubectl patch service ${SERVICE_NAME} -n ${NAMESPACE} -p "{\"spec\":{\"selector\":{\"version\":\"${ROLLBACK_TO}\"}}}"

    # Verify rollback
    sleep 2
    NEW_ACTIVE=$(detect_current_active)
    if [ "$NEW_ACTIVE" = "$ROLLBACK_TO" ]; then
        log_info "Rollback successful!"
        echo ""
        echo "Traffic is now routed to: ${ROLLBACK_TO}"
        echo ""
    else
        log_error "Rollback verification failed"
        exit 1
    fi

    # Remove canary ingress if exists
    log_info "Cleaning up canary ingress (if exists)..."
    kubectl delete ingress vidra-ingress-${CURRENT_ACTIVE}-canary -n ${NAMESPACE} 2>/dev/null || true

    # Post-rollback verification
    log_info "Running post-rollback verification..."
    if kubectl run rollback-verify \
        --image=curlimages/curl \
        --restart=Never \
        --rm \
        -i \
        -n ${NAMESPACE} \
        --timeout=30s \
        -- sh -c "curl -f http://vidra-api/health && curl -f http://vidra-api/ready"; then
        log_info "Post-rollback verification: PASSED"
    else
        log_warn "Post-rollback verification failed, but traffic switch is complete"
    fi

    echo ""
    echo "=========================================="
    echo "  ROLLBACK COMPLETE"
    echo "=========================================="
    echo ""
    echo "Next steps:"
    echo "  1. Monitor application metrics"
    echo "  2. Investigate root cause of deployment failure"
    echo "  3. Review logs: kubectl logs -l version=${CURRENT_ACTIVE} -n ${NAMESPACE}"
    echo "  4. Scale down failed environment when stable"
    echo ""
}

# Run main function
main "$@"
