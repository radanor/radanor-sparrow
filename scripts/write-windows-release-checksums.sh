#!/usr/bin/env bash
# Write the manifest for the complete Windows release candidate. This does not
# sign anything; Authenticode verification is a separate mandatory release gate.
set -euo pipefail
dir=${1:-dist/windows}
[[ -d "$dir" ]] || { echo "No such directory: $dir" >&2; exit 1; }
cd "$dir"

setups=()
while IFS= read -r -d '' candidate; do setups+=("${candidate#./}"); done \
  < <(find . -maxdepth 1 -name 'RadanorSparrow-*-setup.exe' -print0)
if (( ${#setups[@]} != 1 )); then
  echo "Expected exactly one Windows setup executable, found ${#setups[@]}." >&2
  exit 1
fi

for required in RadanorSparrow.exe "${setups[0]}" LICENSE THIRD_PARTY_NOTICES.md BUILD-INFO; do
  [[ -s "$required" ]] || { echo "Missing or empty: $dir/$required" >&2; exit 1; }
done
variant=$(awk -F= '$1 == "variant" { sub(/^[^=]*=/, ""); print; exit }' BUILD-INFO)
if [[ "$variant" != release ]]; then
  echo "Refusing Windows release checksums: BUILD-INFO records variant=${variant:-unknown}, not release." >&2
  exit 1
fi

sha256sum -- RadanorSparrow.exe "${setups[0]}" LICENSE THIRD_PARTY_NOTICES.md BUILD-INFO \
  > SHA256SUMS.windows
sha256sum --check --strict SHA256SUMS.windows
cat SHA256SUMS.windows
