# Windows WebView2 desktop window

Radanor Sparrow uses the installed Microsoft Edge WebView2 Evergreen Runtime on
Windows. The Windows build remains one CGO-free `RadanorSparrow.exe`: the small
Microsoft WebView2 loader is embedded by `github.com/jchv/go-webview2`; Chromium
is not bundled. Linux continues to launch the authenticated dashboard with
`xdg-open` and has no WebView2, GTK or WebKitGTK dependency.

## Launch and fallback

The desktop supervisor starts the managed core and loopback gateway exactly as
before, requests a single-use launch ticket over authenticated IPC, and navigates
the WebView2 window to `http://127.0.0.1:<port>/#ticket=<ticket>`. The React client
redeems that ticket through the existing Origin- and Host-validated endpoint and
receives the existing HttpOnly session cookie. There is no JavaScript-to-native
bridge.

If WebView2 cannot be created, Sparrow opens the same one-time authenticated URL
with the default browser. The application does not download or install a runtime,
and it never weakens authentication to make fallback work. Administrators may
deploy Microsoft's Evergreen Runtime using their normal Windows software policy.

The embedded view blocks top-level navigation initiated by links or forms when
the destination is outside its exact loopback origin. Developer tools and default
context menus are disabled by the WebView2 binding in non-debug mode.

## Lifecycle

- The native title is **Radanor Sparrow** and the standard Windows frame supplies
  minimize, maximize and close controls.
- Closing the window hides it. Monitoring and the managed core continue.
- The notification-area icon uses the committed `web/public/favicon.ico` bytes.
  Its menu provides **Open Radanor Sparrow** and **Exit Radanor Sparrow**.
- A duplicate executable launch is rejected by the existing private lifecycle
  lock and restores/focuses the existing native window. If the original instance
  is using browser fallback, the duplicate issues a new one-time ticket and opens
  a new authenticated browser view.
- Explicit Exit from either React or the notification-area menu cancels the
  supervisor. The gateway shuts down and the managed core receives its existing
  graceful stop signal; Windows Job Object containment remains the crash fallback.

WebView2 profile data is stored below Sparrow's existing private state directory
in `webview2`. Assessment history, monitoring records and settings remain in the
existing compatible Shield/Sparrow state database; no schema or directory name
changed.

## Build and validation

`scripts/build-windows.ps1` continues to build with `CGO_ENABLED=0` and the
Windows GUI subsystem. Cross-compilation proves the Windows sources, embedded
loader and resources link correctly, but it does not validate Windows shell,
WebView2 Runtime, notification-area or focus behavior.

Before release, run the Windows-native desktop tests on both amd64 and arm64 with
the Evergreen Runtime installed. Validate first launch, missing-runtime browser
fallback, X-to-hide, notification-area Open/Exit, duplicate-launch activation,
React Exit, taskbar/native icon appearance, session/CSRF behavior, and graceful
child cleanup. Also test Windows 10 and Windows 11 under standard-user accounts.
