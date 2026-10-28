#!/usr/bin/env bash
# Build the development Linux AppImage from the binaries scripts/build-linux.sh
# produced. Development is the default and the only mode that does not run the
# licensing release gate; `--release` is the explicit public-release path and
# fails closed, exactly like scripts/build-linux.sh.
#
# What goes in: the supervisor and the optional GTK/WebKitGTK window component,
# the launcher entry with the approved icon, the license and third-party notices,
# and a BUILD-INFO file naming the version and variant. What does not: GTK,
# WebKitGTK, bubblewrap, glibc or any other system library. GTK/WebKitGTK are
# loaded from the host, because a bundled copy would need WebKit's own helper
# binaries at a fixed path, its sandbox to keep working from inside a FUSE mount,
# and the LGPL's corresponding-source and relink obligations. The application
# still falls back to the authenticated browser when the host has no GTK/WebKitGTK.
#
# Reproducible: two builds of the same commit with the same toolchain produce a
# byte-identical AppImage. Every inode timestamp in the squashfs is the single
# documented source date (SOURCE_DATE_EPOCH, else the commit time), and the
# binaries are checked against the provenance record the compiler step wrote, so
# neither the clock nor a stale binary can reach the artifact unnoticed.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
arch=x86_64
release=0
for arg in "$@"; do
  case "$arg" in
    amd64|x86_64) arch=x86_64 ;;
    --release) release=1 ;;
    *) echo "Unknown argument: $arg (expected amd64, x86_64 or --release)" >&2; exit 1 ;;
  esac
done
[[ "$arch" == x86_64 ]] || { echo 'The AppImage is built for x86_64 only' >&2; exit 1; }

for tool in unsquashfs sha256sum; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
appimagetool=${APPIMAGETOOL:-}
if [[ -z "$appimagetool" ]]; then
  appimagetool=$(command -v appimagetool || true)
fi
if [[ -z "$appimagetool" || ! -x "$appimagetool" ]]; then
  cat >&2 <<'MSG'
appimagetool is required to build the AppImage. Install it, or point APPIMAGETOOL
at a reviewed executable. The repository initializer downloads immutable tool and
runtime versions and verifies both SHA-256 digests before execution:

  bash validation/linux/Initialize-SparrowAppImageTool.sh --directory /tmp/sparrow-appimage-toolchain
MSG
  exit 1
fi

cd "$repo"
for binary in dist/linux/radanor-sparrow dist/linux/radanor-sparrow-window; do
  [[ -x "$binary" ]] || {
    echo "Missing $binary. Run scripts/build-linux.sh first (it builds the window component when GTK/WebKitGTK development files are present)." >&2
    exit 1
  }
done
for doc in LICENSE THIRD_PARTY_NOTICES.md; do
  [[ -f "$doc" ]] || { echo "Missing release document: $doc" >&2; exit 1; }
done
appstream=packaging/appstream/radanor-sparrow.metainfo.xml
[[ -f "$appstream" ]] || {
  echo "Missing $appstream. It names the application for software centres and is packaged into the image; see docs/deployment/LINUX_INSTALL_GUIDE.md." >&2
  exit 1
}

export GOTOOLCHAIN=local
if (( release )); then
  echo 'Release build: validating licensing metadata before packaging.'
  go run ./cmd/radanor-release-check || {
    echo 'Release blocked: licensing metadata is incomplete. Refusing to produce a public AppImage.' >&2
    exit 1
  }
  version=$(go run ./cmd/radanor-version -release)
  variant=$(go run ./cmd/radanor-version -release -field variant)
else
  version=$(go run ./cmd/radanor-version)
  variant=$(go run ./cmd/radanor-version -field variant)
fi
if (( release )); then
  [[ "$variant" == release ]] || { echo "Refusing to package a $variant build as a release AppImage" >&2; exit 1; }
else
  [[ "$variant" == development ]] || {
    echo "Refusing to package a $variant build as a development AppImage; use --release after the gate passes." >&2
    exit 1
  }
fi

# The packaging step's provenance check. dist/linux/BUILD-INFO is written by
# scripts/build-linux.sh next to the binaries it describes. Without it the
# metadata in this AppImage could name a version the binaries were not built
# with — a tree that moved on, or a binary swapped after the build — so the
# mismatch is refused instead of being packaged.
provenance=dist/linux/BUILD-INFO
if [[ ! -f "$provenance" ]]; then
  echo "Missing $provenance. Run scripts/build-linux.sh, which records the version, variant and binary checksums this step verifies." >&2
  exit 1
