#!/bin/sh
DIRTY=$([ -z "$(git status --porcelain)" ] || echo "-dirty")
BASE_VER=$(git describe --exact-match --tags HEAD 2>/dev/null || git rev-list --count HEAD)
echo "${BASE_VER}${DIRTY}"
