#!/usr/bin/env bash
# Build the Radanor Sparrow per-user Windows installer from the portable
# executable this checkout produced.
#
#   bash scripts/build-windows-installer.sh [--development | --release | --community-unsigned]
#     [--portable-dir DIR] [--output FILE] [--source-dir DIR (development acceptance only)]
#
# Development is the default and the only mode that does not run the licensing
# release gate; `--release` is the explicit public-release path and fails closed,
# exactly like scripts/build-linux.sh and scripts/build-windows.ps1.
#
# `--community-unsigned` produces a Community Edition installer that is openly
# unsigned. It applies the same gate, the same exact-tag rule and the same identity
# checks as `--release`; the only thing it changes is the trust claim, which it
# does not make. It is mutually exclusive with `--release`, refuses to run while
# signing is enabled, and requires RADANOR_COMMUNITY_UNSIGNED=acknowledged, so it
# can never be a way around the signed path. See
# docs/deployment/COMMUNITY_UNSIGNED_DISTRIBUTION_POLICY.md.
#
# The script runs natively on Ubuntu (makensis is a POSIX program) and under Git
# Bash on the windows-2025 runner, so the same script produces the installer in
# both places. It never installs NSIS, downloads a runtime, or writes outside the
# output directory. It does not download or install build tools.
#
# The installer contains the portable executable, LICENSE and
# THIRD_PARTY_NOTICES.md, and its version comes from the BUILD-INFO record the
# portable build wrote. The executable is checked against that record before
# packaging — VERSIONINFO resource, checksum and commit — so a stale build, or
# one from another commit, cannot be wrapped in an installer that names it.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo"

# One implementation of the "whole path, native separators" rule, shared with the
# path tests in internal/wininstaller.
# shellcheck source=scripts/nsis-paths.sh
. "$repo/scripts/nsis-paths.sh"
. "$repo/scripts/windows-installer-policy.sh"

release=0
# An openly unsigned Community Edition installer: identical provenance and gate
# requirements to --release, without a signature it does not have.
community_unsigned=0
mode_count=0
packaging_mode=development
portable_dir=dist/windows
source_dir=$repo
output=
while (( $# )); do
  case "$1" in
    --source-dir)
      (( $# >= 2 )) || { echo '--source-dir needs a directory' >&2; exit 1; }
      source_dir=$2; shift 2 ;;
    --release)
      release=1
      mode_count=$((mode_count + 1)); packaging_mode=signed-release
      shift
      ;;
    --community-unsigned)
      community_unsigned=1
      mode_count=$((mode_count + 1)); packaging_mode=community-unsigned
      shift
      ;;
    --development)
      mode_count=$((mode_count + 1)); packaging_mode=development
      shift ;;
    --portable-dir)
      (( $# >= 2 )) || { echo '--portable-dir needs a directory' >&2; exit 1; }
      portable_dir=$2
      shift 2
      ;;
    --output)
      (( $# >= 2 )) || { echo '--output needs a file path' >&2; exit 1; }
      output=$2
      shift 2
      ;;
    -h|--help)
      sed -n '2,23p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "Unknown argument: $1 (expected --development, --release, --community-unsigned, --portable-dir, --output, --source-dir or --help)" >&2
      exit 1
      ;;
  esac
done

if (( release && community_unsigned )); then
  echo 'Refusing both --release and --community-unsigned: choose one packaging mode.' >&2; exit 1
fi
(( mode_count <= 1 )) || { echo 'Refusing repeated or contradictory packaging modes' >&2; exit 1; }

for tool in go sha256sum; do
  command -v "$tool" >/dev/null || { echo "Missing build prerequisite: $tool" >&2; exit 1; }
done
makensis=${MAKENSIS:-}
if [[ -z "$makensis" ]]; then
  makensis=$(command -v makensis || true)
fi
if [[ -z "$makensis" || ! -x "$makensis" ]]; then
  cat >&2 <<'MSG'
makensis is required to build the installer. Use reviewed Unicode NSIS 3.13
and point MAKENSIS at the executable. See docs/deployment/WINDOWS_INSTALLER.md.
MSG
  exit 1
fi
# Pin the tested stub/plugins as well as their version-resource behavior.
if ! makensis_output=$("$makensis" -VERSION 2>&1); then
  printf 'NSIS version query failed: executable=[%s], output=[%s]\n' "$makensis" "$makensis_output" >&2
  exit 1
fi
makensis_version=${makensis_output%$'\r'}
printf 'NSIS compiler: %s\nNSIS version: [%s]\n' "$makensis" "$makensis_version"
case "$makensis_version" in
  v3.13) ;;
  *)
    echo "Reviewed NSIS 3.13 is required; $makensis reports: $makensis_version" >&2
    exit 1
    ;;
