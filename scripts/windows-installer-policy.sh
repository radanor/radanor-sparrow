#!/usr/bin/env bash
# Sourced policy predicates, also exercised independently by negative tests.
# Only the build entry point may invoke these after reading real BUILD-INFO.
require_community_unsigned_authorization() {
  [[ "${RADANOR_COMMUNITY_UNSIGNED:-}" == acknowledged ]] || {
    echo 'Refusing an unsigned Windows installer: RADANOR_COMMUNITY_UNSIGNED acknowledgement required; see docs/deployment/COMMUNITY_UNSIGNED_DISTRIBUTION_POLICY.md.' >&2; return 1;
  }
  if [[ "${RADANOR_WINDOWS_SIGNING:-}" == enabled || "${RADANOR_RELEASE_SIGNING:-}" == enabled ]]; then
    echo 'Refusing --community-unsigned while production signing is enabled; use --release.' >&2; return 1
  fi
  [[ "${RADANOR_COMMUNITY_UNSIGNED_RELEASE:-}" == approved ]] || {
    echo 'Community owner approval RADANOR_COMMUNITY_UNSIGNED_RELEASE is required.' >&2; return 1;
  }
  [[ "${RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION:-}" == approved ]] || {
    echo 'Separate Windows approval RADANOR_UNSIGNED_WINDOWS_DISTRIBUTION is required.' >&2; return 1;
  }
}

assert_windows_release_identity() {
  local variant=$1 recorded=$2 canonical=$3
  [[ "$variant" == release ]] || { echo 'Refusing non-release build identity' >&2; return 1; }
  [[ "$canonical" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Refusing dirty, untagged or invalid release identity' >&2; return 1; }
  [[ "$recorded" == "$canonical" ]] || { echo 'Refusing mismatched release identity' >&2; return 1; }
}
