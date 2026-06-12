#!/usr/bin/env bash
# Stub: future TypeScript client generation for the Expo app from openapi.yaml.
# When wired, this will emit types under ../memoria/src/api/generated/.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OPENAPI="$ROOT/openapi.yaml"
OUT_DIR="${TS_CLIENT_OUT:-$(cd "$ROOT/../memoria/src/api/generated" 2>/dev/null && pwd || echo "$ROOT/../memoria/src/api/generated")}"

echo "OpenAPI spec: $OPENAPI"
echo "Target (not generated yet): $OUT_DIR"
echo ""
echo "Planned pipeline (post-B11):"
echo "  1. make openapi-validate"
echo "  2. openapi-typescript $OPENAPI -o $OUT_DIR/schema.ts"
echo "  3. App hooks in memoria/src/api/hooks import generated types"
echo ""
echo "No generation run in B11 — hand-maintained openapi.yaml is the source of truth."
