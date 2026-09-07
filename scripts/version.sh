#!/bin/sh
# Print the project version.
#
# Everything is resolved relative to this script's own location rather than the caller's
# working directory. Depending on the caller's directory made this return "unknown" when
# invoked as `sh ../scripts/version.sh` from android/, which is how an APK reached the
# F-Droid repository with versionName "unknown".
#
# $VERSION wins when set, so a build that has to disturb the working tree (gomobile bind
# cannot run with a vendor/ directory present) can pass in the version it computed before
# doing so, instead of getting a spurious "-dirty".
ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd) || exit 1

if [ -n "$VERSION" ]; then
    echo "$VERSION"
elif [ -d "$ROOT/.git" ]; then
    DIRTY=$([ -z "$(git -C "$ROOT" status --porcelain 2>/dev/null)" ] || echo "-dirty")
    BASE_VER=$(git -C "$ROOT" describe --exact-match --tags HEAD 2>/dev/null \
        || git -C "$ROOT" rev-list --count HEAD 2>/dev/null)
    echo "${BASE_VER}${DIRTY}"
elif [ -f "$ROOT/VERSION" ]; then
    cat "$ROOT/VERSION"
else
    echo "unknown"
fi
