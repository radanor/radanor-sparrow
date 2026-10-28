#!/usr/bin/env bash
# Verifies a directory of Radanor Sparrow release artifacts.
#
#   bash scripts/verify-release.sh [--require-signature] [--require-all-signed] \
#       [--key FILE --expect-fingerprint FINGERPRINT] [directory]
#
# Default directory: dist/linux. Exit status: 0 verified, 1 verification failed,
# 2 usage or missing prerequisite.
#
# What each check proves, and deliberately does not prove:
#
#   checksum (SHA256SUMS.*)   the file is byte-for-byte the one the manifest was
#                             written for. It does NOT prove who published it: a
#                             manifest can be replaced together with the file.
#   signature (*.asc)         the file was signed by the holder of the private key
#                             for a public key you trust. Only this binds the
#                             artifact to a publisher, and only when the key's
#                             fingerprint is one you obtained from a trusted
#                             channel rather than from the download directory.
#
# Trust is a fingerprint, not a file. --expect-fingerprint names the 40-hex
# fingerprint you verified out of band; any signature by another key fails, even
# when that key's public key arrived next to the artifact. A public key found in
# the directory is never trusted by itself.
#
# --require-all-signed demands a verified signature for every published artifact:
# the AppImage, the portable executable and both checksum manifests. A partially
# signed release must not be published, so a missing signature fails.
#
# This script never fetches anything from the network and never trusts a key it
# finds in the directory unless you name it with --key.
set -euo pipefail

require_signature=0
require_all_signed=0
key_file=
expected=
dir=dist/linux
while (( $# )); do
  case "$1" in
    --require-signature) require_signature=1; shift ;;
    --require-all-signed) require_all_signed=1; shift ;;
    --key)
      (( $# >= 2 )) || { echo '--key needs a public key file' >&2; exit 2; }
      key_file=$2
      shift 2 ;;
    --expect-fingerprint)
      (( $# >= 2 )) || { echo '--expect-fingerprint needs a full 40-hex OpenPGP fingerprint' >&2; exit 2; }
      expected=$2
      shift 2 ;;
    -h|--help)
      sed -n '2,31p' "$0" | sed 's/^# \?//'
      exit 0 ;;
    -*)
      echo "Unknown argument: $1 (expected --require-signature, --require-all-signed, --key FILE, --expect-fingerprint FINGERPRINT or a directory)" >&2
      exit 2 ;;
    *) dir=$1; shift ;;
  esac
done

# A fingerprint is compared in one normalized form: no spaces or colons, upper
# case. Anything that is not a full 40-hex fingerprint is refused rather than
# shortened to a key id, because a key id is not a trust anchor.
normalize_fingerprint() {
  local value=${1:-}
  value=$(tr -d ' :' <<<"$value" | tr '[:lower:]' '[:upper:]')
  [[ $value =~ ^[0-9A-F]{40}$ ]] || return 1
  printf '%s' "$value"
}
if [[ -n "$expected" ]]; then
  requested=$expected
  expected=$(normalize_fingerprint "$requested") || {
    echo "--expect-fingerprint must be a full 40-hex fingerprint, not a key id or a name: got $requested" >&2
    exit 2
  }
fi

[[ -d "$dir" ]] || { echo "No such directory: $dir" >&2; exit 2; }
if (( require_signature )) && [[ -z "$key_file" ]]; then
  echo '--require-signature needs --key FILE and --expect-fingerprint: a signature is only evidence with a public key and a fingerprint from a trusted channel.' >&2
  exit 2
fi
if (( require_signature )) && [[ -z "$expected" ]]; then
  echo '--require-signature needs --expect-fingerprint: without it a valid signature by any key would be accepted.' >&2
  exit 2
fi
if (( require_all_signed )) && [[ -z "$key_file" ]]; then
  echo '--require-all-signed needs --key FILE and --expect-fingerprint: an unsigned artifact is not a pass.' >&2
  exit 2
fi
if (( require_all_signed )) && [[ -z "$expected" ]]; then
  echo '--require-all-signed needs --expect-fingerprint: coverage is only meaningful against a trusted key.' >&2
  exit 2
fi
if [[ -n "$key_file" && ! -s "$key_file" ]]; then
  echo "Public key file is missing or empty: $key_file" >&2
  exit 2
fi

failures=0
note() { printf '%s\n' "$*"; }

manifest_seen=0
for manifest in SHA256SUMS.appimage SHA256SUMS.portable; do
  if [[ ! -f "$dir/$manifest" ]]; then
    continue
  fi
  manifest_seen=1
  if ( cd "$dir" && sha256sum --check --quiet "$manifest" ); then
    note "checksum OK: $dir/$manifest"
  else
    note "CHECKSUM MISMATCH: $dir/$manifest does not match the files beside it" >&2
    failures=$((failures + 1))
  fi
done
if (( ! manifest_seen )); then
  echo "No SHA256SUMS.appimage or SHA256SUMS.portable in $dir: nothing to verify against." >&2
  exit 1
fi

