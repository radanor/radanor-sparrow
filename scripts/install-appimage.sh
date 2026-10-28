#!/usr/bin/env bash
# Per-user desktop integration for the Radanor Sparrow AppImage.
#
#   bash install-appimage.sh [--release] [--checksums FILE] \
#       [--signature FILE --fingerprint FPR (--public-key FILE | --keyring DIR)] \
#       [path/to/RadanorSparrow-*.AppImage]
#   bash install-appimage.sh --uninstall
#   bash install-appimage.sh --help
#
# The AppImage itself needs no installation: this only puts it somewhere stable
# (~/.local/bin), writes the launcher entry and the approved icons from the
# image's own payload, and copies the license documents the image carries.
# Everything stays in the user's home: no root, no system directory, no service.
#
# It does not need a source checkout. The icons and the documents are read from
# the AppImage being installed (the runtime's --appimage-extract, which needs no
# FUSE and no mount), so the file that is downloaded is the only input.
#
# Variant check. A build says whether it is a development or a release artifact in
# its own BUILD-INFO. This script is the development path by default, so it
# installs a development artifact and refuses a release one unless --release is
# given; --release refuses a development artifact. Upgrading a development
# installation with a release build therefore cannot happen by accident.
#
# The AppImage remains the unit that runs: the launcher entry points at the
# installed image, so an upgrade is a single file replacement, and --uninstall
# removes exactly what this script wrote while leaving monitoring data in place.
#
# Signature check (optional). --signature verifies a detached signature over the
# downloaded image before the runtime is executed or anything is extracted, and
# requires the full fingerprint you checked out of band. A public key that merely
# arrived beside the download is never trusted by itself: either you name the
# fingerprint, or you point at a keyring that already holds the key.
set -euo pipefail

