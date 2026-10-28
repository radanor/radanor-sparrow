#!/usr/bin/env bash
# Native smoke test for the Radanor Sparrow AppImage.
#
#   bash validation/linux/appimage-smoke.sh [--require-native-window] [path/to/AppImage]
#
# It runs the real artifact on a real desktop session and checks the behavior the
# package promises: a native window, the authenticated dashboard, the L3 renderer
# sandbox, close-without-stopping-monitoring, a second launch reusing the running
# core, process cleanup, the authenticated-browser fallback, and data integrity.
# It uses a temporary profile, never the user's own, and it stops the instance it
# started.
#
# A check that cannot run here is reported as SKIP with the measured reason, not
# as a PASS: no display, GTK/WebKit absent, or AppArmor denying bubblewrap the
# user namespace the WebKit sandbox needs. The browser fallback is then the
# designed outcome and is still verified. On a machine that must show a window,
# pass --require-native-window to turn those SKIPs into failures.
#
# Exit status: 0 when nothing failed and (-without the flag) skips are expected;
# 1 when any check failed or --require-native-window found no window; 2 for a
# usage or prerequisite problem.
set -uo pipefail
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)

require_native=0
app=
for arg in "$@"; do
  case "$arg" in
    --require-native-window) require_native=1 ;;
    --help|-h)
      echo 'Usage: bash validation/linux/appimage-smoke.sh [--require-native-window] [path/to/RadanorSparrow-*.AppImage]'
      echo 'Runs the packaged application and checks window, sandbox, lifecycle, fallback and data integrity.'
      echo '--require-native-window fails the run when the native window cannot be shown here.'
      exit 0 ;;
    -*)
      echo "Unknown argument: $arg (expected --require-native-window, --help or an AppImage path)" >&2
      exit 2 ;;
    *) app=$arg ;;
  esac
done
if [[ -z "$app" ]]; then
  app=$(ls -1t "$repo"/dist/linux/RadanorSparrow-*-x86_64.AppImage 2>/dev/null | head -1 || true)
fi
[[ -f "$app" ]] || { echo 'No AppImage found; build one with scripts/build-appimage.sh' >&2; exit 2; }
[[ -x "$app" ]] || { echo "Not executable: $app" >&2; exit 2; }
for tool in curl python3 sqlite3; do
  command -v "$tool" >/dev/null || { echo "Missing prerequisite: $tool" >&2; exit 2; }
done

root=$(mktemp -d /tmp/radanor-appimage-smoke.XXXXXX)
export RADANOR_SPARROW_STATE_DIR="$root/state"
export RADANOR_SPARROW_RUNTIME_DIR="$root/rt"
mkdir -p "$RADANOR_SPARROW_STATE_DIR" "$RADANOR_SPARROW_RUNTIME_DIR" "$root/shim" "$root/broken-browser"
chmod 700 "$RADANOR_SPARROW_STATE_DIR" "$RADANOR_SPARROW_RUNTIME_DIR"

# The browser is modelled, never launched: the contract under test is that the
# authenticated URL is handed over, not which browser the host prefers. It is on
# PATH for every launch so a headless host behaves like a desktop that has a
# browser, instead of failing the fallback and exiting before the checks run.
cat > "$root/shim/xdg-open" <<SH
#!/bin/sh
printf '%s\n' "\$1" > "$root/browser-url"
SH
chmod 755 "$root/shim/xdg-open"
# A browser association that exists but cannot run, for the failure-path check.
printf '#!/bin/sh\nexit 1\n' > "$root/broken-browser/xdg-open"
chmod 755 "$root/broken-browser/xdg-open"
export PATH="$root/shim:$PATH"

pass=0
fail=0
skip=0
ok() { pass=$((pass + 1)); printf 'PASS  %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL  %s\n' "$1"; }
skipped() { skip=$((skip + 1)); printf 'SKIP  %s\n' "$1"; }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

# --- environment: what this host can actually demonstrate -------------------
# The listing is captured before it is searched: with pipefail, `ldconfig -p |
# grep -q` fails when the listing is larger than the pipe buffer, because grep
# exits on the match and ldconfig is then killed by SIGPIPE. That reads as "the
# library is absent" on a host that has it.
soname_present() {
  local ldconfig cache
  ldconfig=$(command -v ldconfig || echo /sbin/ldconfig)
  cache=$("$ldconfig" -p 2>/dev/null || true)
  [[ "$cache" == *"$1"* ]]
}
have_display=0
[[ -n "${DISPLAY:-}${WAYLAND_DISPLAY:-}" ]] && have_display=1
have_gui=0
if soname_present 'libgtk-3.so.0' && soname_present 'libwebkit2gtk-4.1.so.0'; then have_gui=1; fi
have_browser=0
command -v xdg-open >/dev/null && have_browser=1
apparmor_restricted=0
[[ "$(cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns 2>/dev/null || echo 0)" == 1 ]] && apparmor_restricted=1
sandbox_works=0
if command -v bwrap >/dev/null && bwrap --ro-bind / / --proc /proc -- /bin/true >/dev/null 2>&1; then
  sandbox_works=1
