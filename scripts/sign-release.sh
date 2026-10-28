#!/usr/bin/env bash
# Detached-signs release artifacts with a key the maintainer already controls.
#
#   bash scripts/sign-release.sh --armored-key FILE [--expect-fingerprint FPR] ARTIFACT...
#   bash scripts/sign-release.sh --key FINGERPRINT [--expect-fingerprint FPR] ARTIFACT...
#   bash scripts/sign-release.sh --development --key FINGERPRINT ARTIFACT...   # dry run
#
# The complete signed set of a release is the AppImage, the portable executable and
# both checksum manifests, because a signature on one of them says nothing about
# the others:
#
#   <artifact>.asc       the detached, armored signature
#   <artifact>.asc.pub   the public key that verifies it
#
# and it verifies every signature it writes before returning.
#
# --expect-fingerprint is the key that is allowed to sign. It refuses a secret key
# that does not match, which turns a misconfigured CI secret into a failure instead
# of a release signed by the wrong key.
#
# A development build is refused unless --development is given: by default only an
# artifact whose own BUILD-INFO says variant=release can be signed, so a signature
# cannot make a development build look like an approved release.
#
# What it deliberately does NOT do:
#
#   * it never generates a key, and never embeds one in the repository: the key is
#     an input, so no key material can leak through a commit;
#   * it never signs with the maintainer's own keyring: the key is imported into a
#     temporary, isolated GnuPG home that is destroyed on exit;
#   * it never claims the artifact is approved. Signing proves possession of a key;
#     publishing the fingerprint out of band is what makes that key trustworthy
#     (docs/deployment/RELEASE_SIGNING.md).
#
# Exit status: 0 signed, 1 failure, 2 usage or missing prerequisite.
set -euo pipefail

