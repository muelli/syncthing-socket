#!/bin/sh
if [ -n "$VERSION" ]; then
    echo "$VERSION"
elif [ -d .git ]; then
    DIRTY=$([ -z "$(git status --porcelain 2>/dev/null)" ] || echo "-dirty")
    BASE_VER=$(git describe --exact-match --tags HEAD 2>/dev/null || git rev-list --count HEAD 2>/dev/null)
    echo "${BASE_VER}${DIRTY}"
elif [ -f VERSION ]; then
    cat VERSION
else
    echo "unknown"
fi