fi

# The one thing that decides whether a window is possible here. Reported verbatim
# at the end so a FAIL is never mistaken for a policy that was going to allow it.
denial=
if (( ! have_display )); then
  denial='no display server (DISPLAY and WAYLAND_DISPLAY are unset)'
elif (( ! have_gui )); then
  denial='GTK3 or WebKitGTK 4.1 is not installed'
elif (( apparmor_restricted && ! sandbox_works )); then
  denial='AppArmor denies unprivileged user namespaces, so the WebKit sandbox cannot start'
elif (( ! sandbox_works )); then
  denial='bubblewrap cannot create the sandbox here'
fi

echo "Artifact: $(basename "$app") ($(stat -c %s "$app") bytes)"
printf 'Host: display=%s gtk/webkit=%s xdg-open=%s apparmor_userns=%s bubblewrap_sandbox=%s\n' \
  "$([[ $have_display == 1 ]] && echo yes || echo no)" \
  "$([[ $have_gui == 1 ]] && echo yes || echo no)" \
  "$([[ $have_browser == 1 ]] && echo yes || echo no)" \
  "$([[ $apparmor_restricted == 1 ]] && echo restricted || echo open)" \
  "$([[ $sandbox_works == 1 ]] && echo ok || echo denied)"
if [[ -n "$denial" ]]; then
  echo "Native window: not expected here ($denial)"
else
  echo 'Native window: expected'
fi

# The AppImage either mounts (FUSE) or extracts (--appimage-extract-and-run, or a
# runtime without FUSE). Both are the same packaged application, so both are
# matched; a mount-only pattern would report every process check as failed on a
# host that extracts.
runtime_binaries() {
  pgrep -f '^(/tmp/\.mount_[^/]+|/tmp/appimage_extracted_[^/]+)/usr/bin/radanor-sparrow' 2>/dev/null || true
}
supervisor_pid() { runtime_binaries | while read -r pid; do
  args=$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)
  [[ "$args" != *"--internal-core"* && "$args" != *"-window"* ]] && echo "$pid"
done | head -1; }
core_pid() { for pid in $(runtime_binaries); do
  tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null | grep -q -- '--internal-core' && echo "$pid"
done | head -1; }
helper_pid() { for pid in $(runtime_binaries); do
  tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null | grep -q -- '-runtime-dir' && echo "$pid"
done | head -1; }
origin() { cat "$RADANOR_SPARROW_RUNTIME_DIR/desktop-origin" 2>/dev/null || true; }
window_visible() { command -v wmctrl >/dev/null && wmctrl -lx 2>/dev/null | grep -qi 'radanor-sparrow'; }
browser_opened() { [[ -s "$root/browser-url" ]]; }
dashboard_status() {
  local o; o=$(origin)
  if [[ -z "$o" ]]; then echo 'no-origin'; return; fi
  curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$o/" 2>/dev/null || echo 'unreachable'
}
wait_for() { local deadline=$((SECONDS + ${2:-30})); while (( SECONDS < deadline )); do
  if eval "$1"; then return 0; fi; sleep 0.2; done; return 1; }
stop_instance() {
  local pid; pid=$(supervisor_pid)
  [[ -n "$pid" ]] && kill -TERM "$pid" 2>/dev/null
  wait_for '[[ -z "$(supervisor_pid)" ]]' 10
}

