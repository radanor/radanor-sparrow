#!/usr/bin/env bash
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
# Every path below belongs to the checkout, not to the directory the caller
# happens to be in. Without this, a caller outside the repository (the
# reproducibility check builds a clone while standing in the working tree) would
# read its version and write its output in the wrong tree, so a clean clone could
# be packaged as `.dirty`.
cd "$repo"
# Development build by default; `--release` is the explicit public release path,
# which runs the licensing release gate and fails closed.
arch=amd64
release=0
appimage=0
for arg in "$@"; do
  case "$arg" in
    amd64|arm64) arch=$arg ;;
    --release) release=1 ;;
    --appimage) appimage=1 ;;
    *) echo "Unknown argument: $arg (expected amd64, arm64, --release or --appimage)" >&2; exit 1 ;;
  esac
done
if (( appimage )) && [[ "$arch" != amd64 ]]; then
  echo 'The AppImage is packaged for amd64 only' >&2
  exit 1
fi
for tool in go node npm; do
  command -v "$tool" >/dev/null || { echo "Missing build prerequisite: $tool. See docs/deployment/DESKTOP_DELIVERY.md" >&2; exit 1; }
done
export GOTOOLCHAIN=local
if (( release )); then
  echo 'Release build: validating licensing metadata before building.'
  go run ./cmd/radanor-release-check || {
    echo 'Release blocked: licensing metadata is incomplete. Refusing to produce a public release artifact.' >&2
    exit 1
  }
fi
gover=$(go version | awk '{print $3}')
[[ "$gover" =~ ^go1\.([0-9]+)\. ]] && (( BASH_REMATCH[1] >= 27 )) || { echo 'Go 1.27 or newer is required' >&2; exit 1; }
[[ $(node --version) == v24.* ]] || { echo 'Node.js 24 LTS is required' >&2; exit 1; }
# The product version is derived from the checked-out commit, never edited into
# the source. A release build additionally requires an exact, clean `vX.Y.Z`
# tag, so an approved release cannot be assembled from an arbitrary working
# tree. This is stricter than the licensing gate, never a way around it.
if (( release )); then
  version=$(go run ./cmd/radanor-version -release) || {
    echo 'Release blocked: a public release must be built from a clean checkout of an exact vX.Y.Z tag.' >&2
    exit 1
  }
  variant=$(go run ./cmd/radanor-version -release -field variant)
else
  version=$(go run ./cmd/radanor-version)
  variant=$(go run ./cmd/radanor-version -field variant)
fi
echo "Build version: $version ($variant build)"
# Reproducibility: every timestamp written into a generated file comes from one
# documented source, resolved by the version tool so the AppImage packaging step
# cannot choose a different one. Unset, it is the commit's own committer time, so
# two builds of the same commit agree; the current clock is never used, because
# it cannot be reproduced. SOURCE_DATE_EPOCH set by a release engineer wins.
epoch=$(go run ./cmd/radanor-version -field epoch)
epoch_source=$(go run ./cmd/radanor-version -field epoch-source)
echo "Timestamp: $epoch from $epoch_source"
# Not exported here: the AppImage step resolves the same rule a moment before it
# needs it, so the artifact records the real provenance (a commit, or an
# explicitly set SOURCE_DATE_EPOCH) instead of "inherited from the build script".
cd "$repo/web"
npm ci --ignore-scripts
npm run build
cd "$repo"
mkdir -p dist/linux
# Remove the previous outputs before building. When the inputs are unchanged the
# compiler is served from its build cache, and restoring that cache entry leaves a
# file that already sits at the output path alone — including bytes appended to it
# after the last build. A binary that is packaged must be the compiler's output,
# not a file that merely has the right name.
rm -f -- dist/linux/radanor-sparrow dist/linux/radanor-sparrow-window
# Injected build metadata, so the artifact states the commit it came from and
# whether it was produced by the release path. Nothing else about the build
# changes: the binary stays CGO-free, trimmed and without VCS stamping.
ldflags="-X github.com/radanor/radanor-shield/internal/branding.Version=$version -X github.com/radanor/radanor-shield/internal/branding.Variant=$variant"
GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -buildvcs=false -ldflags "$ldflags" -o dist/linux/radanor-sparrow ./cmd/radanor-sparrow
chmod 755 dist/linux/radanor-sparrow
# The native Ubuntu window is a separate component: the product above stays
# CGO-free and static, and a machine without GTK/WebKitGTK still runs it with the
# authenticated browser, because the supervisor starts the component only when it
# sits beside the executable. Building it here is what makes the documented build
# path produce the documented desktop experience. Nothing is installed
# system-wide. A plain build reports a missing development header and keeps the
# browser path; --appimage and --release require the component instead, because
# the AppImage is the native desktop artifact and a public release ships it (see
# the check below).
window=
if [[ "$arch" != "$(go env GOHOSTARCH)" ]]; then
  echo "Native window component not built: a $arch build needs a cgo cross toolchain, so this build produces the browser path only." >&2
