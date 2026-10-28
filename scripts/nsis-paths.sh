#!/usr/bin/env bash
# Path handling for the NSIS installer build. Sourced by
# scripts/build-windows-installer.sh; nothing here writes or builds anything.
#
# The rule these functions exist to enforce: every path that reaches makensis is
# a whole, absolute path in the *host's own* separators, and no part of it is
# assembled inside installer/windows/radanor-sparrow.nsi.
#
# Why: NSIS resolves a `File` path by splitting it at the platform's separator to
# find the name portion. A path that mixes conventions therefore names something
# else. The Windows CI failure was exactly that. The script handed NSIS a
# directory from `cygpath -w` and the .nsi joined the file name with "/", so
# makensis received
#
#   D:\a\radanor-shield\radanor-shield\dist\windows/RadanorSparrow.exe
#
# split it after the last "\" and looked for a file called
# `windows/RadanorSparrow.exe` inside `dist`. The executable was there the whole
# time -- the Go artifact check had just read it -- and makensis still reported
# "no files found".

# nsis_native_path <absolute-path>
#
# Prints the path in the form NSIS expects on this host: the POSIX path on Linux,
# a `C:\...` path under Git Bash on Windows.
#
# `-w` rather than `-m` is deliberate. `-m` would produce `D:/a/...`, and forward
# slashes are the form the failing run proved makensis does not resolve on
# Windows: with no native separator in the string there is no name portion NSIS
# can find at all.
nsis_native_path() {
  local path=${1-}
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w -- "$path"
    return
  fi
  printf '%s\n' "$path"
}

# nsis_path_problem <value>
#
# Prints the reason a value must not be handed to makensis, or nothing when the
# value is usable. The build calls this for every path it is about to pass, so a
# path defect stops the build with the offending value named, instead of
# surfacing as NSIS's bare "no files found" far from the cause.
#
# Only bash builtins are used, so the same function can be exercised with an
# empty PATH.
nsis_path_problem() {
  local value=${1-}
  if [[ -z "$value" ]]; then
    printf '%s\n' 'the path is empty'
    return 0
  fi
  if [[ "$value" == *'"'* ]]; then
    printf '%s\n' 'the path contains a double quote, which would end the NSIS string'
    return 0
  fi
  if [[ "$value" == *'$'* ]]; then
    printf '%s\n' 'the path contains $, which NSIS would expand as a variable'
    return 0
  fi
  if [[ "$value" == *$'\n'* || "$value" == *$'\r'* || "$value" == *$'\t'* ]]; then
    printf '%s\n' 'the path contains a line break or a tab'
    return 0
  fi
  # One convention, throughout. Mixed separators are what broke the Windows
  # build, and they are never intentional.
  if [[ "$value" == *\\* && "$value" == */* ]]; then
    printf '%s\n' 'the path mixes \ and / separators, so NSIS cannot find its name portion'
    return 0
  fi
  # On a host with cygpath, makensis is a Windows program and needs native
  # separators. A drive path with forward slashes is absolute but is not a form
  # Windows NSIS resolves -- it is what `cygpath -m` would produce, so this check
  # also stops a switch from -w to -m from reaching CI.
  if command -v cygpath >/dev/null 2>&1 && [[ "$value" =~ ^[A-Za-z]:/ ]]; then
    printf '%s\n' 'the path uses forward slashes on a Windows host, where NSIS needs native separators'
    return 0
  fi
  # A relative path would be resolved by NSIS against the .nsi rather than the
  # checkout, which is a different file. A drive-qualified path, a UNC share and
  # a POSIX path are all absolute; anything else is not.
  if [[ "$value" != /* && ! "$value" =~ ^[A-Za-z]:[/\\] && "$value" != \\\\* ]]; then
    printf '%s\n' 'the path is not absolute'
    return 0
  fi
  return 0
}
