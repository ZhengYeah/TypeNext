# Verification report — TypeNext

## 0.1.3 PowerPoint pane correction — 19 September 2026

The installed PowerPoint **16.0.20326.20144** exposes the slide editor as `PPTFrameClass -> MDIClient -> mdiClass`, rather than the legacy `paneClassDC` listed in the Office 2000 API documentation. A live metadata probe confirmed `OBJID_NATIVEOM` succeeds on that `mdiClass` pane and exposes Normal view, the active slide pane, and a collapsed text selection. The adapter now accepts both classes while retaining the actual keyboard-focus ancestry and foreground boundaries.

Live verification:

- The production `Capture` path successfully recognized the modern pane and rejected an initial presentation that PowerPoint reported as read-only. The read-only guard remains in place, with a clearer diagnostic.
- Against a subsequently editable presentation, `TestPowerPointLiveNativeRead` passed **three stable bounded reads** through the native adapter's selection, context, and caret-identity code, using the explicitly identified pane HWND.
- The native-read diagnostic does not require foreground focus. Attempts at the separate foreground-capture test timed out waiting for PowerPoint to become foreground, except the read-only rejection above. A full foreground suggestion/acceptance session remains unverified.
- No source text was printed or sent to a model, and no text was inserted or selection changed.

Final `go test -race -count=1 ./...`, `go vet ./...`, and the Windows GUI build passed with **Go 1.27.1 windows/amd64**. The two live tests are opt-in and skipped by ordinary test runs. New focus-tree tests cover modern and legacy panes, child controls, ribbon/search siblings, other presentation windows, absent focus, and malformed parent cycles.

Current workspace artifact: `TypeNext.exe`, version **0.1.3-preview**, **7,163,904 bytes**, SHA-256:

```text
2d80b3e5e9ba083e85945fb3173358bdb9cfb541c5fb430c1a4d17a8a7c81045
```

`SHA256SUMS.txt` describes this executable. Earlier artifact hashes below are historical.

## PowerPoint and suggestion stability update — 19 September 2026

Verified on **Windows amd64 with Go 1.27.1**:

- `go test -count=1 ./...` passed, including Windows ABI and dummy-key DPAPI tests.
- `go test -race -count=1 ./...` passed. After the final PowerPoint container check, `go test -race -count=1 ./internal/win` passed again.
- `go vet ./...` passed against the final source.
- `CGO_ENABLED=0 go build -trimpath -ldflags='-H=windowsgui -s -w' -o TypeNext.exe ./cmd/typenext` succeeded.
- `git diff --check` passed.

New regression tests cover bounded PowerPoint text reads through a fake IDispatch provider, UTF-16 limits and empty suffixes, COM argument order/reference ownership, unsupported selections and containers, presentation/slide/shape/caret identity, logical UIA caret comparisons, stale provider failures, stale request callbacks, focus/input changes, held-Tab acceptance, metadata exclusion from model context, and preservation of existing app approvals.

The COM tests execute native Windows callbacks against a fake provider. They do **not** establish compatibility with an installed PowerPoint version. No live presentation, real model endpoint, or interactive UI/input-hook session was exercised. The PowerPoint and suggestion-stability cases in `WINDOWS_TEST_PLAN.md` remain pending.

Initial PowerPoint/stability artifact (superseded): `TypeNext.exe`, **7,161,344 bytes**, SHA-256:

```text
b6fb8875566c21e098c58937501054ef525957a0d65b8eac36e5806fb97a3813
```

This initial executable was unsigned. The older build and coverage figures below describe the previous API release and are retained as historical records.

## Historical report — 0.1.2 API release

Build date: 17 September 2026.

## Completed in this environment

- **38 top-level platform-independent tests; 198 passing test/subtest entries** executed on Linux using an uncached test run.
- Go race detector passed for these tests. It does not exercise the Windows event loop, native callbacks, or Windows credential APIs.
- Core statement coverage: **93.1%**. This is coverage of `internal/core`, not whole-application coverage.
- Core static analysis (`go vet ./internal/core`) and Windows-target static analysis (`GOOS=windows GOARCH=amd64 go vet ./...`) passed.
- The Windows x64 GUI executable successfully cross-compiled; the output was identified as **PE32+ x86-64 Windows GUI**.
- The Windows-specific ABI and new DPAPI test executable compiled successfully. Its runtime tests were **not executed** here.
- The existing shortcut-conflict test suite is retained and passes.