esac

export GOTOOLCHAIN=local

# The payload is the artifact whose identity the installer carries, so it has to
# exist, and to say what it is, before anything here can be decided.
[[ -d "$portable_dir" ]] || {
  echo "Missing $portable_dir. Build the portable executable first: pwsh ./scripts/build-windows.ps1 (Windows) or the documented equivalent." >&2
  exit 1
}
for file in RadanorSparrow.exe LICENSE THIRD_PARTY_NOTICES.md BUILD-INFO; do
  [[ -f "$portable_dir/$file" ]] || {
    if [[ "$file" == BUILD-INFO ]]; then
      echo "Missing $portable_dir/BUILD-INFO: the portable executable carries no build identity record. Build it with scripts/build-windows.ps1 (or the documented equivalent commands), which writes one." >&2
    else
      echo "Missing $portable_dir/$file. Build the portable artifact first." >&2
    fi
    exit 1
  }
done

# One identity. The version is read from the record the portable build captured
# *before* it generated the executable, not derived again from this checkout. The
# frontend build rewrites the committed dashboard assets, and a Windows checkout
# then reports them as modified even though their content is unchanged, so a
# second derivation said `.dirty` while the executable it wrapped was clean and
# the packaging step refused its own artifact. The installer now names the
# artifact it was given instead of re-deriving the name. The record is validated
# in full — executable VERSIONINFO, checksum and commit — by internal/wininstaller
# below; this block only needs the values that become a file name and defines.
build_info="$portable_dir/BUILD-INFO"
# read to the end rather than stopping at the first match: an early exit would
# send SIGPIPE up a pipeline that `pipefail` turns into a failure.
record_field() {
  tr -d '\r' < "$build_info" | sed -n "s/^$1=//p"
}
version=$(record_field version)
variant=$(record_field variant)
numeric=$(record_field numeric)
sequence=$(record_field sequence)
if [[ -z "$version" || -z "$variant" || -z "$numeric" || ! "$sequence" =~ ^[1-9][0-9]*$ ]]; then
  echo "Refusing to package: $build_info does not name a version, a variant and a numeric version." >&2
  exit 1
fi
echo "Installer version: $version ($variant build), numeric $numeric"

