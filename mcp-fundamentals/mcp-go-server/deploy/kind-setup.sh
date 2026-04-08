#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="mcp-local"
IMAGE_NAME="mcp-wikipedia-server:latest"
NAMESPACE="mcp"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log()  { echo -e "${GREEN}[+]${NC} $*"; }
warn() { echo -e "${YELLOW}[!]${NC} $*"; }
fail() { echo -e "${RED}[x]${NC} $*"; exit 1; }

check_prereqs() {
    log "Checking prerequisites..."
    for cmd in docker kind kubectl; do
        if ! command -v "$cmd" &>/dev/null; then
            fail "$cmd is not installed"
        fi
        echo "  $cmd: $(command -v "$cmd")"
    done
    if ! docker info &>/dev/null; then
        fail "Docker daemon is not running"
    fi
    log "All prerequisites met"
}

create_cluster() {
    if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
        warn "Cluster '${CLUSTER_NAME}' already exists, deleting..."
        kind delete cluster --name "${CLUSTER_NAME}"
    fi
    log "Creating Kind cluster '${CLUSTER_NAME}'..."
    kind create cluster --name "${CLUSTER_NAME}" --config "${SCRIPT_DIR}/kind-config.yaml"
    log "Waiting for nodes to be ready..."
    kubectl wait --for=condition=Ready nodes --all --timeout=120s
    log "Cluster is ready"
}

build_and_load() {
    log "Building Docker image '${IMAGE_NAME}'..."
    docker build -t "${IMAGE_NAME}" "${PROJECT_DIR}"
    log "Loading image into Kind cluster..."
    kind load docker-image "${IMAGE_NAME}" --name "${CLUSTER_NAME}"
    log "Image loaded"
}

deploy() {
    log "Creating namespace '${NAMESPACE}'..."
    kubectl apply -f "${SCRIPT_DIR}/k8s/namespace.yaml"
    kubectl wait --for=jsonpath='{.status.phase}'=Active "namespace/${NAMESPACE}" --timeout=30s

    log "Applying Kubernetes manifests..."
    kubectl apply -f "${SCRIPT_DIR}/k8s/"

    log "Waiting for rollout..."
    kubectl rollout status deployment/mcp-wikipedia-server -n "${NAMESPACE}" --timeout=120s
    log "Deployment ready"
}

smoke_test() {
    log "Running smoke test..."
    local base_url="http://localhost:30080"
    local retries=10
    while ! curl -sf -X POST "${base_url}/mcp" \
        -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' \
        -o /dev/null 2>/dev/null; do
        retries=$((retries - 1))
        if [ "$retries" -le 0 ]; then
            fail "MCP server not reachable after retries"
        fi
        sleep 2
    done
    log "Smoke test passed — MCP server is responding"
}

print_info() {
    echo ""
    log "Setup complete!"
    echo ""
    echo "  Endpoint: http://localhost:30080/mcp"
    echo ""
    echo "  Test with:"
    echo "    curl -s -X POST http://localhost:30080/mcp \\"
    echo "      -H 'Content-Type: application/json' \\"
    echo "      -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-03-26\",\"capabilities\":{},\"clientInfo\":{\"name\":\"test\",\"version\":\"1.0\"}}}'"
    echo ""
    echo "  Useful commands:"
    echo "    kubectl get pods -n ${NAMESPACE}"
    echo "    kubectl logs -l app=mcp-wikipedia-server -n ${NAMESPACE} -f"
    echo "    $0 teardown"
    echo ""
}

teardown() {
    log "Deleting Kind cluster '${CLUSTER_NAME}'..."
    kind delete cluster --name "${CLUSTER_NAME}"
    log "Cluster deleted"
}

case "${1:-setup}" in
    setup)
        check_prereqs
        create_cluster
        build_and_load
        deploy
        smoke_test
        print_info
        ;;
    teardown)
        teardown
        ;;
    *)
        echo "Usage: $0 {setup|teardown}"
        exit 1
        ;;
esac
