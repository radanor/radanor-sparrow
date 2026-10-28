# Third-party compliance decision sheet — Radanor Sparrow Community Edition v0.1.0

**Prepared for:** Radheshyam Dangi (rights holder and Licensor) and qualified legal
review.
**Prepared by:** the engineering side of the release. **This document decides
nothing legally.** It states what was measured, what licence text applies to what,
which obligation is arguably engaged, and the exact question a human answer
unblocks. **F1 and F2 were closed on 2026-10-28 by an owner risk decision that leaves
the legal question open** — the record is in §6 and in
the rights holder's decision record (held privately) D1. Nothing here
is marked settled, and no conclusion was rewritten to obtain a passing gate: the gate
passes because a named person accepted a stated uncertainty, which is a different claim
and is described as such wherever it appears.

Companion records: [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) (the
notice text that ships), the private release decision log (the release
decision log), [`LICENSING.md`](LICENSING.md) (permitted use),
[`RELEASE_SIGNING.md`](../deployment/RELEASE_SIGNING.md) (signing, not licensing).

## 1. Findings at a glance

| # | Subject | Licence engaged | Obligation arguably engaged | Proposed resolution | Decision needed from | Blocks public release |
| --- | --- | --- | --- | --- | --- | --- |
| F1 | GTK 3 + WebKitGTK 4.1 linked at run time by the Linux window component | LGPL-2.1-or-later (WebKitGTK also offers AFL-2.0; BSD-2-Clause files) | LGPL §6 notice and relinking, *if* the distribution is treated as conveying a work that links the library | Ship the provenance and dependency notice (done, §2 below), reproduce the complete LGPL-2.1 text (done, appendix F of the notices), state in the notices that the libraries and their use are covered by the licence (§2, added 2026-10-28) and record that no library is copied, no library is modified, and no RPATH/RUNPATH or bundled copy prevents the recipient from replacing it | Counsel + rights holder | **No longer blocks — closed by owner risk decision 2026-10-28; the interpretation stays open** |
| F2 | Header-derived constants in `modernc.org/libc` packages compiled into the shipped executables | glibc headers: LGPL-2.1-or-later | Whether copied comment text plus interface constants and typedefs amount to a copy of the header | Publish provenance upstream does not publish (done); record the build-selected measurement — 2 header-derived files on Linux, 3 on Windows, constants/typedefs only — and note LGPL-2.1 §5's unrestricted-use rule for such object files | Counsel | **No longer blocks — closed by owner risk decision 2026-10-28; the interpretation stays open** |
| F3 | Header-derived constants derived from **GCC** headers in the same packages | GPL-3.0-or-later **with the GCC Runtime Library Exception 3.1** | The exception exists to license exactly this output; no obligation is expected, but the GPL token appears in a distributed file | Record the exception beside every GPL mention (done, §2 below); no further action proposed | Counsel (confirmation only) | No — confirmation requested |
| F4 | `github.com/sirupsen/logrus` | MIT | None: not distributed | Withdrawn from the inventory as a non-distributed module; do not list it as bundled | Engineering | No |
| F5 | `github.com/hashicorp/golang-lru/v2` (MPL-2.0) | MPL-2.0 | None: reachable in the module graph, linked into nothing that ships | Recorded as not distributed so an SBOM reader cannot mistake it for shipped code | Engineering | No |
| F6 | Radanor Sparrow artwork embedded in the Linux launcher, AppImage and dashboard | Project's own (declared privately retained) | Brand-identity authority, not a third-party licence | Keep the approved artwork; obtain visual approval of the derived 128/256 px sizes | Rights holder | No (release-notes quality item) |

## 2. Evidence behind F1 — the LGPL system libraries

Measured on the shipped artifacts of commit `81745ba`, Ubuntu 22.04.5 host,
2026-10-27:

```
$ readelf -d dist/linux/radanor-sparrow-window | grep -E 'NEEDED|RPATH|RUNPATH'
  (NEEDED)  Shared library: [libwebkit2gtk-4.1.so.0]
  (NEEDED)  Shared library: [libgtk-3.so.0]
  (NEEDED)  Shared library: [libgdk_pixbuf-2.0.so.0]
  (NEEDED)  Shared library: [libgobject-2.0.so.0]
  (NEEDED)  Shared library: [libglib-2.0.so.0]
  (NEEDED)  Shared library: [libc.so.6]
  (no RPATH, no RUNPATH)
$ readelf -d dist/linux/radanor-sparrow | grep -c NEEDED
  0                                  # the supervisor needs no library at all
$ unsquashfs -o $($app --appimage-offset) -ll "$app" | grep -cE '\.so(\.|$)'
  0                                  # the AppImage bundles no shared library
$ ldd dist/linux/radanor-sparrow-window | head -1
  libwebkit2gtk-4.1.so.0 => /lib/x86_64-linux-gnu/libwebkit2gtk-4.1.so.0
```