# VIProductVersion accepts exactly four 16-bit numbers. The version rule already
# reduces the string to that form when it writes the record; this guards the
# script boundary as well, because a malformed value here would produce an
# installer Windows cannot read.
if [[ ! "$numeric" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "Refusing to build: numeric version '$numeric' is not four numbers." >&2
  exit 1
fi
for part in ${numeric//./ }; do
  (( part <= 65535 )) || { echo "Refusing to build: numeric version '$numeric' exceeds 65535." >&2; exit 1; }
done

if (( community_unsigned )); then
  # The recorded authorization for an unsigned Windows artifact, checked here as
  # well as in CI so the rule also holds when a maintainer runs this by hand.
  require_community_unsigned_authorization
fi
if (( release || community_unsigned )); then
  [[ "$source_dir" == "$repo" ]] || { echo 'Release source override refused' >&2; exit 1; }
  echo 'Release build: validating licensing metadata before packaging.'
  go run ./cmd/radanor-release-check || {
    echo 'Release blocked: licensing metadata is incomplete. Refusing to produce a public installer.' >&2
    exit 1
  }
  # The record has to name this release. A development record with a matching
  # executable is still refused, so --release cannot relabel a development build.
  release_version=$(go run ./cmd/radanor-version -release) || {
    echo 'Release blocked: a public installer must be built from a clean checkout of an exact vX.Y.Z tag.' >&2
    exit 1
  }
  assert_windows_release_identity "$variant" "$version" "$release_version"
else
  [[ "$variant" == development ]] || {
    echo "Refusing to package a $variant build as a development installer; use --release after the gate passes." >&2
    exit 1
  }
fi

# The installer must wrap this build, not an older one. The check reads the
# executable's own PE version resource and compares it with the version rule, so
# a stale executable with a matching file name is refused rather than packaged.
# The test runs in its package directory, so the path it compares has to be
# absolute; a relative path would point outside the package and fail to open.
# Under Git Bash the Go test binary is a native Windows program, so it needs a
# Windows path rather than the MSYS one.
portable_abs=$(cd "$portable_dir" && pwd)

# Unsigned absence is an independently verified property, not a compiler option.
# No host/tool downloads or policy bypass switches are permitted here.
if (( community_unsigned )); then
  unsigned_host=${RADANOR_UNSIGNED_POWERSHELL:-$(command -v pwsh || command -v powershell.exe || true)}
  [[ -n "$unsigned_host" && -x "$unsigned_host" && -n "${RADANOR_7ZIP:-}" ]] || {
    echo 'Community packaging requires an installed PowerShell host and verified RADANOR_7ZIP' >&2; exit 1;
  }
  "$unsigned_host" -NoProfile -NonInteractive -File "$(nsis_native_path "$repo/validation/windows/Assert-SparrowUnsignedWindows.ps1")" \
    -Path "$(nsis_native_path "$portable_abs/RadanorSparrow.exe")"
fi
git -C "$source_dir" rev-list HEAD > "$portable_abs/ANCESTRY"
portable_exe=$(nsis_native_path "$portable_abs/RadanorSparrow.exe")
echo 'Checking that the portable executable matches its build identity ...'
if ! RADANOR_PACKAGE_SOURCE_DIR="$(nsis_native_path "$source_dir")" RADANOR_PORTABLE_EXE="$portable_exe" \
  go test ./internal/wininstaller -run '^TestPortableExecutableCarriesTheBuildVersion$' -count=1; then
  echo "Refusing to package: $portable_dir/RadanorSparrow.exe does not match its BUILD-INFO record (version $version), or it was built from another commit." >&2
  exit 1
fi

icon_abs=$(cd "$(dirname web/public/favicon.ico)" && pwd)/favicon.ico
if [[ -z "$output" ]]; then
  output="dist/windows/RadanorSparrow-${version}-setup.exe"
fi
mkdir -p "$(dirname -- "$output")"
out_abs=$(cd "$(dirname -- "$output")" && pwd)/$(basename -- "$output")
rm -f -- "$out_abs"

# Every path that reaches makensis is produced here, whole and absolute, in this
# host's separators. The .nsi uses each one as given: it never appends a file
# name to a directory, because that is what produced
# `D:\...\dist\windows/RadanorSparrow.exe` on the Windows runner and made
# makensis look for `windows/RadanorSparrow.exe` inside `dist`.
#
# Each payload file is checked to exist, and each value is checked for the
# conventions NSIS cannot resolve, immediately before the compiler runs.
deffiles=()
deffile() { # <define> <absolute path> <must-exist: yes|no>
  local name=$1 path=$2 must_exist=$3 value problem
  if [[ "$must_exist" == yes && ! -f "$path" ]]; then
    echo "Refusing to package: $path does not exist, so $name would name nothing." >&2
    exit 1
  fi
  value=$(nsis_native_path "$path") || {
    echo "Refusing to package: cannot express $path in this host's path form." >&2
    exit 1
  }
  problem=$(nsis_path_problem "$value")
  if [[ -n "$problem" ]]; then
    echo "Refusing to pass $name to makensis: $problem. Got: $value" >&2
    exit 1
  fi
  deffiles+=("-D$name=$value")
}
deffile PAYLOAD_EXE "$portable_abs/RadanorSparrow.exe" yes
deffile PAYLOAD_LICENSE "$portable_abs/LICENSE" yes
deffile PAYLOAD_NOTICES "$portable_abs/THIRD_PARTY_NOTICES.md" yes
deffile PAYLOAD_BUILD_INFO "$portable_abs/BUILD-INFO" yes
deffile ANCESTRY "$portable_abs/ANCESTRY" yes
deffile APP_ICON "$icon_abs" yes
deffile OUTFILE "$out_abs" no

if (( community_unsigned )); then
  # Explicit, validated compiler policy; build identity remains release.
  echo 'Community Edition build: this installer is UNSIGNED. No Authenticode signature and no trusted timestamp are applied or claimed.' >&2
elif (( release )); then
  [[ "${RADANOR_WINDOWS_SIGNING:-}" == enabled ]] || { echo 'Release signing is not enabled' >&2; exit 1; }
  export RADANOR_SIGNING_PORTABLE_DIR
  RADANOR_SIGNING_PORTABLE_DIR=$(nsis_native_path "$portable_abs")
  [[ -n "${RADANOR_SIGNING_HOST:-}" && -n "${RADANOR_SIGNED_UNINSTALLER:-}" ]] || { echo 'Release signing host and uninstaller evidence path required' >&2; exit 1; }
  deffile SIGNING_SCRIPT "$repo/validation/windows/Sign-SparrowWindowsArtifact.ps1" yes
  deffile SIGNING_HOST "$RADANOR_SIGNING_HOST" yes
fi

# A failed makensis run must not leave an older installer behind under the same
# name; the output was removed above, and the exit status is the script's.
"$makensis" -V2 \
  -DPACKAGING_MODE="$packaging_mode" \
  -DPRODUCT_VERSION="$version" \
  -DBUILD_SEQUENCE="$sequence" \
  -DBUILD_COMMIT="$(record_field commit)" \
  -DVARIANT="$variant" \
  -DNUMERIC_VERSION="$numeric" \
  "${deffiles[@]}" \
  "$repo/installer/windows/radanor-sparrow.nsi"

[[ -f "$out_abs" ]] || { echo "makensis reported success but produced no installer" >&2; exit 1; }
if (( community_unsigned )); then
  "$unsigned_host" -NoProfile -NonInteractive -File "$(nsis_native_path "$repo/validation/windows/Assert-SparrowUnsignedWindows.ps1")" \
    -Setup "$(nsis_native_path "$out_abs")" -PortableDirectory "$(nsis_native_path "$portable_abs")" \
    -SevenZip "$RADANOR_7ZIP"
fi
chmod 755 -- "$out_abs" 2>/dev/null || true

if (( release || community_unsigned )); then
  go run ./cmd/radanor-release-check --release-dir "$portable_dir" || {
    echo 'Release blocked: the packaged directory is missing required licensing documents.' >&2
    exit 1
  }
  if (( community_unsigned )); then
    echo 'Community Edition installer: licensing gate passed; this artifact is unsigned by policy.'
  else
    echo 'Release installer: licensing gate passed.'
  fi
else
  echo 'Development installer: NOT a release artifact; the licensing release gate was not run.' >&2
fi
sha256sum -- "$out_abs"
printf 'size=%s bytes\n' "$(wc -c < "$out_abs" | tr -d ' ')"
