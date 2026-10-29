#!/usr/bin/env bash
# Cross-compile ashgate-lite.exe and stage the signed WinDivert driver in dist/.
set -euo pipefail
cd "$(dirname "$0")/.."

# Optional overrides; see .env.example.
[ -f .env ] && set -a && . ./.env && set +a
WINDIVERT_VERSION="${WINDIVERT_VERSION:-2.2.2}"
GO_WINRES_VERSION="${GO_WINRES_VERSION:-v0.3.1}"

mkdir -p build dist
zip="build/WinDivert-${WINDIVERT_VERSION}-A.zip"
if [ ! -f "$zip" ]; then
  curl -fsSL -o "$zip" "https://github.com/basil00/WinDivert/releases/download/v${WINDIVERT_VERSION}/WinDivert-${WINDIVERT_VERSION}-A.zip"
fi
rm -rf build/windivert && mkdir -p build/windivert
unzip -q -o "$zip" -d build/windivert
dll=$(find build/windivert -type f -path '*/x64/WinDivert.dll' | head -n 1)
sys=$(find build/windivert -type f -path '*/x64/WinDivert64.sys' | head -n 1)
if [ -z "$dll" ] || [ -z "$sys" ]; then
  echo "WinDivert x64 files not found in $zip" >&2
  exit 1
fi

# The manifest asks for administrator rights: WinDivert needs them.
go run "github.com/tc-hib/go-winres@${GO_WINRES_VERSION}" make \
  --in winres/winres.json --arch amd64 --out cmd/ashgate-lite/rsrc

CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -H windowsgui" -o dist/ashgate-lite.exe ./cmd/ashgate-lite
cp "$dll" dist/WinDivert.dll
cp "$sys" dist/WinDivert64.sys
echo "dist/: ashgate-lite.exe WinDivert.dll WinDivert64.sys"
