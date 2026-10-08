#!/usr/bin/env sh
# Fails if git tracks a path that must never be committed: dependency and
# build output (node_modules/, .nuxt/, .output/) or a real .env file. Only
# *.env.example templates may be tracked.
# Runs in CI (backend job) and on a laptop: sh scripts/check_tracked_files.sh
set -eu

root=$(git rev-parse --show-toplevel)
cd "$root"

bad=$(git ls-files | grep -E '(^|/)(node_modules|\.nuxt|\.output)/|(^|/)([^/]*\.)?env$|(^|/)\.env(\.[^/]*)?$' |
	grep -Ev '\.env\.example$' || true)

if [ -n "$bad" ]; then
	echo "tracked paths that must not be committed:" >&2
	echo "$bad" >&2
	echo "remove them from the index (git rm --cached) and keep them git-ignored" >&2
	exit 1
fi
echo "tracked files OK"