elif ! command -v pkg-config >/dev/null || ! pkg-config --exists gtk+-3.0 webkit2gtk-4.1; then
  echo 'Native window component not built: install gcc, pkg-config, libgtk-3-dev and libwebkit2gtk-4.1-dev to include it; without it the supervisor opens the authenticated dashboard in the default browser.' >&2
else
  GOTOOLCHAIN=local CGO_ENABLED=1 GOOS=linux GOARCH="$arch" go build -tags gtk -trimpath -buildvcs=false -ldflags "$ldflags" -o dist/linux/radanor-sparrow-window ./cmd/radanor-sparrow-window
  chmod 755 dist/linux/radanor-sparrow-window
  window=dist/linux/radanor-sparrow-window
fi
# Neither the AppImage nor a public release may quietly become the browser-only
# product. Those two paths claim to deliver the native desktop experience, so a
# missing helper stops the build rather than producing a differently-functioning
# artifact under the same name. A plain development build is unaffected: it still
# reports the omission and produces the portable, browser-first executable.
if [[ -z "$window" ]] && (( appimage || release )); then
  cat >&2 <<'MSG'
Refusing to build: --appimage and --release require the native window component,
and it could not be built here.

Install gcc, pkg-config, libgtk-3-dev and libwebkit2gtk-4.1-dev, then build again.
Without the component the supervisor opens the authenticated dashboard in the
default browser, which is the portable artifact, not the native-window one these
build paths produce.
MSG
  exit 1
fi
# A release must distribute the license and the third-party notices beside the
# artifact: the Business Source License requires the license to accompany copies
# of the Licensed Work, and the MIT/BSD-3 dependencies require their notices and
# license texts to be reproduced with the distribution. This adds two documents
# next to the single executable; it does not change the executable itself.
# Provenance for the packaging step, written beside the binaries it describes.
# The AppImage script refuses binaries whose version, variant or checksum does
# not match this record, so a new commit or an edited binary cannot be packaged
# under the old version or in a clean-looking artifact. Key order is fixed and
# every value is derived from the source or the binary contents, never from the
# clock, so the file itself is reproducible.
provenance=dist/linux/BUILD-INFO
{
  echo "version=$version"
  echo "variant=$variant"
  echo "architecture=$arch"
  echo "source_date_epoch=$epoch"
  echo "source_date_epoch_source=$epoch_source"
  for binary in radanor-sparrow radanor-sparrow-window; do
    if [[ -f "dist/linux/$binary" ]]; then
      echo "binary.$binary=$(sha256sum -- "dist/linux/$binary" | cut -d' ' -f1)"
    fi
  done
} > "$provenance"
for doc in LICENSE THIRD_PARTY_NOTICES.md; do
  [[ -f "$doc" ]] || { echo "Missing release document: $doc" >&2; exit 1; }
done
install -m 644 -- LICENSE dist/linux/LICENSE
install -m 644 -- THIRD_PARTY_NOTICES.md dist/linux/THIRD_PARTY_NOTICES.md
if (( release )); then
  # Validate the package that was actually produced, not just the source.
  go run ./cmd/radanor-release-check --release-dir dist/linux || {
    echo 'Release blocked: the packaged artifact is missing required licensing documents.' >&2
    exit 1
  }
  echo 'Release build: licensing gate passed.'
else
  echo 'Development build: NOT a release artifact; the licensing release gate was not run.' >&2
fi
if (( appimage )); then
  # The AppImage is produced from exactly these binaries, so it is built after
  # them and never as a substitute for them. The packaging step re-checks the
  # licensing gate itself when --release is passed.
  args=()
  (( release )) && args+=(--release)
  bash "$repo/scripts/build-appimage.sh" "${args[@]}"
fi
if [[ -n "$window" ]]; then
  sha256sum dist/linux/radanor-sparrow dist/linux/LICENSE dist/linux/THIRD_PARTY_NOTICES.md "$window"
else
  sha256sum dist/linux/radanor-sparrow dist/linux/LICENSE dist/linux/THIRD_PARTY_NOTICES.md
fi