fi
prov() { awk -F= -v key="$1" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$provenance"; }
built_version=$(prov version)
if [[ -z "$built_version" ]]; then
  echo "$provenance does not record a version. Rebuild with scripts/build-linux.sh." >&2
  exit 1
fi
if [[ "$built_version" != "$version" ]]; then
  echo "Refusing to package: the binaries in dist/linux are $built_version, but this tree is $version. Rebuild with scripts/build-linux.sh so the metadata matches the binaries." >&2
  exit 1
fi
if [[ "$(prov variant)" != "$variant" ]]; then
  echo "Refusing to package: the binaries in dist/linux are a $(prov variant) build, but this step is producing a $variant AppImage. Rebuild with scripts/build-linux.sh." >&2
  exit 1
fi
# GOARCH names the same machine x86_64 in AppImage metadata; the record keeps the
# Go name because that is what the compiler was told.
if [[ "$(prov architecture)" != amd64 ]]; then
  echo "Refusing to package: the binaries in dist/linux were built for $(prov architecture), not amd64." >&2
  exit 1
fi
for name in radanor-sparrow radanor-sparrow-window; do
  recorded=$(prov "binary.$name")
  if [[ -z "$recorded" ]]; then
    echo "Refusing to package: $provenance does not record $name, so the packaged binary cannot be tied to the build that produced it. Rebuild with scripts/build-linux.sh." >&2
    exit 1
  fi
  actual=$(sha256sum -- "dist/linux/$name" | cut -d' ' -f1)
  if [[ "$recorded" != "$actual" ]]; then
    echo "Refusing to package: dist/linux/$name changed after it was built (recorded $recorded, found $actual). Rebuild with scripts/build-linux.sh." >&2
    exit 1
  fi
done

# One documented timestamp for the whole artifact, from the same rule the
# compiler step used. appimagetool passes it to mksquashfs as the all-time value,
# which is why the inode table — and therefore every byte of the image — stops
# depending on when the build ran.
epoch=$(go run ./cmd/radanor-version -field epoch)
epoch_source=$(go run ./cmd/radanor-version -field epoch-source)
export SOURCE_DATE_EPOCH="$epoch"
echo "Timestamp: $epoch from $epoch_source"

appdir=dist/linux/RadanorSparrow.AppDir
rm -rf -- "$appdir"
mkdir -p -- "$appdir/usr/bin" "$appdir/usr/share/applications" \
  "$appdir/usr/share/icons/hicolor/16x16/apps" \
  "$appdir/usr/share/icons/hicolor/32x32/apps" \
  "$appdir/usr/share/icons/hicolor/48x48/apps" \
  "$appdir/usr/share/icons/hicolor/128x128/apps" \
  "$appdir/usr/share/icons/hicolor/256x256/apps" \
  "$appdir/usr/share/doc/radanor-sparrow" \
  "$appdir/usr/share/metainfo" \
  "$appdir/usr/share/radanor-sparrow"

install -m 755 -- dist/linux/radanor-sparrow "$appdir/usr/bin/radanor-sparrow"
install -m 755 -- dist/linux/radanor-sparrow-window "$appdir/usr/bin/radanor-sparrow-window"
for size in 16 32 48 128 256; do
  install -m 644 -- "web/public/favicon-${size}x${size}.png" \
    "$appdir/usr/share/icons/hicolor/${size}x${size}/apps/radanor-sparrow.png"
done
install -m 644 -- LICENSE THIRD_PARTY_NOTICES.md "$appdir/usr/share/doc/radanor-sparrow/"
# AppStream metadata, so a catalogue or app centre can describe the application
# without network access. It is our own file: --no-appstream below still stops
# appimagetool from fetching or inventing metadata of its own.
install -m 644 -- packaging/appstream/radanor-sparrow.metainfo.xml \
  "$appdir/usr/share/metainfo/radanor-sparrow.metainfo.xml"
# The approved artwork, unrecoloured and uncropped: the runtime icon is the
# largest launcher raster in the set, which GNOME and KDE need at 256 px. The
# 128/256 files are derived from the 48 px master by the documented, pinned
# process in scripts/build-icons.py, and they keep the same composition.
install -m 644 -- web/public/favicon-256x256.png "$appdir/radanor-sparrow.png"
ln -sf -- radanor-sparrow.png "$appdir/.DirIcon"

# The same identity the installed window and launcher use: one program name for
# the X11 WM_CLASS / Wayland app_id, the entry and the icon.
cat > "$appdir/radanor-sparrow.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Radanor Sparrow
GenericName=Security Posture Monitoring
Comment=Local security posture and monitoring
Exec=radanor-sparrow
Terminal=false
Icon=radanor-sparrow
Categories=System;Security;
Keywords=security;monitoring;posture;radanor;sparrow;
StartupNotify=true
StartupWMClass=radanor-sparrow
X-AppImage-Version=${version}
X-Radanor-Variant=${variant}
EOF
install -m 644 -- "$appdir/radanor-sparrow.desktop" "$appdir/usr/share/applications/radanor-sparrow.desktop"

cat > "$appdir/AppRun" <<'EOF'
#!/bin/sh
# The runtime exports APPDIR and mounts this image; the executable and the window
# component sit beside each other in usr/bin, which is the only place the
# supervisor looks for the component. Nothing is added to LD_LIBRARY_PATH: GTK and
# WebKitGTK come from the host, so their own subprocess sandbox and the host's
# library security updates keep working.
set -eu
if [ -z "${APPDIR:-}" ]; then
  APPDIR=$(dirname -- "$(readlink -f -- "$0")")
  export APPDIR
fi
exec "$APPDIR/usr/bin/radanor-sparrow" "$@"
EOF
chmod 755 "$appdir/AppRun"

# Build metadata a validator can read without running the application. The
# variant is stated so a development AppImage cannot be mistaken for a release,
# the source date records which timestamp its files carry and where that value
# came from, and the binary checksums tie the packaged executables back to the
# recorded build inputs rather than to whatever happens to sit in dist/linux.
cat > "$appdir/usr/share/radanor-sparrow/BUILD-INFO" <<EOF
version=${version}
variant=${variant}
artifact=AppImage
architecture=${arch}
source_date_epoch=${epoch}
source_date_epoch_source=${epoch_source}
$(awk -F= '/^binary\./ { print }' "$provenance")
EOF

if command -v desktop-file-validate >/dev/null; then
  desktop-file-validate "$appdir/radanor-sparrow.desktop"
fi

out="dist/linux/RadanorSparrow-${version}-${arch}.AppImage"
rm -f -- "$out"
# The type-2 runtime is an input like the compiler: unless it is pinned here,
# appimagetool downloads the continuous runtime, so a later upstream build of it
# would change the AppImage of an unchanged commit. Set APPIMAGE_RUNTIME to a
# checksummed copy on a build farm that must be reproducible and offline.
runtime_args=()
if [[ -n "${APPIMAGE_RUNTIME:-}" ]]; then
  if [[ ! -f "$APPIMAGE_RUNTIME" || ! -x "$APPIMAGE_RUNTIME" ]]; then
    echo "APPIMAGE_RUNTIME=$APPIMAGE_RUNTIME is not an executable file" >&2
    exit 1
  fi
  runtime_args=(--runtime-file "$APPIMAGE_RUNTIME")
else
  if (( release )); then
    echo 'Release AppImage requires a verified APPIMAGE_RUNTIME; refusing appimagetool network fallback.' >&2
    exit 1
  fi
  echo 'Note: appimagetool fetches its type-2 runtime; set APPIMAGE_RUNTIME to pin it for reproducible offline builds.' >&2
fi
# --no-appstream: this project has no AppStream metadata, and the AppImage must
# not gain one implicitly from the build host's network. SOURCE_DATE_EPOCH is
# what makes appimagetool normalize the squashfs timestamps.
#
# Absolute paths: the reviewed appimagetool 1.9.1 rejects a relative SOURCE (it
# reports `no such file or directory` for a directory that exists), and its own
# working directory is not this checkout once it unpacks itself. Resolving both
# paths here keeps the pinned tool usable and the output in dist/linux rather
# than wherever the tool happens to be standing.
appdir_abs=$(cd -- "$appdir" && pwd)
out_abs=$(cd -- "$(dirname -- "$out")" && pwd)/$(basename -- "$out")
ARCH="$arch" SOURCE_DATE_EPOCH="$epoch" "$appimagetool" "${runtime_args[@]}" --no-appstream "$appdir_abs" "$out_abs"
chmod 755 -- "$out"

# Validate the artifact that was produced, without FUSE: the runtime prints the
# squashfs offset, and unsquashfs can list the image from there.
offset=$("$out" --appimage-offset)
unsquashfs -o "$offset" -ll "$out" >/dev/null

if (( release )); then
  go run ./cmd/radanor-release-check --release-dir dist/linux || {
    echo 'Release blocked: the packaged artifact is missing required licensing documents.' >&2
    exit 1
  }
  echo 'Release AppImage: licensing gate passed.'
else
  echo 'Development AppImage: NOT a release artifact; the licensing release gate was not run.'
fi
sha256sum -- "$out"
stat -c 'size=%s bytes' -- "$out"
