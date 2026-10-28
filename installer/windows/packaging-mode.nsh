; Build identity is independent of signing policy. Direct legacy compilation of
; a release identity still defaults to SIGNED, never to an unsigned fallback.
!ifndef PACKAGING_MODE
 !if "${VARIANT}" == "development"
  !define PACKAGING_MODE "development"
 !else
  !define PACKAGING_MODE "signed-release"
 !endif
!endif
!if "${PACKAGING_MODE}" == "signed-release"
 !if "${VARIANT}" != "release"
  !error "Signed release requires release identity"
 !endif
 !ifndef SIGNING_SCRIPT
  !error "Release installer requires the reviewed uninstaller signer"
 !endif
 !ifndef SIGNING_HOST
  !error "Release installer requires an explicit PowerShell host"
 !endif
 !uninstfinalize '"${SIGNING_HOST}" -NoProfile -NonInteractive -File "${SIGNING_SCRIPT}" -Kind Uninstaller -Path "%1"' = 0
!else if "${PACKAGING_MODE}" == "community-unsigned"
 !if "${VARIANT}" != "release"
  !error "Community unsigned release requires release identity"
 !endif
 !ifdef SIGNING_SCRIPT
  !error "Community unsigned mode rejects signing hooks"
 !endif
 !ifdef SIGNING_HOST
  !error "Community unsigned mode rejects signing hooks"
 !endif
!else if "${PACKAGING_MODE}" == "development"
 !if "${VARIANT}" != "development"
  !error "Development mode requires development identity"
 !endif
 !ifdef SIGNING_SCRIPT
  !error "Development installer must not use the production signer"
 !endif
 !ifdef SIGNING_HOST
  !error "Development installer must not use the production signer"
 !endif
!else
 !error "Unknown installer packaging mode"
!endif