Full portable test events are in `core-tests.jsonl`; per-function coverage is in `core-coverage.txt`.

## API cases exercised

Tests cover loopback-only defaults; old configuration migration; remote HTTPS opt-in; endpoint-specific approval; base/full URL construction; path, hostname and port validation; rejection of URL credentials, queries, control characters and ambiguous paths; and separate automatic-remote permission with a three-second automatic-request interval.

The API tests include a real HTTPS test server bound to local loopback. A test-only DNS/dial substitution and generated local certificate simulate `api.typenext.test`. Tests confirm valid certificate handling, rejection of an untrusted certificate, Authorization headers, endpoint-scoped saved-key selection, environment fallback, decryption failures, credential validation and non-disclosure of secrets in error messages. Production does not use the test dialer or certificate.

Request/response tests exercise bounded Unicode prefix/suffix transmission, omission of local UI metadata, both token-budget parameter forms, optional temperature and reasoning effort, Ollama/official-provider request options, SSE chunks and ordinary JSON responses, usage/reasoning-only events, response size bounds, cancellation, incomplete/malformed streams, inference errors and unsuccessful HTTP statuses. Redirects are refused, environment proxies are disabled, and API error handling does not retry automatically.

The core tests substitute a mock decryption callback; they do **not** execute Windows DPAPI. Windows-only tests are included for dummy-key encryption/decryption, wrong-endpoint entropy rejection, invalid input and structure layout. They must be executed on Windows to verify runtime behavior.

## Not verified here

There is no interactive Windows desktop/runtime in this build environment. The new API settings window, actual DPAPI encryption/decryption and persistence, native input hooks, UI Automation providers, shortcut registration, display scaling, insertion into applications and IME interactions have **not been live-tested**.

No real API key was supplied and no live commercial inference endpoint was called. OpenAI, DeepSeek, OpenRouter and custom-provider presets are implemented against their documented Chat Completions interfaces, not certified end-to-end with every account/model. Model IDs and optional parameters may require adjustment. No live local model was used either.

Word, Typora, WeChat, VS Code and the built-in pad still require Windows acceptance checks. Remote API support does not expand the UI Automation reader's compatibility. TypeNext remains a desktop completion helper, not a native TSF IME.

Use `WINDOWS_TEST_PLAN.md`, especially the new API and credential checks. Use disposable text and limited credentials initially. Source review, current-toolchain rebuilding, code signing and live Windows testing are necessary before production deployment. This is not a security audit or privacy certification.

## Build details and toolchain caveat

Toolchain actually used: **Go 1.23.2 linux/amd64**. Target: Windows amd64, CGO disabled. Build flags: `-trimpath -ldflags='-H=windowsgui -s -w'`.

**Go 1.23.2 is an older toolchain; this delivery does not claim a current, fully patched runtime.** A maintained-toolchain download was attempted but was unavailable through this environment's download/network paths. Rebuild the source with a maintained Go release before production use, especially for remote inference with sensitive text or credentials. The included Windows CI configuration selects a stable toolchain, but that CI workflow was not run for this delivery.

There are no external Go module dependencies. The executable is **unsigned**. No model weights, API credentials, paid service credits or inference servers are bundled. Do not disable security protections to run the preview.

## Reproduce the completed checks

```sh
go test -count=1 -json ./internal/core
go test -race -count=1 -cover ./internal/core
go vet ./internal/core
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go test -c -o windows-tests.exe ./internal/win
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags='-H=windowsgui -s -w' -o TypeNext.exe ./cmd/typenext
```

On Windows, `scripts/Build.ps1` additionally runs the Windows-specific tests rather than only compiling them. These commands were not run on Windows for this report.

## Executable integrity

File: `TypeNext.exe`  
Size: **5,831,168 bytes**

SHA-256:

```text
2b6c47effe1a7a8cd8c3a31f4f37cde59f63b73137b8085cf148d88ef749c778
```

A checksum detects a different file; it is not a publisher signature, malware scan result or security certification. Different Go compiler versions may produce different binaries and checksums.