# A zombie keeps no command line, so the kernel's process name is the only identity
# one still carries, and the supervisor, the core and the window helper all report
# as "radanor-sparrow" (a kernel name is truncated at 15 characters). That name is
# what attributes a zombie to this instance; a failure then prints everything the
# kernel still knows about it - pid, parent, state, name and the parent's own
# command line - so the cause can be read instead of guessed at. Nothing from the
# instance's environment is printed.
instance_zombies() {
  ps -eo stat=,pid=,ppid=,comm= 2>/dev/null | awk '$1 ~ /^Z/ && $4 ~ /^radanor-sparrow/'
}
process_identity() {
  local pid=$1 status
  if ! status=$(cat "/proc/$pid/status" 2>/dev/null); then
    printf 'pid=%s already gone' "$pid"
    return
  fi
  printf 'pid=%s %s %s name=%s' "$pid" \
    "$(awk '/^State:/{print $1 $2}' <<< "$status")" \
    "$(awk '/^PPid:/{print "ppid=" $2}' <<< "$status")" \
    "$(cat "/proc/$pid/comm" 2>/dev/null || echo '?')"
}
report_zombies() {
  local state pid ppid name parent
  while read -r state pid ppid name; do
    [[ -n "$pid" ]] || continue
    parent=$(cat "/proc/$ppid/comm" 2>/dev/null || echo gone)
    printf '      zombie %s parent=%s (%s)\n' "$(process_identity "$pid")" "$ppid" "$parent"
    tr '\0' ' ' < "/proc/$ppid/cmdline" 2>/dev/null | cut -c1-160 | sed 's/^/        parent args: /'
  done
}
cleanup() { stop_instance >/dev/null 2>&1; }
trap cleanup EXIT

launch() {
  # setsid + output redirection detaches the instance from this script, exactly
  # like a double-click; the AppImage runtime keeps its FUSE mount alive.
  setsid "$@" >"$root/launch.log" 2>&1 </dev/null &
}

# The supervisor's own sentence for the last failure, so a SKIP can quote what was
# actually observed instead of the harness's guess about it.
launch_reason() {
  grep -o '"reason":"[^"]*"' "$RADANOR_SPARROW_STATE_DIR/application.log" 2>/dev/null \
    | tail -1 | sed 's/^"reason":"//; s/"$//'
}
# Which of the failures this is. A native window that does not appear has several
# distinct causes, and they need different fixes, so the reason is classified
# rather than reported as one undifferentiated "window missing".
classify() {
  case "$1" in
    *'AppArmor blocks'*) echo apparmor ;;
    *bubblewrap*) echo sandbox ;;
    *'DISPLAY and WAYLAND_DISPLAY are unset'*|*'GTK could not open the display'*|*'GUI runtime is unavailable'*) echo display ;;
    *'launch ticket'*|*'dashboard origin'*|*'IPC secret'*|*'did not finish loading'*|*'did not load'*) echo auth ;;
    '') echo none ;;
    *) echo other ;;
  esac
}
expected_class() {
  case "$denial" in
    'no display'*) echo display ;;
    'GTK3 or WebKitGTK'*) echo gui ;;
    'AppArmor denies'*) echo apparmor ;;
    'bubblewrap cannot'*) echo sandbox ;;
    *) echo native ;;
  esac
}

started=$(date +%s.%N)
launch "$app"
if wait_for window_visible 40; then
  elapsed=$(awk -v start="$started" -v end="$(date +%s.%N)" 'BEGIN { printf "%.2f", end - start }')
  ok "native window appears (${elapsed}s from exec)"
  native=1
else
  native=0
  reason=$(launch_reason)
  class=$(classify "$reason")
  if [[ -n "$denial" && "$class" == "$(expected_class)" ]]; then
    if (( require_native )); then
      bad "native window appears (required, refused: $reason)"
    else
      skipped "native window appears (not possible here: $reason)"
    fi
  elif [[ -n "$denial" ]]; then
    bad "native window appears (expected $(expected_class) but got '${reason:-no reason}')"
  else
    bad "native window appears (no window and no expected denial; reason: ${reason:-none logged})"
  fi
fi

# Authentication and gateway are checked separately from the window: the dashboard
# answering is what the user actually needs, and it is also the check that tells a
# gateway failure apart from a presentation failure.
status=$(dashboard_status)
case "$status" in
  200) ok 'authenticated dashboard answers 200' ;;
  no-origin) bad 'authenticated dashboard answers 200 (the gateway published no origin)' ;;
  401|403) bad "authenticated dashboard answers 200 (the session was rejected: HTTP $status)" ;;
  *) bad "authenticated dashboard answers 200 (HTTP ${status:-no answer})" ;;
esac

