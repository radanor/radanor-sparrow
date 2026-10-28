# Third-Party Notices

Radanor Sparrow Community Edition incorporates third-party software. This file
records, from the actual dependency metadata on disk, **which** third-party
components are distributed, **under what license**, and **what obligations** those
licenses impose on anyone redistributing the product.

The component lists and licenses below were read from the modules and packages
resolved on this machine (`go list`, the Go module cache, and
`web/node_modules`), not from memory. Versions are the resolved versions at the
time of writing; see [`go.mod`](go.mod) and [`web/package-lock.json`](web/package-lock.json)
for the authoritative pins.

Two things this file is careful **not** to do: it does not relabel any third-party
component under Radanor's license, and it does not claim obligations that a
component's license does not actually impose. Each third-party component keeps its
own license.

## 1. What is actually distributed

Radanor Sparrow ships as a single executable that embeds its dashboard. Two
categories of third-party code reach a user:

- **Go modules linked into the binary.** These are compiled into
  `radanor-sparrow` / `RadanorSparrow.exe`.
- **JavaScript bundled into the dashboard.** The embedded dashboard is built from
  the frontend and bundles its runtime dependencies.

On Linux, a build produced by `scripts/build-linux.sh` can additionally contain a
second, optional executable:

- **The native window component** (`radanor-sparrow-window`). It presents the same
  authenticated dashboard in a GTK3 + WebKitGTK 4.1 window and contains no
  third-party code: it is Radanor code that links, at run time, against libraries
  the operating system already installs. Those libraries are not copied into the
  distribution, and they are recorded separately in section 2 because their
  licenses are not permissive in the same way as everything else in this file.

Everything else — build tools, test-only dependencies, and packages present only in
the module graph — is **not** distributed and imposes no distribution obligation.

## 2. Go modules linked into the binary

Determined by resolving the dependency closure of `./cmd/...` for both Linux and
Windows. All are permissive; none is copyleft.