mode=install
expectation=development
source=
checksums=
signature=
public_key=
fingerprint=
keyring=
verify_home=
extract_dir=
stage=
# while/shift, not `for arg in "$@"`: an option that takes a value has to consume
# it, or the value is parsed again as a positional argument.
while (( $# )); do
  case "$1" in
    --uninstall) mode=uninstall; shift ;;
    --release) expectation=release; shift ;;
    --development) expectation=development; shift ;;
    --checksums)
      # A checksum file is optional and never fetched: the publisher's manifest
      # that shipped beside the image, or one you obtained yourself.
      (( $# >= 2 )) || { echo '--checksums needs the checksum file that came with the image' >&2; exit 1; }
      checksums=$2
      shift 2 ;;
    --signature)
      (( $# >= 2 )) || { echo '--signature needs the detached signature that came with the image' >&2; exit 1; }
      signature=$2
      shift 2 ;;
    --public-key)
      (( $# >= 2 )) || { echo '--public-key needs a public key file' >&2; exit 1; }
      public_key=$2
      shift 2 ;;
    --fingerprint)
      (( $# >= 2 )) || { echo '--fingerprint needs the full 40-hex fingerprint you verified out of band' >&2; exit 1; }
      fingerprint=$2
      shift 2 ;;
    --keyring)
      (( $# >= 2 )) || { echo '--keyring needs a GnuPG home directory that already holds the trusted key' >&2; exit 1; }
      keyring=$2
      shift 2 ;;
    --help|-h)
      cat <<'MSG'
Usage: bash install-appimage.sh [--release] [--checksums FILE]
                                [--signature FILE --fingerprint FPR
                                 (--public-key FILE | --keyring DIR)]
                                [path/to/RadanorSparrow-*.AppImage]
       bash install-appimage.sh --uninstall

Installs the AppImage for this user only: the image into ~/.local/bin, a launcher
entry, the approved icons the image carries, and its license documents. No root, no
source checkout, no system directory and no network access are involved.

  --release        the image must be a release artifact (variant=release)
  --development    the image must be a development artifact (the default)
  --checksums FILE verify the image against FILE (SHA256SUMS.appimage) first
  --signature FILE verify FILE (the image's .asc) before the image is unpacked
  --fingerprint FPR the 40-hex fingerprint you verified out of band (required
                   with --signature; a key beside the download proves nothing)
  --public-key FILE the public key to check the signature with
  --keyring DIR    a GnuPG home that already holds the trusted key
  --uninstall      remove what this script installed; user data is kept

Without an AppImage path, the newest image in dist/linux is used, if there is one.
A development image is installed by default; a release image is refused unless
--release is passed, so a release build is never installed through the development
path by accident.

A checksum proves the file arrived unchanged. It does not prove who published it;
that needs a signature, which is documented in docs/deployment/RELEASE_SIGNING.md.
A signature is verified before the image is unpacked or run, and only against the
fingerprint you name.
MSG
      exit 0 ;;
    -*)
      echo "Unknown argument: $1 (expected --release, --development, --checksums FILE, --signature FILE, --fingerprint FPR, --public-key FILE, --keyring DIR, --uninstall, --help or an AppImage path)" >&2
      exit 1 ;;
    *) source=$1; shift ;;
  esac
done

# Signature options are opt-in, and a signature is only accepted with an explicit
# trust anchor: the fingerprint you verified out of band, or a keyring that already
# holds the key. Nothing beside the download is trusted because it is beside the
# download.
trusted=
if [[ -n "$signature" || -n "$public_key" || -n "$fingerprint" || -n "$keyring" ]]; then
  [[ -n "$signature" ]] || { echo '--signature FILE is required when any signature option is given.' >&2; exit 1; }
  [[ -s "$signature" ]] || { echo "Signature file is missing or empty: $signature" >&2; exit 1; }
  if [[ -z "$fingerprint" ]]; then
    cat >&2 <<'MSG'
A signature needs --fingerprint: the full 40-hex fingerprint of the release key,
checked out of band (release notes, the repository, the project site). A key that
arrived beside the download is never trusted on its own, so nothing was installed.
MSG
    exit 1
  fi
  trusted=$(tr -d ' :' <<<"$fingerprint" | tr '[:lower:]' '[:upper:]')
  [[ $trusted =~ ^[0-9A-F]{40}$ ]] || {
    echo "--fingerprint must be a full 40-hex OpenPGP fingerprint, not a key id or a name: $fingerprint" >&2
    exit 1
  }
  if [[ -z "$public_key" && -z "$keyring" ]]; then
    echo 'A signature needs --public-key FILE or --keyring DIR: the key that is allowed to vouch for this file.' >&2
    exit 1
  fi
  if [[ -n "$public_key" && -n "$keyring" ]]; then
    echo 'Pass either --public-key FILE or --keyring DIR, not both: two trust sources would make the result ambiguous.' >&2
    exit 1
  fi
  if [[ -n "$public_key" && ! -s "$public_key" ]]; then
    echo "Public key file is missing or empty: $public_key" >&2
    exit 1
  fi
  if [[ -n "$keyring" && ! -d "$keyring" ]]; then
    echo "Keyring directory does not exist: $keyring" >&2
    exit 1
  fi
  command -v gpg >/dev/null || { echo 'gpg is required to verify a signature; install it or omit --signature.' >&2; exit 1; }
fi

bindir="$HOME/.local/bin"
applications="$HOME/.local/share/applications"
icons="$HOME/.local/share/icons/hicolor"
docdir="$HOME/.local/share/doc/radanor-sparrow"
target="$bindir/radanor-sparrow.AppImage"
entry="$applications/radanor-sparrow.desktop"
appid=radanor-sparrow
# The window class the native window presents, which a desktop shell matches
# against this entry. It is the same value the AppImage's own entry uses.
wmclass=radanor-sparrow

refresh_caches() {
  # Best effort: both tools ship with a normal Ubuntu desktop, and a missing one
  # or a theme directory without an index still leaves a working entry.
  if command -v update-desktop-database >/dev/null; then
    update-desktop-database -q "$applications" 2>/dev/null || true
  fi
  if command -v gtk-update-icon-cache >/dev/null; then
    gtk-update-icon-cache -q -t -f "$icons" 2>/dev/null || true
  fi
}

if [[ "$mode" == uninstall ]]; then
  [[ $(id -u) != 0 ]] || { echo 'Run this as your normal user, without sudo' >&2; exit 1; }
  if [[ -e "$entry" ]]; then rm -f -- "$entry" && echo "Removed: $entry"; fi
  if [[ -e "$target" ]]; then rm -f -- "$target" && echo "Removed: $target"; fi
  # Only the sizes this script writes, and only under our own name.
  for size in 16x16 32x32 48x48 128x128 256x256; do
    icon="$icons/$size/apps/$appid.png"
    if [[ -e "$icon" ]]; then rm -f -- "$icon" && echo "Removed: $icon"; fi
  done
  if [[ -d "$docdir" ]]; then
    rm -rf -- "$docdir"
    echo "Removed: $docdir"
  fi
  refresh_caches
  echo 'Your monitoring history and settings are left in place; the private state directory is documented in docs/deployment/LINUX_INSTALL_GUIDE.md.'
  exit 0
fi

if [[ -z "$source" ]]; then
  repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
  source=$(ls -1t "$repo"/dist/linux/RadanorSparrow-*-x86_64.AppImage 2>/dev/null | head -1 || true)
fi
[[ -n "$source" ]] || {
  cat >&2 <<'MSG'
No AppImage given and none found in dist/linux.
Pass the file explicitly, for example:

  bash install-appimage.sh --release RadanorSparrow-1.0.0-x86_64.AppImage
MSG
  exit 1
}
[[ -f "$source" ]] || { echo "Not a file: $source" >&2; exit 1; }
[[ -x "$source" ]] || {
  echo "Not executable: $source" >&2
  echo 'An AppImage is a program: run chmod +x on it first, then install it again.' >&2
  exit 1
}
# Resolved to an absolute path now: the extraction below changes directory, and a
# relative path would then point somewhere else.
source=$(readlink -f -- "$source")
[[ $(id -u) != 0 ]] || { echo 'Install as your normal user, without sudo' >&2; exit 1; }

# Optional integrity check before anything is unpacked or written. A manifest that
# does not mention this exact file, or mentions it with a different hash, stops the
# installation: a file that is not the one the publisher described is not installed.
if [[ -n "$checksums" ]]; then
  [[ -s "$checksums" ]] || { echo "Checksum file is missing or empty: $checksums" >&2; exit 1; }
  want=$(awk -v name="$(basename -- "$source")" '{ n=$2; sub(/^\*/, "", n); if (n == name) { print tolower($1); exit } }' "$checksums")
  if [[ -z "$want" ]]; then
    echo "$(basename -- "$checksums") does not list $(basename -- "$source"); nothing was installed." >&2
    exit 1
  fi
  got=$(sha256sum -- "$source" | cut -d' ' -f1)
  if [[ "$want" != "$got" ]]; then
    cat >&2 <<MSG
Checksum mismatch for $(basename -- "$source"):
  expected $want ($(basename -- "$checksums"))
  found    $got
Nothing was installed. Do not run this file; download it again from the publisher.
MSG
    exit 1
  fi
  echo "Checksum verified against $(basename -- "$checksums"): $got"
fi

# The installer writes into the user's home only, so a path that cannot be
# represented safely in a desktop entry is refused rather than escaped badly.
for path in "$target" "$entry" "$docdir" "$icons"; do
  [[ "$path" != *$'\n'* && "$path" != *$'\r'* && "$path" != *$'\t'* && "$path" != *'='* && "$path" != *'%'* ]] || {
    echo "Home path cannot contain control characters, = or % for desktop installation: $path" >&2
    exit 1
  }
done

# Signature check, before the runtime that would unpack the file is executed at
# all. The image is not run, extracted or copied until this passes, and a failure
# leaves an existing installation and its database exactly as they were.
if [[ -n "$signature" ]]; then
  if [[ -n "$public_key" ]]; then
    verify_home=$(mktemp -d "${TMPDIR:-/tmp}/radanor-verify.XXXXXX")
    chmod 700 -- "$verify_home"
    if ! gpg --homedir "$verify_home" --batch --quiet --import "$public_key" 2>/dev/null; then
      echo "Could not import the public key $public_key; nothing was installed." >&2
      exit 1
    fi
    verify_keyring=$verify_home
  else
    verify_keyring=$keyring
  fi
  status=$(gpg --homedir "$verify_keyring" --batch --no-tty --trust-model always --status-fd 1 \
    --verify "$signature" "$source" 2>/dev/null || true)
  if ! grep -q '^\[GNUPG:\] GOODSIG' <<<"$status"; then
    cat >&2 <<MSG
Signature verification failed for $(basename -- "$source").
Nothing was installed, and the image was not unpacked or run. A signature is only
evidence when it is made by the key whose fingerprint you supplied ($trusted).
MSG
    exit 1
  fi
  signed_by=$(awk '/^\[GNUPG:\] VALIDSIG / {print $3; exit}' <<<"$status")
  if [[ "$signed_by" != "$trusted" ]]; then
    cat >&2 <<MSG
The signature on $(basename -- "$source") is valid, but it is by $signed_by, not the
trusted fingerprint $trusted. Nothing was installed: a key that arrived beside the
download may not vouch for it.
MSG
    exit 1
  fi
  echo "Signature verified: $(basename -- "$signature") by $signed_by"
fi

# Everything that goes into the home directory comes out of the image itself, so
# the downloaded file is the only input: no checkout, no repository copy of the
# artwork, and no chance of installing icons that do not belong to this build.
extract_dir=$(mktemp -d "${TMPDIR:-/tmp}/radanor-install.XXXXXX")
# stage is created later, inside the bin directory, so its files land on the same
# filesystem as their destination and can be moved into place atomically.
cleanup() { rm -rf -- "${extract_dir:-}" "${verify_home:-}" "${stage:-}"; }
trap cleanup EXIT

if ! ( cd "$extract_dir" && "$source" --appimage-extract >/dev/null 2>&1 ); then
  cat >&2 <<MSG
Cannot read the contents of $source: its AppImage runtime refused --appimage-extract.
A file that cannot unpack itself cannot be verified as a Sparrow build, so nothing
was installed.
MSG
  exit 1
fi
payload="$extract_dir/squashfs-root"
[[ -d "$payload" ]] || { echo "$source unpacked to no payload directory; nothing was installed." >&2; exit 1; }

build_info="$payload/usr/share/radanor-sparrow/BUILD-INFO"
[[ -f "$build_info" ]] || {
  echo "$source carries no BUILD-INFO, so its version and variant cannot be checked; nothing was installed." >&2
  exit 1
}
field() { awk -F= -v key="$1" '$1 == key { sub(/^[^=]*=/, ""); print; exit }' "$build_info"; }
variant=$(field variant)
version=$(field version)
case "$variant" in
  development|release) ;;
  *)
    echo "$source reports variant=${variant:-<absent>}, which is neither development nor release; nothing was installed." >&2
    exit 1 ;;
esac
[[ -n "$version" ]] || { echo "$source carries no version in BUILD-INFO; nothing was installed." >&2; exit 1; }

if [[ "$variant" != "$expectation" ]]; then
  if [[ "$expectation" == development ]]; then
    cat >&2 <<MSG
$source is a release artifact ($version, variant=release). This script installs
development builds by default; pass --release to install a release artifact
deliberately. Nothing was installed.
MSG
  else
    cat >&2 <<MSG
$source is a development artifact ($version, variant=development) and --release asks
to install a release build. Nothing was installed.
MSG
  fi
  exit 1
fi

# The approved artwork, taken from the image at the sizes it carries. A build that
# is missing one of them is refused rather than installed with a gap.
icon_payload="$payload/usr/share/icons/hicolor"
for size in 16 32 48 128 256; do
  source_icon="$icon_payload/${size}x${size}/apps/$appid.png"
  [[ -s "$source_icon" ]] || { echo "$source carries no ${size}x${size} icon at $source_icon; nothing was installed." >&2; exit 1; }
done
for document in LICENSE THIRD_PARTY_NOTICES.md; do
  [[ -s "$payload/usr/share/doc/radanor-sparrow/$document" ]] || {
    echo "$source carries no usr/share/doc/radanor-sparrow/$document; nothing was installed." >&2
    exit 1
  }
done

mkdir -p -- "$bindir" "$applications" "$docdir"

# The two files that must never be left half-written — the installed image and the
# launcher entry — are staged beside their destination first, and moved into place
# only once both are complete and the entry validates. Every check above already ran,
# so a failure here leaves an existing installation exactly as it was rather than
# replacing a working image with a truncated one.
stage=$(mktemp -d "$bindir/.radanor-install.XXXXXX")
staged_image="$stage/radanor-sparrow.AppImage"
staged_entry="$stage/radanor-sparrow.desktop"

install -m 755 -- "$source" "$staged_image"
cmp -s -- "$source" "$staged_image" || {
  echo "The staged copy of $(basename -- "$source") does not match the file that was read; nothing was changed." >&2
  exit 1
}

# Desktop-file quoting escapes backslashes, quotes, dollars and backticks.
escaped=${target//\\/\\\\}
escaped=${escaped//\"/\\\"}
escaped=${escaped//\$/\\$}
escaped=${escaped//\`/\\\`}
escaped=${escaped//%/%%}
printf '[Desktop Entry]\nType=Application\nName=Radanor Sparrow\nGenericName=Security Posture Monitoring\nComment=Local security posture and monitoring\nExec="%s"\nTerminal=false\nIcon=%s\nCategories=System;Security;\nKeywords=security;monitoring;posture;radanor;sparrow;\nStartupNotify=true\nStartupWMClass=%s\nX-Radanor-Variant=%s\n' \
  "$escaped" "$appid" "$wmclass" "$variant" > "$staged_entry"

# Validate the entry before it is installed when the tool is available: a launcher
# that only looks installed is worse than a reported failure.
if command -v desktop-file-validate >/dev/null; then
  desktop-file-validate "$staged_entry"
fi

# Cosmetic assets go first: a failure while writing an icon or a document cannot
# replace the image that is already installed, so an interrupted upgrade leaves the
# previous installation working.
for size in 16 32 48 128 256; do
  install -d -m 755 -- "$icons/${size}x${size}/apps"
  install -m 644 -- "$icon_payload/${size}x${size}/apps/$appid.png" "$icons/${size}x${size}/apps/$appid.png"
done
for document in LICENSE THIRD_PARTY_NOTICES.md; do
  install -m 644 -- "$payload/usr/share/doc/radanor-sparrow/$document" "$docdir/$document"
done

# The image and the entry are the pair an upgrade replaces, and both staged files
# are already complete and validated. Moving them last keeps the window in which
# the two disagree down to two renames.
mv -f -- "$staged_image" "$target"
mv -f -- "$staged_entry" "$entry"
refresh_caches

echo "Installed AppImage: $target"
echo "Version: $version ($variant build)"
echo "Launcher entry: $entry"
echo "License and third-party notices: $docdir"
echo 'Launch "Radanor Sparrow" from the Applications menu or the dock.'
if [[ "$variant" == development ]]; then
  echo 'This is a development build, not an approved public release.'
fi
