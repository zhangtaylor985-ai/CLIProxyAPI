#!/usr/bin/env bash
set -Eeuo pipefail

SERVICE="${SERVICE:-cliproxyapi.service}"
REMOTE="${REMOTE:-origin}"
BRANCH="${BRANCH:-main}"
PROBE_URL="${PROBE_URL:-http://127.0.0.1:8317/}"

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"

cd "${REPO_DIR}"

die() {
  printf 'deploy_systemd: %s\n' "$*" >&2
  exit 1
}

run() {
  printf '+'
  printf ' %q' "$@"
  printf '\n'
  "$@"
}

require_clean_worktree() {
  if ! git diff --quiet || ! git diff --cached --quiet || [[ -n "$(git ls-files --others --exclude-standard)" ]]; then
    git status --short
    die "working tree is not clean; commit/stash unrelated work before deploying"
  fi
}

printf 'Repo: %s\n' "${REPO_DIR}"
run git status --short --branch
require_clean_worktree

run git fetch "${REMOTE}" "${BRANCH}"
read -r behind ahead < <(git rev-list --left-right --count "FETCH_HEAD...HEAD")
if (( behind > 0 && ahead > 0 )); then
  die "local branch diverged from ${REMOTE}/${BRANCH}; resolve manually before deploying"
fi
if (( ahead > 0 )); then
  die "local branch is ahead of ${REMOTE}/${BRANCH}; push or revert before deploying"
fi
if (( behind > 0 )); then
  run git pull --ff-only "${REMOTE}" "${BRANCH}"
else
  printf 'Already up to date with %s/%s.\n' "${REMOTE}" "${BRANCH}"
fi

require_clean_worktree

VERSION="$(git describe --tags --always 2>/dev/null || git rev-parse --short HEAD)"
COMMIT="$(git rev-parse HEAD)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

run env CGO_ENABLED=0 GOOS=linux go build \
  -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
  -o ./bin/cliproxyapi ./cmd/server

run sudo systemctl restart "${SERVICE}"
sleep 2
run systemctl status "${SERVICE}" --no-pager -l
run curl -fsS "${PROBE_URL}"
printf '\n'
run journalctl -u "${SERVICE}" -n 30 --no-pager -l
