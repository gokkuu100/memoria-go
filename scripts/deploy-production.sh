#!/usr/bin/env bash
# Sync local backend to production EC2 and rebuild the Docker stack.
#
# Usage (from anywhere):
#   ./memoria-gobackend/scripts/deploy-production.sh
#
# Optional env overrides:
#   DEPLOY_HOST=ubuntu@203.0.113.10
#   DEPLOY_PATH=/opt/memoria/memoria-gobackend
#   SSH_KEY=/path/to/deploy-key.pem
#   HEALTH_URL=https://memoriago.duckdns.org/readyz
#   SKIP_BUILD=1          # rsync + restart only (no --build)
#   RSYNC_DRY_RUN=1       # preview rsync changes

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BACKEND_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
REPO_ROOT="$(cd "$BACKEND_DIR/.." && pwd)"

DEPLOY_HOST="${DEPLOY_HOST:-ubuntu@203.0.113.10}"
DEPLOY_PATH="${DEPLOY_PATH:-/opt/memoria/memoria-gobackend}"
SSH_KEY="${SSH_KEY:-$REPO_ROOT/deploy-key.pem}"
HEALTH_URL="${HEALTH_URL:-https://memoriago.duckdns.org/readyz}"

SSH_OPTS=(-o StrictHostKeyChecking=accept-new -o ConnectTimeout=20)
if [[ -n "$SSH_KEY" ]]; then
  if [[ ! -f "$SSH_KEY" ]]; then
    echo "error: SSH key not found: $SSH_KEY" >&2
    echo "Set SSH_KEY to your .pem path, e.g.:" >&2
    echo "  SSH_KEY=$REPO_ROOT/deploy-key.pem $0" >&2
    exit 1
  fi
  chmod 400 "$SSH_KEY" 2>/dev/null || true
  SSH_OPTS+=(-i "$SSH_KEY")
fi

RSYNC_OPTS=(-avz --delete)
RSYNC_EXCLUDES=(
  --exclude '.env'
  --exclude '.git'
  --exclude 'pgdata'
  --exclude 'miniodata'
  --exclude 'backups'
  --exclude 'node_modules'
)
if [[ "${RSYNC_DRY_RUN:-}" == "1" ]]; then
  RSYNC_OPTS+=(--dry-run)
  echo "DRY RUN — rsync will not write remote files"
fi

echo "==> Syncing $BACKEND_DIR -> $DEPLOY_HOST:$DEPLOY_PATH"
rsync "${RSYNC_OPTS[@]}" "${RSYNC_EXCLUDES[@]}" \
  -e "ssh ${SSH_OPTS[*]}" \
  "$BACKEND_DIR/" \
  "$DEPLOY_HOST:$DEPLOY_PATH/"

if [[ "${RSYNC_DRY_RUN:-}" == "1" ]]; then
  echo "Dry run complete (remote stack unchanged)."
  exit 0
fi

if [[ "${SKIP_BUILD:-}" != "1" ]]; then
  UP_CMD="docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build"
  echo "==> Rebuilding and restarting stack on $DEPLOY_HOST"
else
  UP_CMD="docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d"
  echo "==> Restarting stack (SKIP_BUILD=1, no image rebuild)"
fi

ssh "${SSH_OPTS[@]}" "$DEPLOY_HOST" "cd '$DEPLOY_PATH' && sg docker -c '$UP_CMD' && sg docker -c 'docker compose -f docker-compose.yml -f docker-compose.prod.yml ps'"

echo "==> Waiting for API health check: $HEALTH_URL"
for i in {1..30}; do
  if response="$(curl -sf --connect-timeout 5 "$HEALTH_URL" 2>/dev/null)"; then
    echo "    $response"
    echo "Deploy complete."
    exit 0
  fi
  sleep 2
done

echo "error: health check did not pass within 60s" >&2
echo "Check logs: ssh ${SSH_OPTS[*]} $DEPLOY_HOST 'cd $DEPLOY_PATH && docker compose logs api --tail 50'" >&2
exit 1