| Module | Version | License |
| --- | --- | --- |
| `github.com/Microsoft/go-winio` | v0.6.2 | MIT |
| `github.com/dustin/go-humanize` | v1.0.1 | MIT |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause |
| `github.com/jchv/go-webview2` | v0.0.0-20260205173254-56598839c808 | MIT (includes Microsoft's permissively licensed WebView2Loader.dll) |
| `github.com/jchv/go-winloader` | v0.0.0-20250406163304-c1995be93bd1 | ISC |
| `github.com/mattn/go-isatty` | v0.0.24 | MIT |
| `github.com/ncruces/go-strftime` | v1.0.0 | MIT |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause |
| `golang.org/x/sys` | v0.48.0 | BSD-3-Clause |
| `modernc.org/libc` | v1.77.1 | BSD-3-Clause (bundles additional notices, see below) |
| `modernc.org/mathutil` | v1.7.1 | BSD-3-Clause |
| `modernc.org/memory` | v1.12.1 | BSD-3-Clause |
| `modernc.org/sqlite` | v1.60.1 | BSD-3-Clause (bundles SQLite, see below) |

### System libraries the Linux window component links at run time

The optional Linux window component is the only part of the product that links a
copyleft library. It links these dynamically, resolved from the running system at
start-up: nothing is copied into the distribution, and the component declines to
start — leaving the supervisor on the authenticated browser path — when they are
absent.

The GTK 3 and WebKitGTK 4.1 libraries, **and this component's use of them, are covered
by the GNU Lesser General Public License**. The complete text of that licence is
reproduced in Appendix F of this file, which ships beside every Linux artifact and
inside the AppImage, so the reference above leads to the licence itself rather than to
a description of it.

Read from `readelf -d` on the built component and from the installed packages'
copyright files on the validated Ubuntu 22.04.5 host:

| Direct dependency (`DT_NEEDED`) | System package | Version | License recorded by the distribution |
| --- | --- | --- | --- |
| `libwebkit2gtk-4.1.so.0` | `libwebkit2gtk-4.1-0` | 2.50.4 | LGPL-2+ (or AFL-2.0), with BSD-2-Clause and BSD-2-Clause-Patent files |
| `libgtk-3.so.0` | `libgtk-3-0` | 3.24.33 | LGPL-2.1+ (with Expat) |
| `libgdk_pixbuf-2.0.so.0` | `libgdk-pixbuf-2.0-0` | 2.42.8 | LGPL-2.1+ (with CC0-1.0) |
| `libgobject-2.0.so.0`, `libglib-2.0.so.0` | `libglib2.0-0` | 2.72.4 | LGPL-2+ (the distribution's copyright file references `/usr/share/common-licenses/LGPL`); some files Expat |
| `libc.so.6` | `libc6` | 2.35 | LGPL-2.1 (glibc) |

Build-time only, and therefore not distributed: `gcc`, `pkg-config`,
`libgtk-3-dev`, `libwebkit2gtk-4.1-dev`.

What follows are facts recorded for review, not licensing conclusions:

- **No copyleft code is copied into the distribution.** The component links the
  system's own copies of these libraries, which the user's operating system
  installs and maintains, and it neither modifies nor redistributes them.
- **The component is a separate executable partly for this reason.** Keeping it
  apart from the product keeps the product's own executable and dashboard free of
  any copyleft linkage, and lets a machine without these libraries run the product
  with the authenticated browser instead.
- **Packaging these libraries would change that.** Any package that bundles GTK or
  WebKitGTK distributes the library itself, which brings the LGPL's conditions with
  it — the license text, the corresponding source, and a way for a recipient to
  replace the library. The build and install scripts do not bundle them and do not
  satisfy those conditions for a bundled copy, so packaging must either keep using
  the system libraries or meet those conditions before publication. This is flagged
  in section 6.
- **The Linux AppImage keeps using the system libraries.** L4 packages the two
  executables, the launcher entry, the approved icon and these documents into
  `RadanorSparrow-<version>-x86_64.AppImage`; it contains no GTK, WebKitGTK,
  JavaScriptCore, libsoup, bubblewrap or glibc file (asserted by
  `internal/appimage`), and AppRun adds nothing to `LD_LIBRARY_PATH`. The recipient's
  own distribution therefore continues to install, update and patch the libraries,
  and no LGPL-obligated copy is distributed. A bundled variant would be a different
  artifact and would have to satisfy those conditions first.

### Components bundled inside `modernc.org/sqlite` and `modernc.org/libc`

These modules carry their own third-party content with it:

| Component | Origin | License |
| --- | --- | --- |
| SQLite (amalgamation, translated to Go) | sqlite.org | Public domain |
| sqlite-vec (`vec/`, only if imported) | asg017/sqlite-vec | MIT |
| Go runtime code | golang.org/go | BSD-3-Clause |
| musl libc | musl.libc.org | MIT |
| go-netdb | dominikh/go-netdb | MIT |
| NixOS/nixpkgs | NixOS/nixpkgs | MIT |

The `modernc.org/libc` generated Go code also contains interface and constant
definitions derived from Linux system headers, and those files carry the upstream
headers' own notice comments. Two distinct upstream sources are involved, and they
are not the same licence: files derived from **glibc** headers carry the GNU Lesser
General Public License 2.1-or-later notice, while files derived from **GCC** headers
carry GPL-3.0-or-later *together with the GCC Runtime Library Exception*, which
exists precisely so that compiling and distributing the exception's output carries
no GPL obligation. Which packages are compiled into each shipped binary, and how
much of that text is comment rather than code, is measured in section 6. The
licensing conclusion still belongs to counsel and is not drawn here.

## 3. JavaScript bundled into the dashboard

The dashboard bundle is produced from the frontend and includes its runtime
dependencies. These are distributed inside the executable as embedded assets.

| Package | Version | License |
| --- | --- | --- |
| `react` | 19.3.0 | MIT |
| `react-dom` | 19.3.0 | MIT |
| `scheduler` | 0.28.0 | MIT |

## 4. Distributed-but-not-required packages

| Package | Version | License | Why no obligation |
| --- | --- | --- | --- |
| `vite` | 8.3.4 | MIT | Used to build the bundle; its own code is not shipped (its generated runtime helper is MIT and covered by section 3's MIT notice). |
| `typescript` | 7.0.2 | Apache-2.0 | Compile-time only. Its output is not a copy of TypeScript, so Apache-2.0 redistribution obligations are not triggered. |

## 5. Not distributed

Present in the module graph or `node_modules` but compiled into nothing that ships:

- **Test/tooling-only Go modules:** `github.com/google/pprof` (Apache-2.0),
  `modernc.org/fileutil`, `modernc.org/cc/v4`, `modernc.org/ccgo/v4`,
  `modernc.org/gc/v2`, `modernc.org/gc/v3`, `modernc.org/goabi0`,
  `modernc.org/opt`, `modernc.org/sortutil`, `modernc.org/strutil`,
  `modernc.org/token`, `golang.org/x/mod`, `golang.org/x/sync`,
  `golang.org/x/tools` — all BSD-3-Clause except where noted.
- **`github.com/hashicorp/golang-lru/v2` v2.0.7 (MPL-2.0):** reachable through the
  module graph via `modernc.org/libc`'s tooling, but **not linked into the product**.
  MPL-2.0 is file-level copyleft and is compatible with GPLv3; because no MPL code
  is distributed here it imposes no obligation on this product. It is noted only so
  that a tool reading `go.sum` or an SBOM does not mistake it for distributed code.
- **Build-only frontend packages:** `vitest`, `@vitejs/plugin-react`,
  `@types/react`, `@types/react-dom`, and all other `node_modules` entries.

## 6. Concerns identified by the audit

- **No copyleft dependency is linked into the binary or bundled dashboard.** Every
  distributed component is MIT, BSD-3-Clause, Apache-2.0 or public domain, all of
  which are compatible with the project's proposed GPLv3 Change License.
- **LGPL system libraries under the Linux window component — closed by a recorded
  owner decision on 2026-10-28, not by legal certification.** The optional
  `radanor-sparrow-window` is the only component that links GTK3 and WebKitGTK 4.1, and it
  links them from the running system. Measured on the shipped artifacts (2026-10-27):

  ```
  $ readelf -d dist/linux/radanor-sparrow-window | grep -E 'NEEDED|RPATH|RUNPATH'
    (NEEDED)  Shared library: [libwebkit2gtk-4.1.so.0]
    (NEEDED)  Shared library: [libgtk-3.so.0]
    (NEEDED)  Shared library: [libgdk_pixbuf-2.0.so.0]
    (NEEDED)  Shared library: [libgobject-2.0.so.0]
    (NEEDED)  Shared library: [libglib-2.0.so.0]
    (NEEDED)  Shared library: [libc.so.6]
    (no RPATH, no RUNPATH)
  $ ldd dist/linux/radanor-sparrow-window | head -3
    libwebkit2gtk-4.1.so.0 => /lib/x86_64-linux-gnu/libwebkit2gtk-4.1.so.0
  $ find dist/linux/RadanorSparrow.AppDir -name '*.so*' | wc -l
    0
  $ readelf -d dist/linux/radanor-sparrow | grep -c NEEDED
    0
  ```

  So: the supervisor is statically linked and needs no library at all; the window
  component resolves six libraries through the loader's default search path; and the
  AppImage carries **no** shared library — neither the LGPL system libraries nor a
  private loader, which is also what keeps WebKit's sandbox and helper processes on
  the host's own entitlements. Nothing LGPL-covered is copied, and no LGPL-covered
  library is modified. The notice element is not outstanding as a document: **Appendix F
  reproduces the complete LGPL-2.1 text**, and §2 above states that the libraries and this
  product's use of them are covered by it, so the artifact carries the terms whichever way
  the question is answered.

  What the question is, and what was decided: whether conveying a program that links the
  host's own LGPL libraries engages LGPL-2.1 §6 at all, and — if it does — whether the
  mapping recorded in the rights holder's decision record §2.2 discharges
  every obligation. The rights holder recorded on **2026-10-28** that the interpretation
  remains open and accepted that residual uncertainty as a **risk decision**, not as legal
  advice (D1 in the record named in the previous sentence). No lawyer has certified it and
  none is claimed. A future variant that *did* bundle those libraries would be a different
  artifact and would have to satisfy source-offer, relinking and sandbox-entitlement
  conditions before publication.
- **Missing attribution before this file existed.** No third-party notice file,
  `NOTICE`, or license text accompanied the product. This file establishes the
  required MIT and BSD-3-Clause attribution and the license texts (appendices A and
  B). Distribution must now include it — see section 7.
- **`modernc.org/libc` header-derived constants — closed by a recorded owner decision on
  2026-10-28, not by legal certification.** Measured status (2026-10-27) rather than an
  assumption:

  ```
  $ go mod why -m github.com/sirupsen/logrus
    (main module does not need module github.com/sirupsen/logrus)
  $ GOOS=windows go list -deps ./cmd/radanor-sparrow | grep 'modernc.org/libc/'
    errno  fcntl  limits  pthread  signal  stdio  sys/stat  sys/types  time  unistd
  $ go list -deps ./cmd/radanor-sparrow | grep 'modernc.org/libc/'
    sys/types  uuid/uuid
  $ GOOS=windows go list -deps ./cmd/radanor-sparrow | grep -c netinet
    0
  ```

  The `netinet/*` files that the earlier audit named are **not** in any shipped
  binary's build graph, on either platform. Resolved per binary, the packages that
  actually reach a distributed executable are exactly:

  ```
  $ GOOS=linux   go list -deps ./cmd/radanor-sparrow | grep '^modernc.org/libc'
    modernc.org/libc/sys/types   modernc.org/libc/uuid/uuid
  $ GOOS=windows go list -deps ./cmd/radanor-sparrow | grep '^modernc.org/libc'
    errno fcntl limits pthread signal stdio sys/stat sys/types time unistd
  ```

  The window component (`cmd/radanor-sparrow-window`) links no `modernc.org/libc`
  package at all. Within those packages, the upstream header text is **entirely
  comment**: scanning every `.go` file of every distributed package for the notice
  strings finds 2 000+ matching lines and **zero** outside a `//` or `/* */`
  comment. The compiled declarations beneath them are constants and type aliases —
  for example `signal/signal_linux_amd64.go` carries `SEGV_MAPERR = 1` and the
  `CLD_*` values under a glibc `siginfo-consts.h` comment.

  Two upstream sources, with different licences, are involved, and the earlier
  audit's single "GPL/LGPL" label concealed the distinction:

  | Derived from | Notice in the distributed files | Packages |
  | --- | --- | --- |
  | glibc headers | LGPL-2.1-or-later | `sys/types`, `fcntl`, `sys/stat`, `time`, `unistd`, `pthread`, `signal`, `stdio`, `uuid/uuid` |
  | GCC headers | GPL-3.0-or-later **with the GCC Runtime Library Exception 3.1** | `limits`, `pthread`, `signal`, `stdio`, `uuid/uuid` |

  Measured file by file: 525 files across those packages, 52 naming GCC, and **0**
  of them naming GCC without also naming the Runtime Library Exception. The GPL
  text therefore never appears there without the exception that licenses the
  output. The
  exception is upstream's own grant and needs no action from this project; it is
  recorded because a reader who sees "GPL" in a distributed file deserves to see
  the exception beside it.

  `modernc.org/libc` is itself BSD-3-Clause, and its own `LICENSE-3RD-PARTY.md`
  lists **neither glibc nor any GNU licence** — the attribution for this header
  material is absent upstream, not merely absent here. That is the practical gap: a
  recipient of this product cannot learn from `modernc.org/libc` that the comments
  are glibc's. Sections 2 and 7 now carry that provenance, which is the notice this
  project can truthfully give.

  What remains open is whether copying comment blocks, constants and type definitions out
  of glibc and GCC headers creates an obligation at all. `modernc.org/libc` v1.77.1 is
  pinned; of the files that actually compile, 29 on linux/amd64 and 25 on windows/amd64 are
  the module's own BSD-3-Clause code, and the header-derived files that compile are two on
  Linux and three on Windows — numeric constants, type aliases, struct layouts and
  function-pointer declarations. Four files reproduce GPL header text and all four also
  reproduce the Runtime Library Exception; none reproduces it without. LGPL-2.1 §5's final
  paragraph declares object files using "numerical parameters, data structure layouts and
  accessors, and small macros and small inline functions" unrestricted, which is the
  in-licence rule that governs this material rather than an argument about it.

  The rights holder recorded on **2026-10-28** an explicit **risk decision**: the
  interpretation stays open, and the residual uncertainty is accepted on the basis that any
  remedy a stricter view would require is an added written offer or notice paragraph, not
  publication of Sparrow's own source. That is not legal clearance and is not presented as
  it. Known concrete obligations are unaffected by it — if one is identified, it is
  corrected rather than absorbed by the decision.
- **Font not bundled.** The dashboard names `Inter` first in its font stack but does
  not embed or download it; it falls back to system fonts. No font license
  obligation arises, and no font file is distributed.
- **No bundled images, icons or third-party artwork were found** outside the
  dependency sets above.

## 7. Redistribution obligations

Anyone redistributing Radanor Sparrow Community Edition — including the built
executable — must:

1. **Include this file and the full license texts in appendices A, B, F and G** in
   the documentation and/or materials accompanying the distribution, satisfying the
   MIT and BSD-3-Clause requirements to reproduce copyright notices and license
   texts, and carrying the LGPL-2.1 text and the GCC Runtime Library Exception beside
   the components they describe. `scripts/build-linux.sh`, `scripts/build-windows.ps1`,
   `scripts/build-appimage.sh` and `scripts/install-linux.sh` all copy this file
   whole, so every one of the appendices travels with the artifact.
2. **Include the project [`LICENSE`](LICENSE)** (the BSL 1.1 requires it to be
   conspicuously displayed on copies of the Licensed Work).
3. **Not relicense, re-copyright, or imply endorsement by the third parties named
   here.** No contributor's name or the name of any project above may be used to
   promote a derivative product without permission.

What the scripts actually do today, and what is still owed:

* `scripts/build-linux.sh` writes `LICENSE` and `THIRD_PARTY_NOTICES.md` beside the
  Linux executable in `dist/linux`, `scripts/build-windows.ps1` does the same in
  `dist/windows`, and `scripts/install-linux.sh` copies both into
  `~/.local/share/doc/radanor-sparrow/`. The executable embeds the project's
  license notice and the dashboard states it, so a copy of the product carries the
  license it is distributed under.
* **Decided, not settled:** the LGPL question in section 6 and the `modernc.org/libc`
  header question beside it were closed on **2026-10-28** by the rights holder's recorded
  **risk decision**, not by legal advice. The BSL parameters were settled on 2026-10-27.
  What this file owes a recipient is completeness and honesty, and it now has both: the
  licence texts are reproduced in full, the system libraries are named with their
  licences, and the paragraphs above state that the legal reading is unresolved and who
  accepted it. A recipient who wants the reasoning can follow
  `the rights holder's decision record` §2 and
  `docs/licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md`.

## Appendix A — MIT License text

Applies to: `github.com/Microsoft/go-winio`, `github.com/dustin/go-humanize`,
`github.com/mattn/go-isatty`, `github.com/ncruces/go-strftime`, `sqlite-vec`,
`musl libc`, `go-netdb`, `NixOS/nixpkgs`, `react`, `react-dom`, `scheduler`,
`vite`, `github.com/jchv/go-webview2`.

```text
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

Copyright notices carried with the MIT components above:

- Copyright (c) 2015 Microsoft — `github.com/Microsoft/go-winio`
- Copyright (c) 2005-2008 Dustin Sallings <dustin@spy.net> — `github.com/dustin/go-humanize`
- Copyright (c) Yasuhiro MATSUMOTO <mattn.jp@gmail.com> — `github.com/mattn/go-isatty`
- Copyright (c) 2022 Nuno Cruces — `github.com/ncruces/go-strftime`
- Copyright (c) 2024 Alex Garcia — `sqlite-vec`
- Copyright © 2005-2020 Rich Felker, et al. — `musl libc`
- Copyright (c) 2012 Dominik Honnef — `go-netdb`
- Copyright (c) 2003-2025 Eelco Dolstra and the Nixpkgs/NixOS contributors — `NixOS/nixpkgs`
- Copyright (c) Facebook, Inc. and its affiliates — `react`, `react-dom`, `scheduler`
- Copyright (c) 2019-present, Yuxi (Evan) You and Vite contributors — `vite`
- Copyright (c) 2020 John Chadwick; portions Copyright (c) 2017 Serge Zaitsev — `github.com/jchv/go-webview2`

## Appendix B — BSD 3-Clause License text

Applies to: `github.com/google/uuid`, `github.com/remyoudompheng/bigfft`,
`golang.org/x/sys`, `modernc.org/libc`, `modernc.org/mathutil`,
`modernc.org/memory`, `modernc.org/sqlite`, and the Go-derived code bundled
within them.

```text
Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

   * Redistributions of source code must retain the above copyright notice,
     this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above copyright notice,
     this list of conditions and the following disclaimer in the documentation
     and/or other materials provided with the distribution.
   * Neither the name of the copyright holder nor the names of its contributors
     may be used to endorse or promote products derived from this software
     without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

Copyright notices carried with the BSD-3-Clause components above:

- Copyright (c) 2009,2014 Google Inc. — `github.com/google/uuid`
- Copyright (c) 2012 The Go Authors — `github.com/remyoudompheng/bigfft`
- Copyright 2009 The Go Authors — `golang.org/x/sys`
- Copyright (c) 2017 The Libc Authors; Copyright (c) 2017 The Sqlite Authors;
  Copyright (c) 2014 The mathutil Authors; Copyright (c) 2017 The Memory Authors —
  `modernc.org/*`
- Copyright (c) 2009 The Go Authors — Go-derived code within `modernc.org/*`

## Appendix C — ISC License text

Applies to: `github.com/jchv/go-winloader`.

```text
Permission to use, copy, modify, and/or distribute this software for any purpose
with or without fee is hereby granted, provided that the above copyright notice
and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES WITH
REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF MERCHANTABILITY AND
FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR ANY SPECIAL, DIRECT,
INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES WHATSOEVER RESULTING FROM LOSS
OF USE, DATA OR PROFITS, WHETHER IN AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER
TORTIOUS ACTION, ARISING OUT OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF
THIS SOFTWARE.
```

Copyright © 2021, John Chadwick <john@jchw.io>.

## Appendix D — SQLite (public domain)

Applies to the SQLite amalgamation bundled inside `modernc.org/sqlite`.

```text
SQLite Is Public Domain

All of the code and documentation in SQLite has been dedicated to the public
domain by the authors. All code authors, and representatives of the companies
they work for, have signed affidavits dedicating their contributions to the
public domain and originals of those signed affidavits are stored in a firesafe
at the main offices of Hwaci. Anyone is free to copy, modify, publish, use,
compile, sell, or distribute the original SQLite code, either in source code form
or as a compiled binary, for any purpose, commercial or non-commercial, and by
any means.
```

## Appendix E — NSIS installer stub (zlib/libpng)

Applies to the stub embedded in `RadanorSparrow-<version>-setup.exe`. The product
is built with NSIS's zlib compressor, which is covered by the zlib/libpng license
below; the LZMA compression module (Common Public License 1.0) is not used.

```text
Copyright (C) 1999-2026 Contributors

This software is provided 'as-is', without any express or implied warranty. In no
event will the authors be held liable for any damages arising from the use of this
software.

Permission is granted to anyone to use this software for any purpose, including
commercial applications, and to alter it and redistribute it freely, subject to
the following restrictions:

1. The origin of this software must not be misrepresented; you must not claim
   that you wrote the original software. If you use this software in a product,
   an acknowledgment in the product documentation would be appreciated but is
   not required.
2. Altered source versions must be plainly marked as such, and must not be
   misrepresented as being the original software.
3. This notice may not be removed or altered from any source distribution.
```

## Appendix F — GNU Lesser General Public License v2.1

Applies to: the system libraries the Linux window component links at run time
(`libgtk-3.so.0`, `libwebkit2gtk-4.1.so.0`, `libgdk_pixbuf-2.0.so.0`,
`libgobject-2.0.so.0`, `libglib-2.0.so.0`) and to the glibc-derived header material
described in section 6.

The licence text is reproduced in full here **whichever way the open question in
section 6 is answered**, so that the notice element of the LGPL is discharged under
either reading. Reproducing it is not an assertion that section 6 is engaged, and it
does not settle that question. Nothing in this appendix states whether conveying a
program that links the recipient's own system libraries attracts LGPL-2.1 §6, or
whether the header-derived comments, constants and type aliases in the shipped
`modernc.org/libc` packages amount to a copy of copyrightable expression. Those
remain the decisions recorded in section 6 and in
[`docs/licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md`](docs/licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md).

What section 2 measured holds regardless of the answer: no LGPL-covered library is
copied into any artifact this project distributes, none is modified, and no `RPATH`,
`RUNPATH` or private copy prevents a recipient from replacing it with the version
their own operating system ships.

```text
                  GNU LESSER GENERAL PUBLIC LICENSE
                       Version 2.1, February 1999

 Copyright (C) 1991, 1999 Free Software Foundation, Inc.
 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301  USA
 Everyone is permitted to copy and distribute verbatim copies
 of this license document, but changing it is not allowed.

[This is the first released version of the Lesser GPL.  It also counts
 as the successor of the GNU Library Public License, version 2, hence
 the version number 2.1.]

                            Preamble

  The licenses for most software are designed to take away your
freedom to share and change it.  By contrast, the GNU General Public
Licenses are intended to guarantee your freedom to share and change
free software--to make sure the software is free for all its users.

  This license, the Lesser General Public License, applies to some
specially designated software packages--typically libraries--of the
Free Software Foundation and other authors who decide to use it.  You
can use it too, but we suggest you first think carefully about whether
this license or the ordinary General Public License is the better
strategy to use in any particular case, based on the explanations below.

  When we speak of free software, we are referring to freedom of use,
not price.  Our General Public Licenses are designed to make sure that
you have the freedom to distribute copies of free software (and charge
for this service if you wish); that you receive source code or can get
it if you want it; that you can change the software and use pieces of
it in new free programs; and that you are informed that you can do
these things.

  To protect your rights, we need to make restrictions that forbid
distributors to deny you these rights or to ask you to surrender these
rights.  These restrictions translate to certain responsibilities for
you if you distribute copies of the library or if you modify it.

  For example, if you distribute copies of the library, whether gratis
or for a fee, you must give the recipients all the rights that we gave
you.  You must make sure that they, too, receive or can get the source
code.  If you link other code with the library, you must provide
complete object files to the recipients, so that they can relink them
with the library after making changes to the library and recompiling
it.  And you must show them these terms so they know their rights.

  We protect your rights with a two-step method: (1) we copyright the
library, and (2) we offer you this license, which gives you legal
permission to copy, distribute and/or modify the library.

  To protect each distributor, we want to make it very clear that
there is no warranty for the free library.  Also, if the library is
modified by someone else and passed on, the recipients should know
that what they have is not the original version, so that the original
author's reputation will not be affected by problems that might be
introduced by others.

  Finally, software patents pose a constant threat to the existence of
any free program.  We wish to make sure that a company cannot
effectively restrict the users of a free program by obtaining a
restrictive license from a patent holder.  Therefore, we insist that
any patent license obtained for a version of the library must be
consistent with the full freedom of use specified in this license.

  Most GNU software, including some libraries, is covered by the
ordinary GNU General Public License.  This license, the GNU Lesser
General Public License, applies to certain designated libraries, and
is quite different from the ordinary General Public License.  We use
this license for certain libraries in order to permit linking those
libraries into non-free programs.

  When a program is linked with a library, whether statically or using
a shared library, the combination of the two is legally speaking a
combined work, a derivative of the original library.  The ordinary
General Public License therefore permits such linking only if the
entire combination fits its criteria of freedom.  The Lesser General
Public License permits more lax criteria for linking other code with
the library.

  We call this license the "Lesser" General Public License because it
does Less to protect the user's freedom than the ordinary General
Public License.  It also provides other free software developers Less
of an advantage over competing non-free programs.  These disadvantages
are the reason we use the ordinary General Public License for many
libraries.  However, the Lesser license provides advantages in certain
special circumstances.

  For example, on rare occasions, there may be a special need to
encourage the widest possible use of a certain library, so that it becomes
a de-facto standard.  To achieve this, non-free programs must be
allowed to use the library.  A more frequent case is that a free
library does the same job as widely used non-free libraries.  In this
case, there is little to gain by limiting the free library to free
software only, so we use the Lesser General Public License.

  In other cases, permission to use a particular library in non-free
programs enables a greater number of people to use a large body of
free software.  For example, permission to use the GNU C Library in
non-free programs enables many more people to use the whole GNU
operating system, as well as its variant, the GNU/Linux operating
system.

  Although the Lesser General Public License is Less protective of the
users' freedom, it does ensure that the user of a program that is
linked with the Library has the freedom and the wherewithal to run
that program using a modified version of the Library.

  The precise terms and conditions for copying, distribution and
modification follow.  Pay close attention to the difference between a
"work based on the library" and a "work that uses the library".  The
former contains code derived from the library, whereas the latter must
be combined with the library in order to run.

                  GNU LESSER GENERAL PUBLIC LICENSE
   TERMS AND CONDITIONS FOR COPYING, DISTRIBUTION AND MODIFICATION

  0. This License Agreement applies to any software library or other
program which contains a notice placed by the copyright holder or
other authorized party saying it may be distributed under the terms of
this Lesser General Public License (also called "this License").
Each licensee is addressed as "you".

  A "library" means a collection of software functions and/or data
prepared so as to be conveniently linked with application programs
(which use some of those functions and data) to form executables.

  The "Library", below, refers to any such software library or work
which has been distributed under these terms.  A "work based on the
Library" means either the Library or any derivative work under
copyright law: that is to say, a work containing the Library or a
portion of it, either verbatim or with modifications and/or translated
straightforwardly into another language.  (Hereinafter, translation is
included without limitation in the term "modification".)

  "Source code" for a work means the preferred form of the work for
making modifications to it.  For a library, complete source code means
all the source code for all modules it contains, plus any associated
interface definition files, plus the scripts used to control compilation
and installation of the library.

  Activities other than copying, distribution and modification are not
covered by this License; they are outside its scope.  The act of
running a program using the Library is not restricted, and output from
such a program is covered only if its contents constitute a work based
on the Library (independent of the use of the Library in a tool for
writing it).  Whether that is true depends on what the Library does
and what the program that uses the Library does.

  1. You may copy and distribute verbatim copies of the Library's
complete source code as you receive it, in any medium, provided that
you conspicuously and appropriately publish on each copy an
appropriate copyright notice and disclaimer of warranty; keep intact
all the notices that refer to this License and to the absence of any
warranty; and distribute a copy of this License along with the
Library.

  You may charge a fee for the physical act of transferring a copy,
and you may at your option offer warranty protection in exchange for a
fee.

  2. You may modify your copy or copies of the Library or any portion
of it, thus forming a work based on the Library, and copy and
distribute such modifications or work under the terms of Section 1
above, provided that you also meet all of these conditions:

    a) The modified work must itself be a software library.

    b) You must cause the files modified to carry prominent notices
    stating that you changed the files and the date of any change.

    c) You must cause the whole of the work to be licensed at no
    charge to all third parties under the terms of this License.

    d) If a facility in the modified Library refers to a function or a
    table of data to be supplied by an application program that uses
    the facility, other than as an argument passed when the facility
    is invoked, then you must make a good faith effort to ensure that,
    in the event an application does not supply such function or
    table, the facility still operates, and performs whatever part of
    its purpose remains meaningful.

    (For example, a function in a library to compute square roots has
    a purpose that is entirely well-defined independent of the
    application.  Therefore, Subsection 2d requires that any
    application-supplied function or table used by this function must
    be optional: if the application does not supply it, the square
    root function must still compute square roots.)

These requirements apply to the modified work as a whole.  If
identifiable sections of that work are not derived from the Library,
and can be reasonably considered independent and separate works in
themselves, then this License, and its terms, do not apply to those
sections when you distribute them as separate works.  But when you
distribute the same sections as part of a whole which is a work based
on the Library, the distribution of the whole must be on the terms of
this License, whose permissions for other licensees extend to the
entire whole, and thus to each and every part regardless of who wrote
it.

Thus, it is not the intent of this section to claim rights or contest
your rights to work written entirely by you; rather, the intent is to
exercise the right to control the distribution of derivative or
collective works based on the Library.

In addition, mere aggregation of another work not based on the Library
with the Library (or with a work based on the Library) on a volume of
a storage or distribution medium does not bring the other work under
the scope of this License.

  3. You may opt to apply the terms of the ordinary GNU General Public
License instead of this License to a given copy of the Library.  To do
this, you must alter all the notices that refer to this License, so
that they refer to the ordinary GNU General Public License, version 2,
instead of to this License.  (If a newer version than version 2 of the
ordinary GNU General Public License has appeared, then you can specify
that version instead if you wish.)  Do not make any other change in
these notices.

  Once this change is made in a given copy, it is irreversible for
that copy, so the ordinary GNU General Public License applies to all
subsequent copies and derivative works made from that copy.

  This option is useful when you wish to copy part of the code of
the Library into a program that is not a library.

  4. You may copy and distribute the Library (or a portion or
derivative of it, under Section 2) in object code or executable form
under the terms of Sections 1 and 2 above provided that you accompany
it with the complete corresponding machine-readable source code, which
must be distributed under the terms of Sections 1 and 2 above on a
medium customarily used for software interchange.

  If distribution of object code is made by offering access to copy
from a designated place, then offering equivalent access to copy the
source code from the same place satisfies the requirement to
distribute the source code, even though third parties are not
compelled to copy the source along with the object code.

  5. A program that contains no derivative of any portion of the
Library, but is designed to work with the Library by being compiled or
linked with it, is called a "work that uses the Library".  Such a
work, in isolation, is not a derivative work of the Library, and
therefore falls outside the scope of this License.

  However, linking a "work that uses the Library" with the Library
creates an executable that is a derivative of the Library (because it
contains portions of the Library), rather than a "work that uses the
library".  The executable is therefore covered by this License.
Section 6 states terms for distribution of such executables.

  When a "work that uses the Library" uses material from a header file
that is part of the Library, the object code for the work may be a
derivative work of the Library even though the source code is not.
Whether this is true is especially significant if the work can be
linked without the Library, or if the work is itself a library.  The
threshold for this to be true is not precisely defined by law.

  If such an object file uses only numerical parameters, data
structure layouts and accessors, and small macros and small inline
functions (ten lines or less in length), then the use of the object
file is unrestricted, regardless of whether it is legally a derivative
work.  (Executables containing this object code plus portions of the
Library will still fall under Section 6.)

  Otherwise, if the work is a derivative of the Library, you may
distribute the object code for the work under the terms of Section 6.
Any executables containing that work also fall under Section 6,
whether or not they are linked directly with the Library itself.

  6. As an exception to the Sections above, you may also combine or
link a "work that uses the Library" with the Library to produce a
work containing portions of the Library, and distribute that work
under terms of your choice, provided that the terms permit
modification of the work for the customer's own use and reverse
engineering for debugging such modifications.

  You must give prominent notice with each copy of the work that the
Library is used in it and that the Library and its use are covered by
this License.  You must supply a copy of this License.  If the work
during execution displays copyright notices, you must include the
copyright notice for the Library among them, as well as a reference
directing the user to the copy of this License.  Also, you must do one
of these things:

    a) Accompany the work with the complete corresponding
    machine-readable source code for the Library including whatever
    changes were used in the work (which must be distributed under
    Sections 1 and 2 above); and, if the work is an executable linked
    with the Library, with the complete machine-readable "work that
    uses the Library", as object code and/or source code, so that the
    user can modify the Library and then relink to produce a modified
    executable containing the modified Library.  (It is understood
    that the user who changes the contents of definitions files in the
    Library will not necessarily be able to recompile the application
    to use the modified definitions.)

    b) Use a suitable shared library mechanism for linking with the
    Library.  A suitable mechanism is one that (1) uses at run time a
    copy of the library already present on the user's computer system,
    rather than copying library functions into the executable, and (2)
    will operate properly with a modified version of the library, if
    the user installs one, as long as the modified version is
    interface-compatible with the version that the work was made with.

    c) Accompany the work with a written offer, valid for at
    least three years, to give the same user the materials
    specified in Subsection 6a, above, for a charge no more
    than the cost of performing this distribution.

    d) If distribution of the work is made by offering access to copy
    from a designated place, offer equivalent access to copy the above
    specified materials from the same place.

    e) Verify that the user has already received a copy of these
    materials or that you have already sent this user a copy.

  For an executable, the required form of the "work that uses the
Library" must include any data and utility programs needed for
reproducing the executable from it.  However, as a special exception,
the materials to be distributed need not include anything that is
normally distributed (in either source or binary form) with the major
components (compiler, kernel, and so on) of the operating system on
which the executable runs, unless that component itself accompanies
the executable.

  It may happen that this requirement contradicts the license
restrictions of other proprietary libraries that do not normally
accompany the operating system.  Such a contradiction means you cannot
use both them and the Library together in an executable that you
distribute.

  7. You may place library facilities that are a work based on the
Library side-by-side in a single library together with other library
facilities not covered by this License, and distribute such a combined
library, provided that the separate distribution of the work based on
the Library and of the other library facilities is otherwise
permitted, and provided that you do these two things:

    a) Accompany the combined library with a copy of the same work
    based on the Library, uncombined with any other library
    facilities.  This must be distributed under the terms of the
    Sections above.

    b) Give prominent notice with the combined library of the fact
    that part of it is a work based on the Library, and explaining
    where to find the accompanying uncombined form of the same work.

  8. You may not copy, modify, sublicense, link with, or distribute
the Library except as expressly provided under this License.  Any
attempt otherwise to copy, modify, sublicense, link with, or
distribute the Library is void, and will automatically terminate your
rights under this License.  However, parties who have received copies,
or rights, from you under this License will not have their licenses
terminated so long as such parties remain in full compliance.

  9. You are not required to accept this License, since you have not
signed it.  However, nothing else grants you permission to modify or
distribute the Library or its derivative works.  These actions are
prohibited by law if you do not accept this License.  Therefore, by
modifying or distributing the Library (or any work based on the
Library), you indicate your acceptance of this License to do so, and
all its terms and conditions for copying, distributing or modifying
the Library or works based on it.

  10. Each time you redistribute the Library (or any work based on the
Library), the recipient automatically receives a license from the
original licensor to copy, distribute, link with or modify the Library
subject to these terms and conditions.  You may not impose any further
restrictions on the recipients' exercise of the rights granted herein.
You are not responsible for enforcing compliance by third parties with
this License.

  11. If, as a consequence of a court judgment or allegation of patent
infringement or for any other reason (not limited to patent issues),
conditions are imposed on you (whether by court order, agreement or
otherwise) that contradict the conditions of this License, they do not
excuse you from the conditions of this License.  If you cannot
distribute so as to satisfy simultaneously your obligations under this
License and any other pertinent obligations, then as a consequence you
may not distribute the Library at all.  For example, if a patent
license would not permit royalty-free redistribution of the Library by
all those who receive copies directly or indirectly through you, then
the only way you could satisfy both it and this License would be to
refrain entirely from distribution of the Library.

If any portion of this section is held invalid or unenforceable under any
particular circumstance, the balance of the section is intended to apply,
and the section as a whole is intended to apply in other circumstances.

It is not the purpose of this section to induce you to infringe any
patents or other property right claims or to contest validity of any
such claims; this section has the sole purpose of protecting the
integrity of the free software distribution system which is
implemented by public license practices.  Many people have made
generous contributions to the wide range of software distributed
through that system in reliance on consistent application of that
system; it is up to the author/donor to decide if he or she is willing
to distribute software through any other system and a licensee cannot
impose that choice.

This section is intended to make thoroughly clear what is believed to
be a consequence of the rest of this License.

  12. If the distribution and/or use of the Library is restricted in
certain countries either by patents or by copyrighted interfaces, the
original copyright holder who places the Library under this License may add
an explicit geographical distribution limitation excluding those countries,
so that distribution is permitted only in or among countries not thus
excluded.  In such case, this License incorporates the limitation as if
written in the body of this License.

  13. The Free Software Foundation may publish revised and/or new
versions of the Lesser General Public License from time to time.
Such new versions will be similar in spirit to the present version,
but may differ in detail to address new problems or concerns.

Each version is given a distinguishing version number.  If the Library
specifies a version number of this License which applies to it and
"any later version", you have the option of following the terms and
conditions either of that version or of any later version published by
the Free Software Foundation.  If the Library does not specify a
license version number, you may choose any version ever published by
the Free Software Foundation.

  14. If you wish to incorporate parts of the Library into other free
programs whose distribution conditions are incompatible with these,
write to the author to ask for permission.  For software which is
copyrighted by the Free Software Foundation, write to the Free
Software Foundation; we sometimes make exceptions for this.  Our
decision will be guided by the two goals of preserving the free status
of all derivatives of our free software and of promoting the sharing
and reuse of software generally.

                            NO WARRANTY

  15. BECAUSE THE LIBRARY IS LICENSED FREE OF CHARGE, THERE IS NO
WARRANTY FOR THE LIBRARY, TO THE EXTENT PERMITTED BY APPLICABLE LAW.
EXCEPT WHEN OTHERWISE STATED IN WRITING THE COPYRIGHT HOLDERS AND/OR
OTHER PARTIES PROVIDE THE LIBRARY "AS IS" WITHOUT WARRANTY OF ANY
KIND, EITHER EXPRESSED OR IMPLIED, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR
PURPOSE.  THE ENTIRE RISK AS TO THE QUALITY AND PERFORMANCE OF THE
LIBRARY IS WITH YOU.  SHOULD THE LIBRARY PROVE DEFECTIVE, YOU ASSUME
THE COST OF ALL NECESSARY SERVICING, REPAIR OR CORRECTION.

  16. IN NO EVENT UNLESS REQUIRED BY APPLICABLE LAW OR AGREED TO IN
WRITING WILL ANY COPYRIGHT HOLDER, OR ANY OTHER PARTY WHO MAY MODIFY
AND/OR REDISTRIBUTE THE LIBRARY AS PERMITTED ABOVE, BE LIABLE TO YOU
FOR DAMAGES, INCLUDING ANY GENERAL, SPECIAL, INCIDENTAL OR
CONSEQUENTIAL DAMAGES ARISING OUT OF THE USE OR INABILITY TO USE THE
LIBRARY (INCLUDING BUT NOT LIMITED TO LOSS OF DATA OR DATA BEING
RENDERED INACCURATE OR LOSSES SUSTAINED BY YOU OR THIRD PARTIES OR A
FAILURE OF THE LIBRARY TO OPERATE WITH ANY OTHER SOFTWARE), EVEN IF
SUCH HOLDER OR OTHER PARTY HAS BEEN ADVISED OF THE POSSIBILITY OF SUCH
DAMAGES.

                     END OF TERMS AND CONDITIONS

           How to Apply These Terms to Your New Libraries

  If you develop a new library, and you want it to be of the greatest
possible use to the public, we recommend making it free software that
everyone can redistribute and change.  You can do so by permitting
redistribution under these terms (or, alternatively, under the terms of the
ordinary General Public License).

  To apply these terms, attach the following notices to the library.  It is
safest to attach them to the start of each source file to most effectively
convey the exclusion of warranty; and each file should have at least the
"copyright" line and a pointer to where the full notice is found.

    <one line to give the library's name and a brief idea of what it does.>
    Copyright (C) <year>  <name of author>

    This library is free software; you can redistribute it and/or
    modify it under the terms of the GNU Lesser General Public
    License as published by the Free Software Foundation; either
    version 2.1 of the License, or (at your option) any later version.

    This library is distributed in the hope that it will be useful,
    but WITHOUT ANY WARRANTY; without even the implied warranty of
    MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
    Lesser General Public License for more details.

    You should have received a copy of the GNU Lesser General Public
    License along with this library; if not, write to the Free Software
    Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, MA  02110-1301  USA

Also add information on how to contact you by electronic and paper mail.

You should also get your employer (if you work as a programmer) or your
school, if any, to sign a "copyright disclaimer" for the library, if
necessary.  Here is a sample; alter the names:

  Yoyodyne, Inc., hereby disclaims all copyright interest in the
  library `Frob' (a library for tweaking knobs) written by James Random Hacker.

  <signature of Ty Coon>, 1 April 1990
  Ty Coon, President of Vice

That's all there is to it!
```

## Appendix G — GCC Runtime Library Exception 3.1

Applies to: header-derived material in the shipped `modernc.org/libc` packages that
carries a GCC notice — `limits`, `pthread`, `signal`, `stdio` and `uuid/uuid`
(section 6).

This Exception is an additional permission under GPLv3 §7. It is reproduced beside
the GPL token it accompanies so that a reader who finds "GPL" in a distributed file
also finds the grant that licenses its output. Measured in section 6: all 52 files
that name GCC also name this Exception, so the GPL token never appears in them
without it. Whether anything further is owed for that material is the question
recorded in section 6 and in row F2 of the decision sheet; this appendix does not
answer it.

```text
Copyright (C) 2009 Free Software Foundation, Inc. <http://fsf.org/>

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.

This GCC Runtime Library Exception ("Exception") is an additional
permission under section 7 of the GNU General Public License, version
3 ("GPLv3"). It applies to a given file (the "Runtime Library") that
bears a notice placed by the copyright holder of the file stating that
the file is governed by GPLv3 along with this Exception.

When you use GCC to compile a program, GCC may combine portions of
certain GCC header files and runtime libraries with the compiled
program. The purpose of this Exception is to allow compilation of
non-GPL (including proprietary) programs to use, in this way, the
header files and runtime libraries covered by this Exception.

0. Definitions.

A file is an "Independent Module" if it either requires the Runtime
Library for execution after a Compilation Process, or makes use of an
interface provided by the Runtime Library, but is not otherwise based
on the Runtime Library.

"GCC" means a version of the GNU Compiler Collection, with or without
modifications, governed by version 3 (or a specified later version) of
the GNU General Public License (GPL) with the option of using any
subsequent versions published by the FSF.

"GPL-compatible Software" is software whose conditions of propagation,
modification and use would permit combination with GCC in accord with
the license of GCC.

"Target Code" refers to output from any compiler for a real or virtual
target processor architecture, in executable form or suitable for
input to an assembler, loader, linker and/or execution
phase. Notwithstanding that, Target Code does not include data in any
format that is used as a compiler intermediate representation, or used
for producing a compiler intermediate representation.

The "Compilation Process" transforms code entirely represented in
non-intermediate languages designed for human-written code, and/or in
Java Virtual Machine byte code, into Target Code. Thus, for example,
use of source code generators and preprocessors need not be considered
part of the Compilation Process, since the Compilation Process can be
understood as starting with the output of the generators or
preprocessors.

A Compilation Process is "Eligible" if it is done using GCC, alone or
with other GPL-compatible software, or if it is done without using any
work based on GCC. For example, using non-GPL-compatible Software to
optimize any GCC intermediate representations would not qualify as an
Eligible Compilation Process.

1. Grant of Additional Permission.

You have permission to propagate a work of Target Code formed by
combining the Runtime Library with Independent Modules, even if such
propagation would otherwise violate the terms of GPLv3, provided that
all Target Code was generated by Eligible Compilation Processes. You
may then convey such a combination under terms of your choice,
consistent with the licensing of the Independent Modules.

2. No Weakening of GCC Copyleft.

The availability of this Exception does not imply any general
presumption that third-party software is unaffected by the copyleft
requirements of the license of GCC.
```
