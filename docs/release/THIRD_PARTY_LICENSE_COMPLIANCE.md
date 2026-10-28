# Third-party licence compliance — Radanor Sparrow Community Edition v0.1.0

This is the public summary of the third-party licensing position for Radanor Sparrow
Community Edition. It states what is distributed, under which licence, what obligations
follow, and how each statement was verified.

**Status: one legal conclusion is still open.** The measured facts are settled and
reproducible; the *legal characterisation* of two of them is a decision for counsel and
the rights holder, and until that decision is recorded the strict release gate blocks a
public release. Nothing on this page records or implies an approval, and **no release has
been published**.

Scope: this page covers the third-party material that ships with Radanor Sparrow
Community Edition. The product itself is not third-party material — it is © 2026
Radheshyam Dangi under the terms in [`LICENSE`](../../LICENSE).

- Authoritative licence texts and attributions: [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md)
- The questions a human must answer:
  [`THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md`](../licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md)
- The product's own licence: [`LICENSE`](../../LICENSE) and
  [`COMMUNITY_LICENSE_GUIDE.md`](COMMUNITY_LICENSE_GUIDE.md)

## 1. What is actually distributed

Three artifacts, and the licence documents travel inside or beside each of them.

| Artifact | Contents | What it links |
| --- | --- | --- |
| `RadanorSparrow-<version>-x86_64.AppImage` | the supervisor `radanor-sparrow`, the window component `radanor-sparrow-window`, the embedded dashboard, `LICENSE`, `THIRD_PARTY_NOTICES.md` | GTK 3 and WebKitGTK 4.1 **from the host system** |
| `radanor-sparrow` (portable supervisor) | one static executable | nothing at run time |
| `RadanorSparrow.exe` (Windows portable) and `RadanorSparrow-<version>-setup.exe` (NSIS) | the executable, the embedded dashboard, the installer and uninstaller | WebView2 **from the host system**, when present |

Go binaries in this project are built with `CGO_ENABLED=0`, so no C library is linked
into them. The AppImage adds no shared library of its own and does not alter
`LD_LIBRARY_PATH`.

## 2. Dependency inventory

The distributed Go module set is the build closure of the four shipped commands. It
differs by platform, which is why the platform is named.

| Module | Version | Licence | Platform |
| --- | --- | --- | --- |
| `github.com/Microsoft/go-winio` | v0.6.2 | MIT | Windows |
| `github.com/jchv/go-webview2` | v0.0.0-20260205173254-56598839c808 | MIT | Windows |
| `github.com/jchv/go-winloader` | v0.0.0-20250406163304-c1995be93bd1 | ISC | Windows |
| `github.com/mattn/go-isatty` | v0.0.24 | MIT | Windows |
| `github.com/ncruces/go-strftime` | v1.0.0 | MIT | Windows |
| `github.com/dustin/go-humanize` | v1.0.1 | MIT | both |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause | both |
| `github.com/remyoudompheng/bigfft` | v0.0.0-20230129092748-24d4a6f8daec | BSD-3-Clause | both |
| `golang.org/x/sys` | v0.48.0 | BSD-3-Clause | both |
| `modernc.org/sqlite` | v1.60.1 | BSD-3-Clause (SQLite itself: public domain) | both |
| `modernc.org/libc` | v1.77.1 | BSD-3-Clause | both |
| `modernc.org/mathutil` | v1.7.1 | BSD-3-Clause | both |
| `modernc.org/memory` | v1.12.1 | BSD-3-Clause | both |

### JavaScript in the embedded dashboard

| Package | Version | Licence | Distributed? |
| --- | --- | --- | --- |
| `react` | 19.3.0 | MIT | **yes** — bundled into the dashboard |
| `react-dom` | 19.3.0 | MIT | **yes** |
| `scheduler` | 0.28.0 | MIT | **yes** (transitive) |
| `vite` | 8.3.4 | MIT | no — build tool; only its MIT runtime helper lands in the bundle |
| `@vitejs/plugin-react` | 6.1.2 | MIT | no — build tool |
| `typescript` | 7.0.2 | Apache-2.0 | no — compiler |
| `vitest` | 5.0.3 | MIT | no — test runner |
| `@types/react`, `@types/react-dom` | 19.2.2 | MIT | no — type declarations |

### System components that are not bundled