Host-library provenance, from the installed packages' own copyright files:

| `DT_NEEDED` | Package | Version | Distribution-recorded licence |
| --- | --- | --- | --- |
| `libwebkit2gtk-4.1.so.0` | `libwebkit2gtk-4.1-0` | 2.50.4 | `AFL-2.0 or LGPL-2+`, plus BSD-2-Clause files |
| `libgtk-3.so.0` | `libgtk-3-0` | 3.24.33 | LGPL-2.1+ (with Expat) |
| `libgdk_pixbuf-2.0.so.0` | `libgdk-pixbuf-2.0-0` | 2.42.8 | LGPL-2.1+ (with CC0-1.0) |
| `libgobject-2.0.so.0`, `libglib-2.0.so.0` | `libglib2.0-0` | 2.72.4 | LGPL-2+ (some files Expat) |
| `libc.so.6` | `libc6` | 2.35 | LGPL-2.1 (glibc) |

**What follows from the measurement, stated as facts and not as a conclusion:**

1. No LGPL-covered library is **copied** into any artifact this project
   distributes. The AppImage contains zero shared objects; the two executables are
   the only binaries shipped, and one of them has no `NEEDED` entry at all.
2. No LGPL-covered library is **modified**.
3. Nothing prevents a recipient from **replacing** the library: linking is by
   soname through the loader's default search path, with no `RPATH`, no `RUNPATH`
   and no private copy to shadow the system's.
4. The window component is a **separate executable**, not linked into the product
   binary, so the product's own executable and dashboard carry no copyleft linkage.

**§6 mapped clause by clause, on the conservative reading that it applies.** This is
not a legal conclusion; it is the answer to "if §6 applies, is anything still owed?"
Each of the four requirements is answered by the tree as shipped:

| LGPL-2.1 §6 requirement | Answered by |
| --- | --- |
| Prominent notice that the Library is used and covered by the Licence | `THIRD_PARTY_NOTICES.md` §2, shipped inside the AppImage and beside the Windows artifacts |
| Supply a copy of the Licence | Appendix F, verified identical to the authoritative text on all 418 significant lines |
| Suitable shared library mechanism (or source accompaniment) | soname linking to the host copy, **0** `RPATH`/`RUNPATH`, **0** shared objects inside the image |
| Terms permit modification and reverse engineering for debugging | `LICENSE` grants copying, modification and derivative works, and contains no anti-RE clause |

The one clause not exercised is the conditional one: *"if the work during execution
displays copyright notices"*. Measured — the dashboard displays the licence name and
licence notice, not a copyright notice, so it is not triggered. If a copyright line is
ever added to the About panel, the libraries' notices must appear beside it.

**The question for counsel.** Whether conveying a program that merely links, at run
time, libraries the recipient's own operating system installed and continues to
maintain, engages LGPL-2.1 §6 at all — and if it does, whether the clause mapping
above discharges it, or whether a written offer of corresponding source for the
*headers* is additionally wanted.
Recorded because it is not obvious: nothing here bundles a library, so the
source-offer and relinking-material clauses may simply have no subject to attach
to. That is the lawyer's call, not ours.

**Also measured, and relevant to a future variant:** the AppImage deliberately does
*not* bundle GTK, WebKitGTK, JavaScriptCore, libsoup, bubblewrap or glibc. It does
that for two reasons — LGPL conditions a bundled copy would attract, and keeping
WebKit's helper processes on the host's own entitlements so its sandbox keeps
working. A future "self-contained" AppImage would be a *different artifact* that
must satisfy those conditions before publication. It is not proposed here.

## 3. Evidence behind F2 and F3 — header-derived material

Resolved per shipped binary, not per module:

```
$ GOOS=linux   go list -deps ./cmd/radanor-sparrow | grep '^modernc.org/libc'
  modernc.org/libc/sys/types   modernc.org/libc/uuid/uuid
$ GOOS=windows go list -deps ./cmd/radanor-sparrow | grep '^modernc.org/libc'
  errno fcntl limits pthread signal stdio sys/stat sys/types time unistd
$ go list -deps ./cmd/radanor-sparrow-window | grep '^modernc.org/libc'
  (no output — the window component links none)
```

Why the module is present at all, and why it cannot simply be dropped:

```
$ go mod why modernc.org/libc
  github.com/radanor/radanor-shield/internal/storage
  modernc.org/sqlite
  modernc.org/libc
```

`modernc.org/sqlite` is the pure-Go SQLite driver behind the local database. There
is no build in which the header-derived material disappears without replacing the
storage engine, which this milestone is not authorized to do.

Text versus code, measured across every `.go` file of every distributed package:

