#!/usr/bin/env sh
# Runs shellcheck -x on every backend/scripts/*.sh with the dialect named by
# its shebang (bash -> -s bash, anything else, or no shebang -> -s sh).
# Uses a local shellcheck if there is one, else koalaman/shellcheck via Docker
# (nothing to install). Run from backend/: sh scripts/shellcheck_all.sh
set -eu

if command -v shellcheck >/dev/null 2>&1; then
	run_shellcheck() { shellcheck -x "$@"; }
elif command -v docker >/dev/null 2>&1; then
	# The image has no shell, so it is started once per file. pwd -W gives
	# Docker Desktop a Windows path under Git Bash; elsewhere plain pwd.
	here=$(pwd -W 2>/dev/null || pwd)
	run_shellcheck() {
		MSYS_NO_PATHCONV=1 docker run --rm -v "$here:/mnt:ro" -w /mnt \
			koalaman/shellcheck:stable -x "$@"
	}
else
	echo "need shellcheck or docker" >&2
	exit 1
fi

status=0
for f in scripts/*.sh; do
	case "$(head -n 1 "$f")" in
	*bash*) shell=bash ;;
	*) shell="sh" ;;
	esac
	run_shellcheck -s "$shell" "$f" || status=1
done
exit "$status"