| Component | Licence | How it reaches the user |
| --- | --- | --- |
| GTK 3 | LGPL-2.1-or-later | installed by Ubuntu; loaded at run time |
| WebKitGTK 4.1 | LGPL-2.1-or-later | installed by Ubuntu; loaded at run time |
| WebView2 Evergreen | Microsoft terms | installed by Windows; loaded at run time |

### Installer components

The NSIS installer is built with NSIS's zlib compressor, covered by the zlib/libpng
licence, and the installer payload is compressed by the signed 7-Zip tool from the
pinned MSYS2 packages. `THIRD_PARTY_NOTICES.md` appendix E records the NSIS notice.

## 3. LGPL-2.1 obligations

GTK 3 and WebKitGTK 4.1 are LGPL-2.1-or-later. The LGPL's obligations are not automatic
consequences of "it is LGPL" — they depend on what is distributed and how it is
combined. Three facts decide most of the question here, and all three were measured
rather than assumed:

| Question | Measured answer |
| --- | --- |
| Is any LGPL-covered library copied into the artifact? | **No.** The AppImage contains no shared object at all; the supervisor's executable has no `DT_NEEDED` entries. |
| Is the library modified? | **No.** Nothing in the build patches, recompiles or relinks GTK or WebKitGTK. |
| Can a recipient replace the library with their own build? | **Yes.** No `RPATH`, no `RUNPATH`, no private copy on a search path that would take precedence over the system libraries. |

What the license does require, and what the project does:

- **Notice.** The licence must accompany the work. `THIRD_PARTY_NOTICES.md` states which
  libraries are used and their licence, and ships inside the AppImage and beside the
  Windows artifacts.
- **The licence text itself.** `THIRD_PARTY_NOTICES.md` **appendix F** reproduces the
  complete LGPL-2.1 text and **appendix G** the GCC Runtime Library Exception, so a
  recipient holds the terms and not merely a reference to them.
- **Access to the library source.** Each library's source is available from Ubuntu, from
  the upstream GTK and WebKitGTK projects, and — because the libraries are unmodified
  and separately replaceable — from the distribution the user already has.
- **No restriction on the user's LGPL rights.** Nothing in Sparrow's licence or packaging
  forbids replacing the libraries or reverse engineering for debugging those
  modifications. The window component is a separate executable rather than a linked
  library partly for this reason.

**What is open.** Whether anything *further* is owed — for example a written offer of
corresponding source, or an explicit relinking statement — is a legal conclusion, not a
measurement. This page does not answer it. On **2026-10-28** the rights holder recorded a
**risk decision** (D1) accepting that uncertainty, on the basis that any remedy a stricter
reading would require is an added written offer or notice paragraph; the decision sheet
row F1 records it as *risk accepted by the owner*, and the notices shipped beside the
binary say on their face that the interpretation is open.

## 4. The GCC Runtime Library Exception

Some of the code that compiles into the shipped binaries is derived from GCC and musl
headers. Where such material carries GPL-3.0 header text, the Runtime Library Exception
3.1 matters: it is an additional permission under GPLv3 §7 that allows the resulting
binary to be distributed under terms of the distributor's choosing.

Measured on the files actually compiled into the shipped binaries:

| Measurement (build-selected) | linux/amd64 | windows/amd64 |
| --- | --- | --- |
| Files carrying "GNU General Public License" header text | **1** (`uuid/uuid`) | **3** (`limits`, `pthread`, `sqlite/lib`) |
| …also carrying the Runtime Library Exception | **1** | **3** |
| …without the Exception | **0** | **0** |

The other mentions of "GCC" in the compiled set are incidental — a generator flag
(`-D_GCC_NULLPTR_T`) in a generated-code banner, and SQLite comments such as "when
compiling with `-pedantic-errors` on GCC". Neither is a licence statement.

## 5. `modernc.org/libc` header-derived material

`modernc.org/libc` is a pure-Go translation of a C library. Its generated files contain
type aliases, numeric constants and function bodies derived from system and GCC headers.
The question is what, if anything, of that header material is *distributed* and under
what terms.