# The artifacts a signed release must cover. Only collected when --require-all-signed
# is used, because a directory that is merely being inspected may hold one artifact.
required=()
if (( require_all_signed )); then
  images=()
  while IFS= read -r -d '' image; do
    images+=("$image")
  done < <(find "$dir" -maxdepth 1 -name 'RadanorSparrow-*-x86_64.AppImage' -print0)
  if (( ${#images[@]} != 1 )); then
    echo "Expected exactly one RadanorSparrow-*-x86_64.AppImage in $dir, found ${#images[@]}: the set to sign is ambiguous." >&2
    failures=$((failures + 1))
  else
    required+=("${images[0]}")
  fi
  for name in radanor-sparrow SHA256SUMS.appimage SHA256SUMS.portable; do
    if [[ -f "$dir/$name" ]]; then
      required+=("$dir/$name")
    else
      echo "UNSIGNED: $dir/$name is missing, so the release cannot carry a complete signed set." >&2
      failures=$((failures + 1))
    fi
  done
  for artifact in "${required[@]}"; do
    if [[ ! -f "$artifact.asc" ]]; then
      echo "UNSIGNED: $artifact has no detached signature beside it; a partially signed release must not be published." >&2
      failures=$((failures + 1))
    fi
  done
fi

# Signatures, when they exist. Each signature is verified against a keyring
# containing only the key you supplied, so a key shipped in the same directory
# cannot vouch for itself, and the signing fingerprint must equal the one you
# named.
signatures=()
while IFS= read -r -d '' signature; do
  signatures+=("$signature")
done < <(find "$dir" -maxdepth 1 -name '*.asc' -print0)
if (( ${#signatures[@]} )); then
  if [[ -z "$key_file" ]]; then
    note "${#signatures[@]} signature file(s) found; pass --key FILE --expect-fingerprint FINGERPRINT to check them."
  else
    gnupg=$(mktemp -d "${TMPDIR:-/tmp}/radanor-verify.XXXXXX")
    trap 'rm -rf -- "$gnupg"' EXIT
    export GNUPGHOME="$gnupg/gnupg"
    mkdir -m 700 -p "$GNUPGHOME"
    if ! gpg --batch --quiet --import "$key_file" 2>/dev/null; then
      echo "Could not import the public key: $key_file" >&2
      exit 1
    fi
    for signature in "${signatures[@]}"; do
      artifact=${signature%.asc}
      if [[ ! -f "$artifact" ]]; then
        note "SIGNATURE HAS NO ARTIFACT: $signature" >&2
        failures=$((failures + 1))
        continue
      fi
      status=$(gpg --batch --no-tty --trust-model always --status-fd 1 --verify "$signature" "$artifact" 2>/dev/null || true)
      if ! grep -q '^\[GNUPG:\] GOODSIG' <<<"$status"; then
        # A missing public key and a corrupt signature are different findings. The
        # first is the ordinary "signed by some other key" case, and it must say so:
        # the keyring here holds only the key the caller trusted.
        if grep -q 'NO_PUBKEY' <<<"$status"; then
          note "SIGNED BY AN UNTRUSTED KEY: $signature is not by ${expected:-the key you trusted}; the signer's key is not in the trusted keyring, so it may not vouch for this file" >&2
        else
          note "SIGNATURE INVALID OR UNTRUSTED: $signature does not verify against the trusted key" >&2
        fi
        failures=$((failures + 1))
        continue
      fi
      # GOODSIG <keyid> <user id>; VALIDSIG <fingerprint> <sig date> …
      signer=$(awk '/^\[GNUPG:\] GOODSIG / {sub(/^\[GNUPG:\] GOODSIG [^ ]+ /, ""); print; exit}' <<<"$status")
      fingerprint=$(awk '/^\[GNUPG:\] VALIDSIG / {print $3; exit}' <<<"$status")
      if [[ -z "$fingerprint" && -n "$expected" ]]; then
        note "SIGNATURE WITHOUT A FINGERPRINT: $signature cannot be matched against $expected" >&2
        failures=$((failures + 1))
        continue
      fi
      if [[ -n "$expected" && "$fingerprint" != "$expected" ]]; then
        # This is the case the old tool missed: the signature is cryptographically
        # valid, but it belongs to a key nobody trusted.
        note "SIGNED BY AN UNTRUSTED KEY: $signature is by ${signer:-unknown signer} ($fingerprint), not the expected $expected" >&2
        failures=$((failures + 1))
        continue
      fi
      note "signature OK: $signature by ${signer:-unknown signer} ($fingerprint)"
      if [[ -n "$expected" ]]; then
        note "  trusted fingerprint confirmed: $expected"
      else
        note "  confirm that fingerprint out of band; a signature is only as trustworthy as the key you checked it against."
      fi
    done
  fi
elif (( require_signature )); then
  echo "--require-signature was given but $dir contains no *.asc signature." >&2
  failures=$((failures + 1))
fi

if (( failures )); then
  echo "Verification failed: $failures problem(s). Do not run or distribute these files." >&2
  exit 1
fi
note "Verification passed for $dir"