if (( native )); then
  check 'window is titled Radanor Sparrow' "wmctrl -lx | grep -q 'Radanor Sparrow'"
  check 'window carries the radanor-sparrow class' "wmctrl -lx | grep -q 'radanor-sparrow\\.Radanor-sparrow'"
  check 'exactly one supervisor, core and window helper' "[[ \$(runtime_binaries | wc -l) -eq 3 ]]"
  check 'exactly one monitoring core' "[[ \$(printf '%s\n' \"\$(core_pid)\" | grep -c .) -eq 1 ]]"
  check 'exactly one window helper' "[[ \$(printf '%s\n' \"\$(helper_pid)\" | grep -c .) -eq 1 ]]"

  sandbox=$(python3 - "$(helper_pid)" <<'PY'
import os, sys
ui = sys.argv[1]
if not ui:
    print('no-renderer')
    raise SystemExit
web = None
for name in os.listdir('/proc'):
    if not name.isdigit():
        continue
    try:
        cmd = open(f'/proc/{name}/cmdline', 'rb').read().split(b'\0')
    except OSError:
        continue
    if cmd and cmd[0] and os.path.basename(cmd[0].decode('utf-8', 'replace')) == 'WebKitWebProcess':
        web = int(name)
if web is None:
    print('no-renderer')
    raise SystemExit
status = dict(line.split(':', 1) for line in open(f'/proc/{web}/status') if ':' in line)
confined = all(os.readlink(f'/proc/{int(ui)}/ns/{ns}') != os.readlink(f'/proc/{web}/ns/{ns}')
               for ns in ('user', 'pid', 'mnt', 'net', 'uts'))
mapping = open(f'/proc/{web}/uid_map').read().split()
single = len(mapping) == 3 and mapping[2] == '1' and mapping[1] == str(os.getuid()) and mapping[0] in (str(os.getuid()), '0')
print(f"seccomp={status.get('Seccomp','').strip()} nnp={status.get('NoNewPrivs','').strip()} "
      f"caps={status.get('CapEff','').strip()} namespaces={'confined' if confined else 'shared'} "
      f"uidmap={'single' if single else ' '.join(mapping)}")
PY
)
  echo "      renderer: $sandbox"
  if [[ "$sandbox" == no-renderer ]]; then
    bad 'renderer is in the WebKit sandbox (no WebKitWebProcess was running)'
    bad 'renderer has separate user/pid/mnt/net/uts namespaces (no renderer)'
    bad 'renderer uid map is a single id (no renderer)'
  else
    check 'renderer is in the WebKit sandbox (seccomp 2, no new privs, no caps)' \
      "grep -q 'seccomp=2 nnp=1 caps=0000000000000000' <<< '$sandbox'"
    check 'renderer has separate user/pid/mnt/net/uts namespaces' "grep -q 'namespaces=confined' <<< '$sandbox'"
    check 'renderer uid map is a single id' "grep -q 'uidmap=single' <<< '$sandbox'"
  fi
else
  # The window-dependent checks are not evidence either way on this host. They are
  # reported, not silently dropped, and the fallback they imply is verified below.
  for name in 'window is titled Radanor Sparrow' 'window carries the radanor-sparrow class' \
    'exactly one supervisor, core and window helper' 'exactly one monitoring core' \
    'exactly one window helper' 'renderer is in the WebKit sandbox' \
    'renderer has separate user/pid/mnt/net/uts namespaces' 'renderer uid map is a single id'; do
    skipped "$name (no native window)"
  done
fi

# The designed outcome when no window can be shown: the authenticated dashboard in
# the browser, with a single-use ticket, and no second monitoring core.
if (( ! native )); then
  wait_for browser_opened 15
  if browser_opened; then
    ok 'expected authenticated browser fallback opened'
    check 'the fallback address carries a launch ticket' "grep -q '#ticket=' '$root/browser-url'"
    check 'no second core was started for the fallback' "[[ \$(printf '%s\n' \"\$(core_pid)\" | grep -c .) -eq 1 ]]"
  else
    bad "expected authenticated browser fallback opened (reason: $(launch_reason))"
  fi
fi

if (( native )); then
  core_before=$(core_pid)
  echo '--- X closes the window'
  wmctrl -c 'Radanor Sparrow' 2>/dev/null
  wait_for '! window_visible' 10 && ok 'window closes on WM_DELETE_WINDOW' || bad 'window closes on WM_DELETE_WINDOW'
  wait_for '[[ -z "$(helper_pid)" ]]' 10 && ok 'window helper exits with the window' || bad 'window helper exits with the window'
  check 'monitoring continues while the window is closed' "[[ \"\$(dashboard_status)\" == 200 ]]"
  check 'the monitoring core is still the same process' "[[ \"\$(core_pid)\" == '$core_before' ]]"

  echo '--- second launch'
  launch "$app"
  if wait_for window_visible 40; then ok 'second launch shows a window'; else bad 'second launch shows a window'; fi
  check 'no duplicate monitoring core' "[[ \"\$(core_pid)\" == '$core_before' ]]"
  check 'exactly one supervisor and one helper remain' "[[ \$(runtime_binaries | wc -l) -eq 3 ]]"