| Measurement (build-selected files, amd64) | linux/amd64 | windows/amd64 |
| --- | --- | --- |
| Third-party files compiled (`libc`, `sqlite`, `memory`, `mathutil`, `x/sys`) | **249** | **199** |
| Licence text outside a comment | **0** | **0** |
| Header-derived generated files that actually compile | **2** (`sys/types`, `uuid/uuid`) | **3** (`limits`, `pthread`, `sqlite/lib`) |
| …of those, functions or executable logic rather than constants and type aliases | **0** | **0** |

Two scan artifacts are worth knowing about, both confirmed by reading the files: the SQLite
notice sits in a `/* … */` block comment rather than a `//` one, and identifiers such as
`MCI_INFO_COPYRIGHT` contain the word without carrying the text. So no licence text is
compiled into either binary as data or code, and what does compile from the header material
is numeric constants, struct layouts and typedefs — the category LGPL-2.1 §5's final
paragraph declares unrestricted ("numerical parameters, data structure layouts and
accessors, and small macros and small inline functions").

One file, `modernc.org/libc/int128.go`, additionally reproduces the full MIT notice of
`lukechampine/uint128`, from which some code was copied and adjusted. That notice is in
a `/* … */` block in the source, and the MIT licence requires it to be retained in
copies — which it is.

**What is open.** Whether comment-only header material carries an obligation the project
has not yet discharged — and whether the BSD-3-Clause notice for `modernc.org/libc`
itself covers the derived constants — is a legal conclusion. It is recorded in the
decision sheet (row F2) and is the second reason the strict gate blocks. The package
cannot simply be removed: it is the storage engine underneath `modernc.org/sqlite`, so
dropping it would mean replacing the database layer.

## 6. How these statements were verified

Every number above came from a command that can be re-run. The two that matter:

```sh
# the distributed module set, per platform
go list -deps -f '{{if .Module}}{{if not .Module.Main}}{{.Module.Path}} {{.Module.Version}}{{end}}{{end}}' \
  ./cmd/radanor-sparrow ./cmd/radanor-sparrow-core ./cmd/radanor-sparrow-gateway ./cmd/radanor-sparrow-window | sort -u
GOOS=windows GOARCH=amd64 go list -deps -f '{{if .Module}}{{if not .Module.Main}}{{.Module.Path}} {{.Module.Version}}{{end}}{{end}}' \
  ./cmd/radanor-sparrow | sort -u

# licences, read from the module cache rather than asserted
for m in modernc.org/libc@v1.77.1 modernc.org/sqlite@v1.60.1; do head -4 "$(go env GOMODCACHE)/$m"/LICENSE; done
```

The comment-versus-code classification and the LGPL linkage facts were established with
a scanner that parses Go comments, string literals and raw strings, and with `readelf`
and `ldd` over the built artifacts. Those probes are recorded with the acceptance
evidence rather than committed as scripts, because they depend on the module cache.

Honest limitation: the module-cache licences were read from the *local* module cache at
the exact pinned versions. A future dependency upgrade invalidates every table on this
page, and the release process re-runs these checks against whatever `go.sum` pins.

## 7. If you redistribute Sparrow

The licence documents are not optional extras. Carry these, unmodified, with any copy
you pass on:

- `LICENSE` — Sparrow's own licence, which must be displayed conspicuously on every
  original or modified copy;
- `THIRD_PARTY_NOTICES.md` — the MIT, BSD-3-Clause, ISC, zlib/libpng, LGPL-2.1 and GCC
  Runtime Library Exception texts and the attributions they require;
- if you ship the Windows installer, its notices file as built;
- if you modify or relink anything LGPL-covered, the source and notices that your
  modification then requires.

Because the AppImage carries `LICENSE` and the notices inside itself, and the Windows
artifacts ship them beside the executables, a straight redistribution of an unmodified
artifact already satisfies the notice requirement. A repackaged one does not
automatically.

## 8. Related documents

- [`THIRD_PARTY_NOTICES.md`](../../THIRD_PARTY_NOTICES.md) — the authoritative notices.
- [`THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md`](../licensing/THIRD_PARTY_COMPLIANCE_DECISION_SHEET.md)
  — the open questions, with the evidence and the decision each needs.
- [`COMMUNITY_LICENSE_GUIDE.md`](COMMUNITY_LICENSE_GUIDE.md) — Sparrow's own licence in
  plain language.
- [`FINAL_RELEASE_CHECKLIST.md`](FINAL_RELEASE_CHECKLIST.md) — where this sits in the
  release gate.