| Measurement | Result |
| --- | --- |
| Files scanned | 525 |
| Lines matching an LGPL or GPL notice string | 2 000+ |
| Notice lines **outside** a `//` or `/* */` comment | **0** |
| Files naming GCC | 52 |
| GCC files **without** the Runtime Library Exception | **0** |

The same question asked more narrowly — only the files that actually **compile into** the
shipped binaries on linux/amd64 and windows/amd64, which is the set a recipient's binary is
built from — gives a sharper answer, because it separates a licence notice from an
incidental mention of the compiler:

Re-measured on 2026-10-28 by build selection rather than by scanning directories, which is
the only set a recipient's binary is built from. Reproduce with:

```sh
GOOS=linux|windows GOARCH=amd64 go list -deps \
  -f '{{.ImportPath}}|{{.Dir}}|{{join .GoFiles ","}}' ./cmd/radanor-sparrow ./cmd/radanor-sparrow-window
```

| Measurement (build-selected files only) | linux/amd64 | windows/amd64 |
| --- | --- | --- |
| Third-party files compiled (`libc`, `sqlite`, `memory`, `mathutil`, `x/sys`) | **249** | **199** |
| Files carrying "GNU General Public License" **header text** | **1** (`uuid/uuid`) | **3** (`limits`, `pthread`, `sqlite/lib`) |
| …of which also carry the Runtime Library Exception | **1** | **3** |
| …of which carry GPL text **without** the Exception | **0** | **0** |
| Files carrying the glibc **LGPL-2.1** notice, as comments | **2** (`sys/types`, `uuid/uuid`) | **0** |
| Files that are `modernc.org/libc`'s own BSD-3 code ("The Libc Authors") | **29** | **25** |
| Licence text appearing outside a comment | **0** | **0** |

The earlier figure of 310 files / 6 GPL-text files came from a broader scan of the packages'
directories and is superseded by these build-selected numbers; the conclusion is unchanged
and slightly stronger — the header-derived surface that actually compiles is **two generated
constant files on Linux and three on Windows**, and every GPL notice among them carries the
exception. The remaining mentions of GCC are incidental (the `-D_GCC_NULLPTR_T` generator
flag, SQLite commentary) and carry no licence statement at all.

Two details that a line-prefix scan gets wrong, both checked by reading the files: the
Windows `sqlite/lib/sqlite_g_0000000000060000.go` notice sits in a `/* … */` **block
comment** (legal Go, not code, but not `//`-prefixed, so a naive scan counts it as
non-comment), and identifiers such as `MCI_INFO_COPYRIGHT` contain the word without carrying
the text. No licence text is compiled into either binary as data or as code.

Two upstream sources with different licences are mixed in these packages, which an
earlier audit had collapsed into one "GPL/LGPL" label:

- **glibc-derived** files carry the **LGPL-2.1-or-later** notice (`sys/types`,
  `fcntl`, `sys/stat`, `time`, `unistd`, `pthread`, `signal`, `stdio`,
  `uuid/uuid`).
- **GCC-derived** files carry **GPL-3.0-or-later with the GCC Runtime Library
  Exception 3.1** (`limits`, `pthread`, `signal`, `stdio`, `uuid/uuid`). The
  exception is upstream's own additional permission under GPLv3 §7, and it exists
  precisely so that compiling and distributing its output carries no GPL
  obligation.

What is compiled beneath those comments is declarations, not implementation: size
and flag constants and type aliases, e.g. `SEGV_MAPERR = 1` and the `CLD_*` values
in `signal/signal_linux_amd64.go`, taken from a glibc `siginfo-consts.h` comment
block by `ccgo`.

**A separate, non-legal gap found here:** `modernc.org/libc` is BSD-3-Clause, but
its own `LICENSE-3RD-PARTY.md` lists **neither glibc nor any GNU licence**. A
recipient of this product cannot learn from upstream that these comments are
glibc's. `THIRD_PARTY_NOTICES.md` §2 and §6 now carry that provenance, which is the
notice this project can truthfully give.

**The question for counsel.** Whether copied comment blocks plus interface constants
and typedefs constitute copying copyrightable expression; whether the LGPL-2.1
notice text should be reproduced in full regardless; and whether the GCC Runtime
Library Exception need be reproduced when the GPL text it accompanies is reproduced.
Guidance pointing at the common "headers, constants and interfaces are
non-copyrightable / de minimis, and the exception covers compiler output" line of
reasoning is welcome, but it is a conclusion this sheet does not draw.

## 4. Findings closed without a legal question

