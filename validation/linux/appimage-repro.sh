#!/usr/bin/env bash
# Reproducibility check for the Radanor Sparrow AppImage.
#
#   bash validation/linux/appimage-repro.sh [--ref REF] [--keep]
#
# Builds the AppImage twice, each from its own fresh clone of the same commit, and
# requires the two artifacts to be byte-identical. Cloning is what makes "the same
# committed source" literal: no uncommitted change, build output, or previous run
# can reach either build, so matching bytes are decided by the commit and the
# toolchain rather than by this working directory.
#
# It builds the whole path (frontend, supervisor, window component, AppImage), not
# just the packaging step, because a nondeterministic compiler or bundler output
# would show up there just as well. Exit status is non-zero on a mismatch.
set -euo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
ref=HEAD
keep=0
while (( $# )); do
  case "$1" in
    --ref)
      (( $# >= 2 )) || { echo '--ref needs a commit, tag or branch' >&2; exit 1; }
      ref=$2
      shift 2
      ;;
    --keep)
      keep=1
      shift
      ;;
    -h|--help)
      sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "Unknown argument: $1 (expected --ref <ref> or --keep)" >&2
      exit 1
      ;;
  esac
done

for tool in git sha256sum go node npm; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 1; }
done
commit=$(git -C "$repo" rev-parse --verify --quiet "$ref^{commit}") || {
  echo "Cannot resolve $ref to a commit in $repo" >&2
  exit 1
}

work=$(mktemp -d "${TMPDIR:-/tmp}/radanor-appimage-repro.XXXXXX")
cleanup() {
  if (( keep )); then
    echo "Kept both builds and their logs in $work"
  else
    rm -rf -- "$work"
  fi
}
trap cleanup EXIT

echo "Commit: $commit"
hashes=()
names=()
for n in 1 2; do
  dir="$work/build-$n"
  log="$work/build-$n.log"
  # --shared borrows objects from this repository instead of copying them; the
  # checkout is still a separate, clean tree with its own git metadata, so the
  # version and the source date are derived from the commit exactly as they are in
  # a normal checkout.
  git clone --quiet --shared --no-checkout "$repo" "$dir"
  git -C "$dir" checkout --quiet --detach "$commit"
  if [[ -n "$(git -C "$dir" status --porcelain --untracked-files=normal)" ]]; then
    echo "The fresh clone of $commit is not clean, so it cannot prove reproducibility" >&2
    exit 1
  fi

  echo "Building $n of 2 in $dir ..."
  if ! bash "$dir/scripts/build-linux.sh" --appimage >"$log" 2>&1; then
    echo "Build $n failed; the last lines of $log:" >&2
    tail -30 "$log" >&2
    exit 1
  fi
  appimage=$(ls -1 "$dir"/dist/linux/RadanorSparrow-*-x86_64.AppImage 2>/dev/null | head -1 || true)
  [[ -f "$appimage" ]] || { echo "Build $n produced no AppImage; see $log" >&2; exit 1; }
  name=$(basename -- "$appimage")
  # The clone is clean, so a version that calls itself dirty would mean the
  # uncommitted-change check is not seeing something it should.
  if [[ "$name" == *".dirty"* ]]; then
    echo "Build $n came from a clean clone of $commit but its version reports .dirty: $name" >&2
    exit 1
  fi
  hashes+=("$(sha256sum -- "$appimage" | cut -d' ' -f1)")
  names+=("$name")
  size=$(stat -c %s -- "$appimage")
  printf 'Build %s: %s  %s (%s bytes)\n' "$n" "${hashes[$((n - 1))]}" "$name" "$size"
done

if [[ "${names[0]}" != "${names[1]}" ]]; then
  echo "NOT REPRODUCIBLE: the two builds disagree about the version or variant: ${names[0]} vs ${names[1]}" >&2
  keep=1
  exit 1
fi
if [[ "${hashes[0]}" == "${hashes[1]}" ]]; then
  echo "REPRODUCIBLE: two clean builds of $commit produced one AppImage byte for byte."
  exit 0
fi

echo "NOT REPRODUCIBLE: the two builds of $commit differ." >&2
a=$(ls -1 "$work"/build-1/dist/linux/*-x86_64.AppImage | head -1)
b=$(ls -1 "$work"/build-2/dist/linux/*-x86_64.AppImage | head -1)
cmp -l "$a" "$b" >"$work/differing-bytes.txt" 2>/dev/null || true
echo "differing bytes: $(wc -l < "$work/differing-bytes.txt")" >&2
head -10 "$work/differing-bytes.txt" >&2
echo "Both artifacts are kept for inspection." >&2
keep=1
exit 1
