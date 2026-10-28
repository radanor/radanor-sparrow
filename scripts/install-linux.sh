#!/usr/bin/env bash
# Per-user installation; run from the source checkout after building.
#
#   bash scripts/install-linux.sh              install or refresh
#   bash scripts/install-linux.sh --uninstall  remove the installed files
#
# Everything stays inside the user's own home: no system directory, no service, no
# elevation, and no terminal needed afterwards — the Applications menu or the dock
# launches the product. The launcher entry names the icon and the window class the
# native window actually presents, so the running window is matched to its entry
# instead of appearing as a second, generic item.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

mode=install
case "${1:-}" in
  --uninstall) mode=uninstall ;;
  --help|-h)
    echo 'Usage: bash scripts/install-linux.sh [--uninstall]'
    echo 'Installs Radanor Sparrow for this user only: the executable and the optional native window component into ~/.local/bin,'
    echo 'a launcher entry with the approved icon, and the license and third-party notices into ~/.local/share/doc/radanor-sparrow.'
    exit 0 ;;
  '') ;;
  *) echo "Unknown argument: $1 (expected --uninstall or --help)" >&2; exit 1 ;;
esac

[[ $(id -u) != 0 ]] || { echo 'Install as your normal user, without sudo' >&2; exit 1; }

bindir="$HOME/.local/bin"
applications="$HOME/.local/share/applications"
icons="$HOME/.local/share/icons/hicolor"
docdir="$HOME/.local/share/doc/radanor-sparrow"
target="$bindir/radanor-sparrow"
component="$bindir/radanor-sparrow-window"
entry="$applications/radanor-sparrow.desktop"
appid=radanor-sparrow
# The window class the native window presents: the program name internal/gtkwindow
# sets for GTK, which a desktop shell matches against this entry. Both values are
# pinned by a test, so the installer cannot drift away from the window.
wmclass=radanor-sparrow

# Desktop entries and hicolor icon directories cannot express these safely.
for path in "$target" "$component" "$entry" "$docdir" "$icons"; do
  [[ "$path" != *$'\n'* && "$path" != *$'\r'* && "$path" != *$'\t'* && "$path" != *'='* && "$path" != *'%'* ]] || { echo 'Home path cannot contain control characters, = or % for desktop installation' >&2; exit 1; }
done

refresh_caches() {
  # Best effort. Both tools ship with a normal Ubuntu desktop; a missing one, or a
  # theme directory without an index, leaves a working entry either way, so neither
  # failure is allowed to fail an installation or an uninstall.
  if command -v update-desktop-database >/dev/null; then
    update-desktop-database -q "$applications" 2>/dev/null || true
  fi
  if command -v gtk-update-icon-cache >/dev/null; then
    gtk-update-icon-cache -q -t -f "$icons" 2>/dev/null || true
  fi
}

remove_if_present() {
  if [[ -e "$1" ]]; then
    rm -f -- "$1"
    echo "Removed: $1"
  fi
}

if [[ "$mode" == uninstall ]]; then
  remove_if_present "$entry"
  remove_if_present "$component"
  remove_if_present "$target"
  # Only the sizes the installer writes, and only under our own name.
  for size in 16x16 32x32 48x48 128x128 256x256; do
    remove_if_present "$icons/$size/apps/$appid.png"
  done
  if [[ -d "$docdir" ]]; then
    rm -rf -- "$docdir"
    echo "Removed: $docdir"
  fi
  refresh_caches
  echo 'Your monitoring history and settings are left in place; the private state directory is documented in docs/deployment/DESKTOP_DELIVERY.md.'
  exit 0
fi

mkdir -p -- "$bindir" "$applications" "$docdir"
install -m 755 -- "$repo/dist/linux/radanor-sparrow" "$target"

# The native window component is optional by design: it needs GTK3/WebKitGTK 4.1 at
# runtime, and without it the product opens the same authenticated dashboard in the
# default browser. Installing it is therefore reported, never required.
if [[ -x "$repo/dist/linux/radanor-sparrow-window" ]]; then
  install -m 755 -- "$repo/dist/linux/radanor-sparrow-window" "$component"
else
  echo 'No native window component in dist/linux: the authenticated dashboard will open in the default browser.' >&2
  echo 'Build it with gcc, pkg-config, libgtk-3-dev and libwebkit2gtk-4.1-dev installed, then install again.' >&2
fi

# The approved Radanor application artwork, at the sizes the approved files carry.
# These are the same pixels the native window's own icon uses, so a launcher cannot
# show different artwork from the window it starts.
icon_source="$repo/web/public"
install_icon() {
  local source="$icon_source/$1" directory="$icons/$2/apps"
  [[ -f "$source" ]] || { echo "Missing approved icon: $source" >&2; exit 1; }
  install -d -m 755 -- "$directory"
  install -m 644 -- "$source" "$directory/$appid.png"
}
install_icon favicon-16x16.png 16x16
install_icon favicon-32x32.png 32x32
install_icon favicon-48x48.png 48x48
install_icon favicon-128x128.png 128x128
install_icon favicon-256x256.png 256x256

# Install the license and third-party notices beside the installed product so an
# installed copy carries the same documents as the release artifact directory.
for doc in LICENSE THIRD_PARTY_NOTICES.md; do
  [[ -f "$repo/$doc" ]] || { echo "Missing release document: $doc" >&2; exit 1; }
done
install -m 644 -- "$repo/LICENSE" "$docdir/LICENSE"
install -m 644 -- "$repo/THIRD_PARTY_NOTICES.md" "$docdir/THIRD_PARTY_NOTICES.md"

# Desktop-file quoting escapes backslashes, quotes, dollars and backticks.
escaped=${target//\\/\\\\\\\\}
escaped=${escaped//\"/\\\\\"}
escaped=${escaped//\$/\\\\\$}
escaped=${escaped//\`/\\\\\`}
escaped=${escaped//%/%%}
printf '[Desktop Entry]\nType=Application\nName=Radanor Sparrow\nGenericName=Security Posture Monitoring\nComment=Local security posture and monitoring\nExec="%s"\nTerminal=false\nIcon=%s\nCategories=System;Security;\nKeywords=security;monitoring;posture;radanor;sparrow;\nStartupNotify=true\nStartupWMClass=%s\n' \
  "$escaped" "$appid" "$wmclass" > "$entry"

# Validate the entry that was just written when the tool is available: a launcher
# that only looks installed is worse than a reported failure.
if command -v desktop-file-validate >/dev/null; then
  desktop-file-validate "$entry"
fi
refresh_caches

echo "Installed: $target"
if [[ -x "$component" ]]; then
  echo "Installed native window component: $component"
fi
echo "Launcher entry: $entry (icon $appid)"
echo "License and third-party notices: $docdir"
echo 'Launch "Radanor Sparrow" from the Applications menu or the dock; no terminal is needed.'