| # | Item | Resolution and evidence |
| --- | --- | --- |
| F4 | `github.com/sirupsen/logrus` v1.9.3 | **Not distributed.** `go mod why -m github.com/sirupsen/logrus` reports the main module does not need it; it is absent from `go.mod`, `go.sum` and every `go list -deps` of a shipped binary. Withdrawn from the inventory rather than described as bundled. Corrected in `THIRD_PARTY_NOTICES.md`. |
| F5 | `github.com/hashicorp/golang-lru/v2` v2.0.7 (MPL-2.0) | **Not linked into anything that ships.** MPL-2.0 is file-level copyleft and GPLv3-compatible; with no MPL code distributed it imposes no obligation here. Recorded in `THIRD_PARTY_NOTICES.md` §5 so an SBOM reader does not mistake graph membership for distribution. |
| F7 | Fonts | `Inter` is named first in the dashboard font stack but is neither embedded nor downloaded. No font file is distributed and no font licence obligation arises. |
| F8 | AppImage build toolchain | `appimagetool` 1.9.1 and the type-2 runtime 20251108 are **build inputs, not distributed artifacts**. Both are pinned by SHA-256, verified before execution, and each pin was independently corroborated against the digest GitHub's release API publishes for the asset (see `docs/deployment/RELEASE_SUPPLY_CHAIN.md`). The AppImage they produce contains no part of them beyond the runtime stub, which is MIT. |
| F9 | NSIS installer stub | zlib/libpng licensed and redistributable; covered by `THIRD_PARTY_NOTICES.md` Appendix E. |
| F10 | The LGPL-2.1 and GCC Runtime Library Exception texts | The notices named both licences and, until this pass, reproduced neither: a recipient could read *that* LGPL-2.1 applies but not *what* it says. **Fixed.** `THIRD_PARTY_NOTICES.md` appendix F carries the complete LGPL-2.1 text and appendix G the Runtime Library Exception, each verified byte-identical to the authoritative text. This is not a legal conclusion — it is the notice document being complete whichever way F1 and F2 are answered, so the answer does not require another build. Pinned by `TestThirdPartyNoticesCarryTheLicenceTextsTheyName`. |

## 5. What was answered, and what is still open

**Answered on 2026-10-28, by the rights holder, as risk acceptance rather than as legal
advice:**

1. **F1 —** the LGPL §6 question is not settled. The owner recorded that the shipped
   notice, licence copy and shared-library mechanism are accepted as sufficient, and that
   any remedy a stricter reading would require is added when identified.
2. **F2/F3 —** the header-derived question is not settled either. F3 needed no decision:
   every GPL notice in the compiled set already carries the Runtime Library Exception.
3. **Authority —** publishing under the Radanor brand is recorded under D2 in
   the rights holder's decision record (held privately).

**Still open:**

- **Qualified legal review is recommended, not required.** If counsel later concludes that
  something further is owed, that becomes a concrete engineering task and the gate re-opens.
- **Artwork** — visual approval of the derived 128/256 px launcher icons, or a native
  high-resolution master to derive from instead.

All of these, together with the unsigned Linux and unsigned Windows distribution
decisions and the GitHub release responsibility, are collected for signature in
the rights holder's decision record (held privately) (decisions
D1–D6). That record starts empty on purpose and is the one a release run depends on.

## 6. Approval record

Add one line per decision. An "approved" entry must name the person who decided it
and the date; a decision recorded without a name is not a decision. The distinction
this table now records is between a **counsel conclusion** and an **owner risk
decision** — they close the same release-policy requirement and mean different things.

| Finding | Decision | Decided by | Date | Notes |
| --- | --- | --- | --- | --- |
| F1 | **Risk accepted by the owner** | Radheshyam Dangi (self-declared; identity not independently verified) | 2026-10-28 | LGPL §6 engagement stays legally uninterpreted. What is decided is that the shipped notice, licence copy and shared-library mechanism are accepted as sufficient, and that any remedy a stricter view would require is added when identified |
| F2 | **Risk accepted by the owner** | Radheshyam Dangi (self-declared; identity not independently verified) | 2026-10-28 | Same basis. Supported by measurement, not by counsel: 2 header-derived files compile on Linux, 3 on Windows, constants and typedefs only |
| F3 | **Confirmed, no action** | — | 2026-10-28 | Every GPL notice in the compiled set is accompanied by the Runtime Library Exception; the exception is reproduced in appendix G |
| Brand publishing authority | **Approved under D2** (owner declaration) | Radheshyam Dangi (self-declared) | 2026-10-28 | Radanor is a product brand; the publisher is the individual Licensor |
| Artwork visual approval | ⟨awaiting⟩ | | | Still open: the 128/256 px icons derived from the 48 px master need a human visual decision |

The two markers in `THIRD_PARTY_NOTICES.md` §6 were removed on **2026-10-28** as the
mechanical consequence of the D1 risk decision, in the same change that recorded it. That
is not a claim that the legal questions are settled: the notices say on their face that the
interpretation remains open and that the rights holder accepted the residual uncertainty.
If a concrete obligation is later identified — a missing notice, an uncompiled licence text,
a bundled library — it is corrected and the gate is re-opened, because the decision covers
uncertainty, not known defects.