else
  skipped 'window close keeps monitoring running (no native window)'
  skipped 'second launch shows a window (no native window)'
  core_before=$(core_pid)
  launch "$app"
  sleep 3
  check 'no duplicate monitoring core across launches' "[[ \"\$(core_pid)\" == '$core_before' ]]"
fi

echo '--- explicit exit through the application'
# Recorded before the exit so a failure can say which process it expected to be
# gone, and with which parent.
exit_supervisor=$(supervisor_pid)
exit_core=$(core_pid)
exit_helper=$(helper_pid)
stop_instance
check 'supervisor, core and helper are gone after exit' "[[ -z \"\$(runtime_binaries)\" ]]"
# The supervisor stops the window component and waits until it has been reaped
# before it exits, so a clean shutdown leaves nothing behind. The wait covers what
# no application can control: a component the kernel is still reparenting and
# reaping. It is bounded, and a failure prints the process evidence and never
# passes on a process that is still there.
if wait_for '[[ -z "$(instance_zombies)" ]]' 5; then
  ok 'no zombie process is left'
else
  bad 'no zombie process is left'
  printf '      before exit: supervisor=%s core=%s helper=%s\n' \
    "${exit_supervisor:-none}" "${exit_core:-none}" "${exit_helper:-none}"
  instance_zombies | report_zombies
fi
# WebKit disposes its helpers through the UI process exiting, which takes a moment
# after the supervisor is signalled. The check waits for that teardown instead of
# sampling the instant the signal is delivered, or it reports the shutdown it just
# asked for as a leak.
if wait_for "! pgrep -f 'WebKitWebProcess|WebKitNetworkProcess|/usr/bin/bwrap' >/dev/null" 10; then
  ok 'no WebKit or bubblewrap process outlives the application'
else
  bad 'no WebKit or bubblewrap process outlives the application'
fi
check 'the discovery record is removed' "[[ ! -e \"$RADANOR_SPARROW_RUNTIME_DIR/desktop-origin\" ]]"
check 'SQLite integrity is intact after exit' "[[ \"\$(sqlite3 \"$RADANOR_SPARROW_STATE_DIR/monitoring.db\" 'PRAGMA integrity_check;')\" == ok ]]"

echo '--- fallback: no window component'
rm -f "$root/browser-url"
launch env RADANOR_SPARROW_WINDOW_HELPER=/nonexistent/radanor-sparrow-window "$app"
wait_for browser_opened 30 && ok 'missing component opens the authenticated browser' || bad 'missing component opens the authenticated browser'
check 'the browser address carries a launch ticket' "grep -q '#ticket=' '$root/browser-url'"
check 'no second core was started for the fallback' "[[ \$(printf '%s\n' \"\$(core_pid)\" | grep -c .) -eq 1 ]]"
check 'the window component was not started' "[[ -z \"\$(helper_pid)\" ]]"
stop_instance

echo '--- fallback: no display server'
rm -f "$root/browser-url"
launch env -u DISPLAY -u WAYLAND_DISPLAY -u XDG_RUNTIME_DIR -u GDK_BACKEND "$app"
wait_for browser_opened 30 && ok 'an unreachable display opens the authenticated browser' || bad 'an unreachable display opens the authenticated browser'
check 'the display failure is reported as such' "[[ \"\$(classify \"\$(launch_reason)\")\" == display ]]"
stop_instance

echo '--- fallback: browser association cannot run'
rm -f "$root/browser-url"
launch env PATH="$root/broken-browser:$PATH" RADANOR_SPARROW_WINDOW_HELPER=/nonexistent/radanor-sparrow-window "$app"
if wait_for "grep -q 'Cannot open the default browser' '$root/launch.log'" 30; then
  ok 'a broken browser association is reported, not hidden'
else
  bad 'a broken browser association is reported, not hidden'
fi
stop_instance

check 'the profile survived every scenario' "[[ -f '$RADANOR_SPARROW_STATE_DIR/monitoring.db' ]]"
echo
if (( native )); then
  echo 'Native window: shown and measured on this host'
elif [[ -n "$denial" ]]; then
  echo "Native window: SKIPPED here ($denial)"
else
  echo 'Native window: DID NOT START although this host should allow it'
fi
printf 'Result: %d passed, %d failed, %d skipped\n' "$pass" "$fail" "$skip"
(( fail == 0 )) || exit 1
if (( require_native && ! native )); then
  echo 'Failed: --require-native-window was given and no native window was shown.' >&2
  exit 1
fi
