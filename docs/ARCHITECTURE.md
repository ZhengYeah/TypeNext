# TypeNext implementation notes

## Scope

Version 0.1.2 is a native Windows x64 tray application, not a TSF DLL. It supports local models and explicitly approved remote HTTPS Chat Completions APIs, with a modest installation footprint. Existing IMEs continue to operate normally unless a TypeNext shortcut or optional Tab acceptance overlaps their shortcuts.

```text
focused, approved textbox
        |
        v
Accessibility worker (dedicated MTA / OS thread)
UI Automation or focused PowerPoint document adapter
        |
        | bounded prefix + suffix, focus/caret identity, window, location
        v
local HTTP or approved HTTPS API (cancellable NDJSON / SSE / JSON)
        |
        v
fresh accessibility read + context comparison
        |
        v
non-activating Win32 suggestion window
        |
        | user accepts and releases the shortcut keys
        v
fresh accessibility read + revision/focus comparison
        |
        v
Unicode SendInput; no clipboard or Return-key injection
```

## Code layout

`internal/core/config.go` validates settings and executable permissions. `endpoint.go` normalizes URLs, enforces local-only or approved remote HTTPS policy, and binds consent to a canonical request URL. `client.go` builds completion-only prompts and parses streamed model responses. `text.go` represents immutable context snapshots and sanitizes suggested text. Their tests run on Linux and Windows.

`internal/win/native_windows.go` contains the native API bindings and native structures. `uia_windows.go` implements COM text access on a dedicated, OS-thread-locked MTA; `uia_identity_windows.go` retains a caret range for logical position comparison. `powerpoint_windows.go` implements the focused PowerPoint adapter. `app_windows.go` owns the native windows, settings controls, tray, hook callbacks, timers, and request state. `abi_assert_windows.go` enforces native structure layouts during compilation; `abi_windows_test.go` checks them in Windows tests.

The Win32 message loop owns UI state. Background model/capture operations marshal callbacks through a bounded queue. Input hooks do not perform accessibility or network calls and do not accumulate typed characters. Cancellation invalidates a request generation and increments an atomic input revision. Capture and model callbacks also check request ownership and foreground binding before updating suggestion state. Acceptance checks the original target and input revision before insertion, and its result cannot replace a newer request. Optional Tab acceptance consumes repeat events while waiting for release, so a held acceptance key does not cancel its own pending insertion.

A single accessibility worker prevents unbounded concurrent COM calls. Each caller has a timeout, and cancelled queued jobs are not executed. A malfunctioning third-party accessibility provider may still block the underlying COM call after the caller times out. The worker then remains unavailable until it returns or TypeNext is restarted. An isolated, restartable helper process is a future hardening step.

## Text access details

The foreground executable is checked before text is read. UI Automation reads only the focused element, not its parents, siblings, or the entire application tree. The element must be an Edit or Document, report keyboard focus and enabled status, and not be password-protected. The caret selection must be collapsed. Read-only text is rejected when the provider exposes that attribute.

The native text-pattern interface is acquired through `GetCurrentPatternAs`, rather than assuming an arbitrary `IUnknown` pointer has a text-pattern vtable. Prefix and suffix are independent clones of the caret range. Only the requested endpoints are moved; TypeNext never changes the application's selection to read text.

A snapshot includes the focus identity, foreground HWND, bounded text, logical `CaretID`, and caret geometry. A SHA-256 fingerprint is used only for local equality checks; neither the fingerprint nor the source text is persisted. When a reader supplies a verified logical caret identity, geometry is excluded from this comparison. Thus a blinking native caret or changing positioning fallback does not by itself invalidate unchanged text. Snapshots without a logical identity retain coordinate comparison.

For UIA, the worker retains a cloned caret range and compares both endpoints with the next capture using `CompareEndpoints`. A changed window, focused element, endpoint, or failed comparison produces a new identity. Identical surrounding text at a different position therefore does not reuse the previous identity. Provider behavior still determines range reliability; these checks are not an atomic transaction or proof that all document state is unchanged.

Native caret geometry is preferred. Degenerate UIA range geometry or a neighboring glyph can supply the location. If these are unavailable, the top-left region of the focused control is used, and the inspector labels the positioning fallback. A floating card is used because arbitrary programs do not share an API for rendering inline ghost text.

## PowerPoint slide text (0.1.3 preview)