artifacts=()
armored_key=
key_fingerprint=
expected=
development_allowed=0
while (( $# )); do
  case "$1" in
    --development)
      # Opt-in for a pipeline dry run. The signature written is real, so the result
      # is still not a release and the summary says so.
      development_allowed=1
      shift ;;
    --armored-key)
      (( $# >= 2 )) || { echo '--armored-key needs a file' >&2; exit 2; }
      armored_key=$2
      shift 2 ;;
    --key)
      (( $# >= 2 )) || { echo '--key needs a fingerprint' >&2; exit 2; }
      key_fingerprint=$2
      shift 2 ;;
    --expect-fingerprint)
      (( $# >= 2 )) || { echo '--expect-fingerprint needs a full 40-hex OpenPGP fingerprint' >&2; exit 2; }
      expected=$2
      shift 2 ;;
    -h|--help)
      sed -n '2,35p' "$0" | sed 's/^# \?//'
      exit 0 ;;
    -*)
      echo "Unknown argument: $1 (expected --armored-key FILE, --key FINGERPRINT, --expect-fingerprint FPR, --development or artifact paths)" >&2
      exit 2 ;;
    *)
      artifacts+=("$1")
      shift ;;
  esac
done

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
if [[ -n "$key_fingerprint" ]]; then
  requested=$key_fingerprint
  key_fingerprint=$(normalize_fingerprint "$requested") || {
    echo "--key must be a full 40-hex fingerprint, not a key id or a name: got $requested" >&2
    exit 2
  }
fi

(( ${#artifacts[@]} )) || { echo 'No artifact to sign. Pass at least one file, for example dist/linux/RadanorSparrow-1.0.0-x86_64.AppImage.' >&2; exit 2; }
for artifact in "${artifacts[@]}"; do
  [[ -f "$artifact" && -s "$artifact" ]] || { echo "Not a non-empty file: $artifact" >&2; exit 2; }
done

# A release signature is what makes an artifact look approved, so signing a
# development build is refused unless it is asked for explicitly. The artifact's own
# provenance record is what decides, never a name or a flag passed here.
if (( ! development_allowed )); then
  for artifact in "${artifacts[@]}"; do
    provenance=$(dirname -- "$artifact")/BUILD-INFO
    [[ -f "$provenance" ]] || continue
    variant=$(awk -F= '$1 == "variant" { sub(/^[^=]*=/, ""); print; exit }' "$provenance")
    if [[ "$variant" != release ]]; then
      cat >&2 <<MSG
$(basename -- "$artifact") sits beside a BUILD-INFO that reports variant=${variant:-<absent>},
so it is a development build. Release signing signs release artifacts only: a
signature here would make a development build look approved. Nothing was signed.
Pass --development to sign a development build deliberately for pipeline testing,
and never publish the result.
MSG
      exit 1
    fi
  done
fi
if [[ -z "$armored_key" && -z "$key_fingerprint" ]]; then
  cat >&2 <<'MSG'
No signing key given. This script never generates one and never ships one:

  --armored-key FILE   an armored private key in a file (CI secret, or an export)
  --key FINGERPRINT    a key already in your own GnuPG keyring

Release signing is BLOCKED until Radanor has a key and publishes its fingerprint;
see docs/deployment/RELEASE_SIGNING.md.
MSG
  exit 2
fi
[[ -z "$armored_key" || -s "$armored_key" ]] || { echo "Signing key file is missing or empty: $armored_key" >&2; exit 2; }
[[ -n "$armored_key" || -n "${HOME:-}" || -n "${GNUPGHOME:-}" ]] || { echo 'No keyring location: set GNUPGHOME or HOME, or pass --armored-key.' >&2; exit 2; }
command -v gpg >/dev/null || { echo 'gpg is required to sign a release artifact' >&2; exit 2; }

# The caller's keyring is only read, and only through this path: everything else
# happens in an isolated home that is removed when the script ends.
caller_gnupg=${GNUPGHOME:-${HOME:-}/.gnupg}
gnupg=$(mktemp -d "${TMPDIR:-/tmp}/radanor-sign.XXXXXX")
trap 'rm -rf -- "$gnupg"' EXIT
export GNUPGHOME="$gnupg/gnupg"
mkdir -m 700 -p "$GNUPGHOME"

if [[ -n "$armored_key" ]]; then
  if ! gpg --batch --quiet --import "$armored_key" 2>/dev/null; then
    echo "Could not import a private key from $armored_key. Nothing was signed." >&2
    exit 1
  fi
else
  # The caller's own keyring is read, never written: only this fingerprint is
  # exported into the isolated home.
  if ! gpg --homedir "$caller_gnupg" --export-secret-keys --armor "$key_fingerprint" | gpg --batch --quiet --import 2>/dev/null; then
    echo "No usable private key for $key_fingerprint in this keyring. Check the fingerprint, or pass --armored-key with an export of the private key. Nothing was signed." >&2
    exit 1
  fi
fi

signing_fingerprint=$(gpg --batch --with-colons --list-secret-keys 2>/dev/null | awk -F: '/^fpr:/ {print $10; exit}')
if [[ -z "$signing_fingerprint" ]]; then
  echo 'The imported key has no usable secret key, so it cannot sign. Nothing was signed.' >&2
  exit 1
fi
if [[ -n "$key_fingerprint" && "$signing_fingerprint" != "$key_fingerprint" ]]; then
  echo "Imported key $signing_fingerprint does not match the requested $key_fingerprint. Nothing was signed." >&2
  exit 1
fi
if [[ -n "$expected" && "$signing_fingerprint" != "$expected" ]]; then
  echo "The provided key is $signing_fingerprint, not the trusted fingerprint $expected. Nothing was signed." >&2
  exit 1
fi

signed=0
for artifact in "${artifacts[@]}"; do
  if ! gpg --batch --yes --armor --detach-sign --local-user "$signing_fingerprint" \
    --output "$artifact.asc" "$artifact" 2>/dev/null; then
    echo "Signing $artifact failed (a passphrase-protected key cannot be used unattended). Nothing was published; earlier files in this run are unverified and must not be used." >&2
    rm -f -- "$artifact.asc"
    exit 1
  fi
  gpg --batch --armor --export "$signing_fingerprint" > "$artifact.asc.pub"

  # Prove the signature matches the file before telling anyone it does.
  status=$(gpg --batch --no-tty --trust-model always --status-fd 1 --verify "$artifact.asc" "$artifact" 2>/dev/null || true)
  if ! grep -q '^\[GNUPG:\] GOODSIG' <<<"$status"; then
    echo "The signature was written but does not verify against $artifact. Treat the output as unusable." >&2
    exit 1
  fi

  sha=$(sha256sum -- "$artifact" | cut -d' ' -f1)
  cat <<MSG
Signed: $(basename -- "$artifact")
  signature       $artifact.asc
  public key      $artifact.asc.pub
  fingerprint     $signing_fingerprint
  sha256          $sha

MSG
  signed=$((signed + 1))
done

if (( development_allowed )); then
  cat >&2 <<'MSG'
WARNING: --development was given. These signatures are real but the artifacts are
NOT a release: do not publish them, and do not present them as approved.
MSG
fi

cat <<MSG
Signed $signed artifact(s) with $signing_fingerprint.
Publish all of them together, and publish the fingerprint somewhere a downloader can
reach without the download directory (release notes, the repository, the site).
A signature whose fingerprint a user cannot check out of band proves nothing.
MSG
