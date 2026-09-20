# Text context engine

TypeNext reads available text and reconstructs recent typing when a caret-aware reader is unavailable. The implementation stays in Go with the existing Windows API bindings and PowerPoint adapter. It adds no TSF service or input-method installation.

## Reader order and ownership

1. Use the existing PowerPoint adapter when it applies. An unavailable adapter may fall through; a known selection or protection failure remains blocking.
2. Check the actual focused UIA element's capabilities, preferring `TextPattern2` and then `TextPattern`. `Edit` and `Document` are hints, not required types.
3. Search up to three nearby ancestors, then the focused element's descendants to depth three and a maximum of 24 visited nodes. Ancestor sibling subtrees are not searched. A neighboring provider must establish ownership of the active caret; a stale collapsed selection in another field is insufficient.
4. Read a writable `ValuePattern` or text-role `LegacyIAccessible` value when available. These values have no reliable text caret. They can supplement keyboard context only when the tracked span occurs uniquely and agrees with the value; the end of the value is never assumed to be the caret. Values larger than 8,192 UTF-16 units are ignored for this purpose.
5. Use the current field's keyboard shadow when text access is unavailable and its tracked state is usable. Protection, approval, read-only, known selection, and changed-focus errors do not enable fallback.

Every path remains bound to the foreground application and the actual focused control. Missing password/protection properties or a missing stable focus identity prevent tracking. This means some custom editors will still be unsupported.

## Keyboard shadow and synchronization

`keyboard_context_tracking` defaults to `true`; the main settings checkbox can disable it. The shadow retains only the current field, with a 12,000-character limit and bounded prefix/suffix snapshots. Returning to a previous field does not restore its history. Text stays in memory and is not written to a history file.

| State | Meaning | Completion behavior |
|---|---|---|
| `synchronized` | UIA or the existing adapter supplied text and an established caret. | Use the authoritative context. |
| `tracked` | Recent observed typing and supported edits establish a local context. | Allow a partial context with lower confidence. |
| `unknown` | No usable text is known for this focus. | Wait for a readable snapshot or usable typing. |
| `uncertain` | An operation invalidated the tracked text or caret. | Discard the old context; recover from a fresh read or new typing. |

Confidence is local diagnostic metadata, not a calibrated probability. Typical values are 1.0 for an authoritative read, 0.7 for tracking, and 0.8 for a matching accessible value plus a tracked caret. Only prefix and suffix text are sent as model context; focus identities and diagnostic metadata remain local.

The Windows bridge reconstructs ordinary translated characters, Backspace/Delete, Left/Right, and Shift+Left/Right when their effects are unambiguous. Ctrl+A and Ctrl+Home/End require known document boundaries. Bounded UIA reads do not imply those boundaries are known. Unicode edits that might split a composed grapheme invalidate the shadow.

Clipboard shortcuts, undo/redo, word navigation, plain Home/End, vertical movement, Enter, Tab, and unknown shortcuts invalidate tracking instead of guessing their application-specific effects. Mouse interaction also invalidates it. A subsequent authoritative UIA read can immediately restore context; otherwise new typing establishes only a partial prefix.

IME and CJK layouts use UIA/adapter text after composition has committed. Keyboard fallback does not infer Chinese, Japanese, or Korean committed text from the physical keys, including Latin mode on a CJK input layout. Dead keys and the following composed character are not reconstructed. No clipboard copying or selection-changing recovery shortcut is injected.

## Ordering and insertion

The existing COM worker serializes provider reads and shadow updates. The input hooks enqueue bounded key metadata; they do not call UIA or store a separate text log. Events must match a field admitted before the event occurred. Late events, dropped queue entries, changed focus, and reads spanning intervening input discard their stale state.

Successful authoritative reads replace the shadow. A changed control, pause, settings change, or unsupported operation clears it. Acceptance rechecks the context, caret identity, foreground window, input revision, and composition state before Unicode insertion. TypeNext's own injected keys are marked and are not replayed into the shadow; the next read or typing rebuilds context.

Keyboard hooks observe keys, not the target application's accepted edits. An application may reject a key, perform autocorrection, insert a snippet, or modify text without an observable input event. UIA can repair such differences when it is readable; keyboard-only mode cannot guarantee detection. Text that predates tracking is also unknown. The final checks and Windows `SendInput` remain separate operations, so insertion is not an atomic cross-process transaction.

## Windows validation matrix

Run `scripts/Build.ps1` or the Windows GitHub workflow for automated tests, static analysis, and the executable build. Portable tests cover shadow editing, boundaries, Unicode, focus separation, concurrency, and request metadata. Windows tests cover fake COM discovery/caret behavior, keyboard decoding decisions, and the owned test-pad provider. Passing those tests does not validate the live applications below. The live matrix has not been completed for this refactor; record Windows/app versions and results when testing it.

Use disposable sample text and the built-in test pad or an approved application. **Inspect in 3s** reads locally without requesting a model continuation. For insertion checks, deliberately request and accept a short sample continuation.

| Target or scenario | Check | Expected result |
|---|---|---|
| Built-in pad and Notepad | Inspect at the start, middle, and end; include emoji and repeated passages. | Correct bounded prefix/suffix and stable caret identity; no text selected or changed by inspection. |
| Chrome/Edge text input, textarea, contenteditable | Type, move the caret, and switch between two fields in the same tab. | Context follows the actual field; prior-field text and suggestions are discarded. |
| Custom/Pane provider mocks | Run `TestDiscovery*` tests; exercise active and inactive neighboring providers. | Non-Edit types can expose usable patterns; inactive or unrelated caret ranges are rejected. |
| Value-only / legacy text provider | Type a unique span, then repeat that span elsewhere in the value. | A matching unique span can supplement tracking; ambiguous or mismatching values do not invent a caret. |
| PowerPoint slide text | Test an insertion caret, selected text, switching shapes, and non-text selection. | Existing adapter behavior remains; selection/protection failures do not become keyboard fallback. |
| Weixin/WeChat, explicitly approved | Try an empty message field with a non-IME keyboard; inspect after typing, then click another conversation/field. | Experimental: track only if protection and focused identity checks succeed; otherwise explain why the field is unavailable. No support guarantee. |
| Selection and acceptance | Shift-select text; move focus or type while a suggestion is streaming; accept after a change. | No completion replaces a known selection; stale suggestions cannot be accepted. |
| Paste, cut, undo/redo, mouse movement of caret | Perform each operation after obtaining tracked context. | Old shadow is discarded; use fresh UIA text or rebuild from subsequent typing. Clipboard contents remain unchanged by TypeNext. |
| IME and dead keys | Compose CJK text; use an accented dead-key sequence; switch input layouts. | No preedit/physical-key text is presented as committed text; authoritative reads restore the final text when supported. |
| Password, read-only, unapproved, unknown protection | Inspect and type disposable test data in each field. | No fallback around a known restriction; fields whose protection cannot be verified remain disabled. |
| Bounded history and silent app edits | Use text longer than configured context; then trigger app autocorrection or another programmatic edit. | Truncation never claims complete document boundaries; UIA resynchronizes observable changes. Document any keyboard-only mismatch as a limitation. |
| Tracking disabled, pause/resume, settings change | Toggle the checkbox or pause while a field contains tracked text. | History is cleared; disabled tracking does not reconstruct new text. Authoritative UIA/adapter reads still work when enabled. |

For a compatibility report, include the executable/app version, Windows version, control type, exposed patterns, chosen provider, synchronization state, and the operation that failed. Avoid posting the inspected document text or credentials.
