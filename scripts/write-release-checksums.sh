#!/usr/bin/env bash
# Writes the SHA-256 manifests that accompany the Linux distribution artifacts.
#
#   bash scripts/write-release-checksums.sh [directory]     # default dist/linux
#
# Two manifests, because a release publishes two different artifacts:
#
#   SHA256SUMS.appimage   the AppImage (the native-window desktop artifact)
#   SHA256SUMS.portable   the portable supervisor, LICENSE and the notices
#
# A checksum proves the file reached the user unchanged; it does not prove who
# published it. That distinction, and the signing mechanism that would close it,
# are in docs/deployment/RELEASE_SIGNING.md.
set -euo pipefail
dir=${1:-dist/linux}
[[ -d "$dir" ]] || { echo "No such directory: $dir" >&2; exit 1; }
cd "$dir"

# Exactly one AppImage: two would make the manifest name the wrong file, or a
# stale build would be published beside the new one.
appimages=()
while IFS= read -r -d '' candidate; do
  appimages+=("$candidate")
done < <(find . -maxdepth 1 -name 'RadanorSparrow-*-x86_64.AppImage' -print0)
if (( ${#appimages[@]} != 1 )); then
  echo "Expected exactly one RadanorSparrow-*-x86_64.AppImage in $dir, found ${#appimages[@]}. Build the artifact before writing its checksums." >&2
  exit 1
fi
appimage=${appimages[0]#./}

for required in "$appimage" radanor-sparrow LICENSE THIRD_PARTY_NOTICES.md; do
  [[ -s "$required" ]] || { echo "Missing or empty: $dir/$required" >&2; exit 1; }
done

# A manifest is the file a downloader compares against, and the release workflow
# uploads these two alongside the binaries. Writing them for a development build
# would let that build travel with exactly the supporting files a release has, so
# the artifact's own BUILD-INFO has to say it came from the release path first.
# The check reads the record the build wrote, never a name or a flag passed here.
[[ -f BUILD-INFO ]] || {
  echo "Missing $dir/BUILD-INFO: checksums are written for an artifact a build produced, so its provenance must be present." >&2
  exit 1
}
variant=$(awk -F= '$1 == "variant" { sub(/^[^=]*=/, ""); print; exit }' BUILD-INFO)
if [[ "$variant" != release ]]; then
  echo "Refusing to write release checksums: $dir/BUILD-INFO records variant=${variant:-unknown}, not release." >&2
  echo 'A development build is not a release artifact. Build with scripts/build-linux.sh --release.' >&2
  exit 1
fi

sha256sum -- "$appimage" > SHA256SUMS.appimage
sha256sum -- radanor-sparrow LICENSE THIRD_PARTY_NOTICES.md > SHA256SUMS.portable

cat SHA256SUMS.appimage SHA256SUMS.portable
echo "Checksums written to $dir/SHA256SUMS.appimage and $dir/SHA256SUMS.portable."
