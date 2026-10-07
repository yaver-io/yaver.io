#!/bin/sh
set -eu
cd "$(dirname "$0")/core"
target=${1:-ios}
mkdir -p ../build ../.tools
# Both tools are pinned in go.mod. Keep executables local to this package.
GOBIN="$(cd ../.tools && pwd)" go install golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
export PATH="$(cd ../.tools && pwd):$PATH"
case "$target" in
  ios) gomobile bind -target=ios,iossimulator -iosversion=15.1 -o ../build/PlainSSH.xcframework . ;;
  android) gomobile bind -target=android -androidapi=23 -o ../build/plainssh.aar . ;;
  *) echo 'Usage: build.sh ios|android' >&2; exit 2 ;;
esac
