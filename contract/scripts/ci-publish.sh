#!/usr/bin/env bash
# Tag-triggered publish of @morehao/trendradar-contract via npm Trusted Publishing (OIDC).
# Invoked by .github/workflows/contract-release.yml. Local debug: RELEASE_TAG=contract/v0.2.0 bash scripts/ci-publish.sh
# Guards: tag version == package.json version; idempotent skip if already published; retry with registry re-check.
set -euo pipefail

TAG="${RELEASE_TAG:-${GIT_TAG:-}}"
if [ -z "$TAG" ]; then
  echo "error: RELEASE_TAG not set (expected e.g. contract/v0.2.0)" >&2
  exit 1
fi
PKG_VERSION="${TAG#contract/v}" # strip the nested-module prefix

cd "$(dirname "$0")/.." # -> contract

name="@morehao/trendradar-contract"
PKG_JSON_VERSION="$(node -p "require('./package.json').version")"
echo "Publish check: ${name}@${PKG_JSON_VERSION} (tag=${TAG})"

if [ "$PKG_JSON_VERSION" != "$PKG_VERSION" ]; then
  echo "error: tag version ${PKG_VERSION} != package.json version ${PKG_JSON_VERSION}" >&2
  exit 1
fi

published="$(npm view "${name}@${PKG_VERSION}" version --registry https://registry.npmjs.org 2>/dev/null || true)"
if [ -n "$published" ]; then
  echo "${name}@${PKG_VERSION} already published; skipping"
  exit 0
fi

echo "npm version: $(npm --version)"
RETRY_BASE_SECONDS="${RETRY_BASE_SECONDS:-30}"
MAX_ATTEMPTS=3
attempt=1
while :; do
  if npm publish --provenance --access public --tag latest --registry https://registry.npmjs.org; then
    exit 0
  fi
  if npm view "${name}@${PKG_VERSION}" version --registry https://registry.npmjs.org >/dev/null 2>&1; then
    echo "${name}@${PKG_VERSION} present in registry; treating as success (lost response)"
    exit 0
  fi
  if [ "$attempt" -ge "$MAX_ATTEMPTS" ]; then
    echo "error: npm publish failed ${attempt} times" >&2
    echo "check: 1) status.npmjs.org  2) Trusted Publisher(workflow=contract-release.yml, allow-publish)  3) rerun the run" >&2
    exit 1
  fi
  wait_seconds=$((attempt * RETRY_BASE_SECONDS))
  echo "attempt ${attempt} failed; retrying in ${wait_seconds}s" >&2
  sleep "$wait_seconds"
  attempt=$((attempt + 1))
done