PowerPoint's slide canvas does not reliably expose the same editable text patterns as a standard Windows textbox. Its adapter uses `AccessibleObjectFromWindow` with `OBJID_NATIVEOM` on the focused `mdiClass` (current desktop PowerPoint) or `paneClassDC` (older versions) document pane. Only the actual keyboard focus and its ancestors are considered; sibling ribbon/search controls cannot reuse a stale slide selection. It binds to that pane's `DocumentWindow`; it does not search open presentations or use a global active-document lookup to choose a target. Foreground, focus, view, and selection are checked around the bounded read.

Only normal/slide editing with `ppSelectionText` and a zero-length `Selection.TextRange` is accepted. Shape selection, a nonempty text selection, unsupported views, or ambiguous ownership fail closed. The adapter reads bounded `Characters(start, length)` slices around the caret rather than retrieving the whole shape or presentation text. Retained presentation identity, slide and shape identifiers, and the text-range start distinguish targets and repeated passages. These identifiers remain local; only prefix/suffix reach the model.

The object-model adapter performs reads only. Acceptance revalidates the current target and context, then uses the existing Unicode `SendInput` path. Popup positioning uses the native caret or PowerPoint range bounds converted through `PointsToScreenPixelsX/Y`, with a pane-corner fallback when caret geometry is unavailable. Notes, masters, slide shows, and embedded chart/SmartArt/table editors are not supported. New default configurations approve `powerpnt.exe`; existing saved allowlists are not expanded. See `VERIFICATION.md` for the live checks completed and their limits.

## Model behavior

The completion client is not a general agent. It does not execute tools, browse, call shell commands, or follow instructions found in a document. Prefix and suffix are provided as JSON data with a fixed completion-only system prompt. This reduces prompt confusion but does not prove that the model will always follow the prompt. The user reviews the generated text.

Ollama's separate `thinking` output is ignored. Visible `<think>` blocks and special termination tokens in the content channel are filtered defensively. Control characters, line separators, and tabs are removed or mapped to spaces. Leading whitespace is intentionally preserved, because it may be needed for insertion. Exact full-prefix/full-suffix echoes can be removed; fuzzy rewriting is avoided.

Incomplete streams and server errors never become an acceptable completion. Partial text is preview-only. Accepted output is limited in characters and sent as UTF-16 Unicode keyboard input in one batch. A partial `SendInput` failure is reported and not retried automatically.

## Boundaries, not promises

UI Automation access is provider-dependent. A TSF-enabled app is not automatically compatible with this reader, and the PowerPoint adapter applies only to its supported slide text. No claims are made that this preview reads full Word documents, Typora's source model, VS Code's complete buffer, or WeChat message history. It reads bounded context from the focused text provider.

Automatic mode detects a typing pause using activity events, not a rolling key transcript. Shift+typing, numpad input, Delete, Enter, and IME processing keys arm automatic completion. The scheduler preserves editing activity from the newly focused window when its next timer observes the foreground change. A pending automatic request waits for modifier release and detected IME composition to finish before capture, while a switch away from its target invalidates it. Modern TSF-only composition detection remains incomplete.

The scheduler checks every 150 ms. The configured delay is a minimum typing pause before starting a request; model response time and the minimum three-second interval between remote automatic starts can add latency. Both global automatic mode and automatic-remote permission are required for a remote request, along with endpoint approval. Main-window status explains when global automatic mode is on but remote automatic use is disabled. Some edits initiated by menus, other assistive tools, or the application itself will not arm automatic completion. Use the manual request shortcut for those cases. Keyboard and mouse activity still invalidate known suggestions; acceptance always attempts a fresh read.

Before insertion, context is rechecked on the accessibility worker after modifier release. This is followed by `SendInput`, not a TSF edit-session write. The application could change in the gap, so atomicity is not guaranteed. Elevated windows, custom controls, modern IME composition, DPI changes, and multiple monitors need live validation.

## Next engineering milestones

A signed installer and a maintained, current build toolchain should precede broad distribution. Run the Windows test matrix first. The most valuable compatibility improvement is an out-of-process broker paired with a native TSF text service and appropriately scoped read/write edit sessions. TSF still requires per-app testing; it is not a guarantee that every application exposes its entire text buffer.

