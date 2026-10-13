#!/bin/sh
# Builds Any Install for Windows and Linux into dist/.
# Usage: scripts/build.sh [version]
set -eu
cd "$(dirname "$0")/.."
VERSION="${1:-dev}"
NUMERIC="$(echo "$VERSION" | sed -E 's/^v//; s/[^0-9.].*$//')"
[ -n "$NUMERIC" ] || NUMERIC="0.0.0"

mkdir -p dist
# Icon, version info and manifest for the Windows .exe
go run github.com/tc-hib/go-winres@v0.3.3 make --in winres/winres.json --out rsrc --arch amd64 \
  --product-version "$NUMERIC" --file-version "$NUMERIC"

LDFLAGS="-s -w -X main.version=$VERSION"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS -H=windowsgui" -o dist/AnyInstall.exe .
rm -f rsrc_windows_*.syso
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o dist/AnyInstall-linux-x86_64 .
ls -l dist
