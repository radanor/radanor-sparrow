#!/usr/bin/env bash
# Provision the reviewed AppImage build tool and runtime without executing bytes
# selected by a mutable release URL. Both immutable assets are checked before the
# tool is made executable or asked to extract itself.
set -euo pipefail

usage() {
  echo 'Usage: Initialize-SparrowAppImageTool.sh --directory DIR [--github-env FILE]' >&2
  exit 2
}

directory=
github_env=
while (( $# )); do
  case "$1" in
    --directory) (( $# >= 2 )) || usage; directory=$2; shift 2 ;;
    --github-env) (( $# >= 2 )) || usage; github_env=$2; shift 2 ;;
    *) usage ;;
  esac
done
[[ -n "$directory" ]] || usage

readonly tool_version=1.9.1
readonly tool_name=appimagetool-x86_64.AppImage
readonly tool_sha256=ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
readonly tool_url="https://github.com/AppImage/appimagetool/releases/download/${tool_version}/${tool_name}"
readonly runtime_version=20251108
readonly runtime_name=runtime-x86_64
readonly runtime_sha256=2fca8b443c92510f1483a883f60061ad09b46b978b2631c807cd873a47ec260d
readonly runtime_url="https://github.com/AppImage/type2-runtime/releases/download/${runtime_version}/${runtime_name}"

mkdir -p -- "$directory"
directory=$(cd -- "$directory" && pwd)

fetch_verified() {
  local url=$1 expected=$2 destination=$3 label=$4 actual partial
  if [[ ! -f "$destination" ]]; then
    partial="${destination}.partial"
    rm -f -- "$partial"
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
      --output "$partial" "$url"
    mv -- "$partial" "$destination"
  fi
  actual=$(sha256sum -- "$destination" | awk '{print $1}')
  printf '%s SHA-256: %s\n' "$label" "$actual"
  if [[ "$actual" != "$expected" ]]; then
    echo "$label checksum mismatch; refusing to execute or package unverified bytes" >&2
    exit 1
  fi
}

tool="$directory/$tool_name"
runtime="$directory/$runtime_name"
fetch_verified "$tool_url" "$tool_sha256" "$tool" 'appimagetool'
fetch_verified "$runtime_url" "$runtime_sha256" "$runtime" 'AppImage runtime'
chmod 0755 -- "$tool" "$runtime"

extract="$directory/appimagetool-1.9.1"
rm -rf -- "$extract"
mkdir -- "$extract"
(cd -- "$extract" && "$tool" --appimage-extract >/dev/null)
app_run="$extract/squashfs-root/AppRun"
[[ -x "$app_run" ]] || { echo 'Verified appimagetool did not extract an executable AppRun' >&2; exit 1; }

if [[ -n "$github_env" ]]; then
  printf 'APPIMAGETOOL=%s\nAPPIMAGE_RUNTIME=%s\n' "$app_run" "$runtime" >> "$github_env"
fi
printf 'APPIMAGETOOL=%s\nAPPIMAGE_RUNTIME=%s\n' "$app_run" "$runtime"