Further improvements include per-window/site permissions, a restartable UIA worker process, model discovery, per-app model/context settings, explicit modern composition events, and a tested VS Code adapter for genuine inline ghost text. These are not included in version 0.1.2.

## Shortcut registration (0.1.1)

`internal/core/hotkeys.go` parses normalized shortcuts, validates duplicate/reserved bindings, and manages independent registrations through an injected registrar. Tests simulate conflicts and release failures without pretending to invoke Windows. `internal/win/hotkeys_windows.go` connects this policy to RegisterHotKey/UnregisterHotKey and implements the Shortcuts settings window.

The manager retains unchanged successful registrations, unregisters changed keys before registering replacements (so swaps work), and never unregisters an action that it did not successfully register. If an unregister operation unexpectedly fails, the manager retains and reports the old active binding. Failed keys do not stop startup and are not exempted from ordinary activity cancellation. The input hook recognizes only active bindings with exact modifiers. Insertion waits for the actual configured accept key as well as modifiers and Tab to be released.

Keyboard choices and model settings are saved together, without application text. Windows registration can still fail at runtime and is therefore displayed as runtime status, not rejected as malformed configuration.

## Remote APIs and credentials (0.1.2)

`internal/win/api_windows.go` implements the API settings window. Its draft is a deep copy of configuration, including the endpoint-key map. Presets only change the form; they do not send requests or approve remote use. Applying or testing requires validated settings and explicit approval for a remote endpoint. A separate permission controls automatic remote requests; global Automatic suggestions must also be enabled. Automatic remote starts have a minimum three-second interval, not an assurance about provider billing or a monthly budget.

`internal/core/endpoint.go` canonicalizes the complete request URL, including API path. It accepts a base URL or a complete `/chat/completions` or `/api/chat` URL, without duplicating the route. A new host, port, or path invalidates old endpoint consent. Loopback HTTP remains available; non-loopback HTTP, URL credentials, query parameters, fragments, ambiguous paths, and invalid hosts are rejected. HTTPS uses certificate validation and TLS 1.2 or newer. The production client disables redirects and environment HTTP proxies. DNS is resolved by the normal HTTPS transport; this is not a firewall, DNS allowlist, or guarantee about a local server's upstream behavior.

`internal/win/secrets_windows.go` uses current-user Windows DPAPI, without the machine-wide flag. Encrypted API keys are stored in the configuration under their canonical endpoint URL; this URL is also additional DPAPI entropy. When switching endpoints, a different saved key is used only if one is already saved for the new endpoint. A configured environment variable is a fallback when no saved key exists; unlike saved keys, the environment variable itself is not mechanically endpoint-bound. If decryption fails, TypeNext fails closed rather than silently trying a different key. The user approves the new destination before any key or completion request is sent.

Plaintext keys are present in the form/request process memory while used. Temporary mutable decryption buffers are cleared, but Go strings, the Windows edit control, the HTTP stack, crash dumps, or a process running as the same user can still expose secrets. DPAPI is at-rest protection, not protection against an already-compromised account. Removing a saved key does not revoke it with the service. TypeNext's own logs/config do not store application text or intentionally store a plaintext API-key field.

`internal/core/client.go` checks configuration and endpoint consent before resolving credentials or starting a network request. It clamps the prefix and suffix again at the network boundary. Only those two strings, the fixed completion instruction, model ID and configured generation options are serialized. Window handles, application names, UIA runtime IDs and caret geometry are not included. The destination naturally observes network/account/request metadata. Test model/API uses a fixed sample, not an accessibility capture.

The compatible client supports Bearer authentication, optional `max_tokens` or `max_completion_tokens`, optional temperature and reasoning effort, SSE streams and ordinary JSON responses. Separate reasoning deltas are ignored. Official DeepSeek requests can include non-thinking mode; official OpenAI requests include `store: false`, which is not a zero-retention guarantee. Parameter support and model availability are provider-specific.

The parser enforces response/event/content bounds, rejects malformed or unfinished streams, and never retries failures automatically. HTTP and inference errors are shown without echoing potentially sensitive server response bodies. Canceled requests cannot resurrect stale suggestions. Cancellation does not guarantee that the provider stops computation or billing.

The API integration does not implement a Responses API, Anthropic Messages, tool calls, file upload, arbitrary headers, provider-specific query parameters, account billing, or model discovery. No cloud provider is selected or contacted during startup or an upgrade.
