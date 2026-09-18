# Windows acceptance test plan

**Status: not executed in the Linux build environment.** Use disposable content. Do not treat a compiled binary or passing model-client tests as proof of application compatibility.

## Baseline

Start TypeNext as a normal user. Verify that settings show and the tray icon appears, that only one instance can run, and that Quit removes the hooks and icon. Save settings, quit, restart, and verify that settings persist. Check settings on the actual display scale you use.

Use Test model with an installed Ollama model. Verify that the test does not access the focused editor. Try a nonexistent model and a stopped server; TypeNext should report an error without freezing. Then restore the working model.

## Shortcut conflicts and migration (0.1.1)

Keep a copy of a 0.1.0 configuration without hotkey fields. Load it in 0.1.1 and verify the model, endpoint, and permissions are retained, with default Suggest = Ctrl+Shift+F9, Accept = Ctrl+Shift+F10, Pause = Ctrl+Shift+F11.

Use a separate, trusted test utility to register a chosen test shortcut, then configure TypeNext to request the same key. TypeNext must stay open and show the affected action as Unavailable, ideally with Windows error 1409. The other active actions must continue working. Repeat with all three keys unavailable. The settings UI and tray must remain available. TypeNext must not unregister or steal the other utility's key.

Choose a different key and Apply. Verify the status, test-pad title, main footer, actual request/accept/pause behavior, and suggestion-card footer all use the new active key. Release the other utility's key and Apply again to retry. Apply unchanged settings repeatedly; no self-conflict should appear. Swap two actions' keys in one Apply. Set Pause to None and verify the tray Pause / resume action still works. Enable Tab, disable the global Accept key, and verify Tab acceptance. Reject duplicate bindings without changing the active ones.

Check the Shortcuts window at your display scale. Open it both from main settings and the tray, close and reopen it, and verify Tab navigation and error messages. Hold the configured Accept key and modifiers to verify insertion waits until all are released. All these Windows checks remain unexecuted in the Linux build environment.

## Existing text and caret positioning

Open the built-in test pad. With its pre-existing paragraph intact, place the caret at the end. Request a suggestion and accept it. Confirm that existing content is not replaced, that the popup does not take focus, and that insertion is exactly the previewed continuation.

Use Inspect in 3s, switch back to the pad, and verify that it reports text that was already present. Move the caret into the middle of the paragraph, run another inspection, and compare the reported prefix and suffix with the visible document. Try long documents, blank suffixes, an empty document, Chinese text, emoji, and a selected span. A selected span should be rejected, not replaced.

## Cancellation and input safety

During slow generation, type another character, move the caret, select another field, click elsewhere, scroll, switch apps, and press Esc. The old suggestion must not reappear or be inserted. Test two textboxes in one window with similar contents. Verify that accepting a stale suggestion is refused.

Accept while holding the shortcut modifiers briefly; TypeNext should wait for release. Keep them held beyond the timeout and verify a safe refusal. A partial Windows input failure must not trigger automatic retries. Test Tab acceptance separately; when there is no finished suggestion, Tab should retain its normal function. Turn the option back off if it conflicts with the current IME/editor.

In a disposable chat draft, verify that accepted text does not send the message. Do not test this in an important conversation. The program never deliberately generates Enter/Return, but the receiving application's handling still requires validation.

## Permissions and privacy

Try an application outside the allowlist. It should be rejected before text retrieval or a model request. Test an ordinary password control in an explicitly approved test application; it should be rejected. Check that known blocked executables remain denied even after being added to the allowlist.

Confirm that config.json contains only settings. Inspect model-server logging separately. Test a remote server URL and a redirecting local endpoint; TypeNext should refuse both. No text logs or clipboard changes should appear.

## IME, privilege, and displays

Use Chinese Pinyin composition. Request and accept only after committing the composition. Verify that suggestion hotkeys and optional Tab acceptance do not steal candidate-selection keys. Leave automatic mode off if the current IME exposes composition poorly.

Test two monitors, negative screen coordinates, the bottom edge of a display, high-DPI scaling, resizing a document, and switching monitors. An elevated application may refuse accessibility/input from the normal-user helper. Do not elevate TypeNext as a blanket workaround.

## Application matrix to fill in

| Application/version | Existing prefix | Mid-text suffix | Popup placement | Acceptance | Composition | Status |
|---|---|---|---|---|---|---|
| Built-in test pad | Pending | Pending | Pending | Pending | Pending | Not live-tested |
| Windows Notepad | Pending | Pending | Pending | Pending | Pending | Not live-tested |
| Microsoft Word | Pending | Pending | Pending | Pending | Pending | Not live-tested |
| Typora | Pending | Pending | Pending | Pending | Pending | Not live-tested |
| WeChat / Weixin | Pending | Pending | Pending | Pending | Pending | Not live-tested |
| VS Code (explicit approval) | Pending | Pending | Pending | Pending | Pending | Not live-tested |
| Browser textarea (explicit approval) | Pending | Pending | Pending | Pending | Pending | Not live-tested |

An unavailable text pattern or caret is a compatibility failure, not evidence that the document is empty. Record the inspector's error rather than enabling an intrusive clipboard or key-history fallback.

## Remote API acceptance checks (0.1.2)

These are manual checks to perform on Windows, not claims of tests already run. Use disposable text and a limited API key/account with a spending cap. Never publish key values, clipboard contents or actual private paragraphs in a bug report.

1. Upgrade from 0.1.1 using a copy of its configuration. Verify all existing application/hotkey/local model settings survive and both remote flags remain off. Opening settings or selecting a preset must not make a model request.
2. Open API settings from the main window and from the tray. Check form layout, keyboard navigation, masked key entry, Close/Esc, large display scaling and owner-window re-enabling. Switching presets should clear the new-key form field and remote checkboxes without deleting already saved keys.
3. Select a provider, enter an exact supported model ID and a disposable API key, allow remote HTTPS, and select Apply. The approval message must name the complete request URL and context bounds. Choose No: nothing may be persisted or sent. Retry and choose Yes. Inspect the config: it must contain only `dpapi:` ciphertext for the key, not the plaintext secret.
4. Test API (sample). Confirm from a controlled API endpoint that only the fixed test prefix/suffix, instruction and generation settings are sent. The active editor must not be read. The test may incur service charges. Close/reopen the window during a test and verify the UI remains responsive; no concurrent repeated test should start.
5. Quit/restart. Test the saved key on the same Windows account. Run `go test ./internal/win` from source to execute the dummy-key DPAPI roundtrip and wrong-scope tests. A copied config on an unrelated account should require key re-entry rather than transmit the encrypted bytes or fall back silently when decryption fails.
6. Change URL host, port or path. New approval must be required. The old saved key must not be sent to an endpoint with no saved key. If an environment variable is configured, its value is an explicit fallback: clear its name to test that no fallback Authorization header is sent. Returning to an already saved endpoint should select that endpoint's saved key only.
7. Enter a replacement key with Remove unchecked, then save/restart and test. Leave blank to keep the saved key. Select Remove with no replacement; the saved entry should be deleted. Removal must not be represented as provider revocation, and configured environment fallback must remain clearly explained.
8. Try a full `/v1/chat/completions` URL and a base `/v1` URL on a controlled server. They should resolve to the same request path, key scope and consent. Plain HTTP remote URLs, URL passwords, query-string keys, fragments, untrusted certificates and redirect destinations must fail without forwarding credentials or context.
9. Exercise invalid key (401), forbidden model (403), wrong model/path (404), rate limit (429), server error, stream error, truncation and cancellation. The UI must not echo response bodies or retry automatically. Unsupported temperature/token/reasoning parameters should give an actionable error; change the relevant setting and explicitly retry.
10. With approved remote mode and global Automatic suggestions on but Allow automatic remote requests off, typing must not call the API. Manual Suggest must still work. Enabling the second remote permission must ask for approval; then successive automatic starts must be at least three seconds apart. Disable either automatic setting to stop automatic remote requests. Pause/resume still applies.
11. In the built-in pad, then each target app separately, manually generate and review a suggestion. Move the caret or edit before accepting; stale text must not be inserted. A remote response must not alter foreground/selection. Acceptance must not send a chat message. Test IME composition with disposable text; commit Pinyin before using TypeNext.
12. Repeat the shortcut conflict checks and local Ollama smoke test after switching back to a local preset. No remote endpoint should be contacted when using local mode. Verify the main connection label and suggestion-card status accurately distinguish local and remote use.

A passing provider test does not validate another provider, model, region, proxy, or enterprise TLS configuration. Record the TypeNext version, Windows version, provider protocol (not key), error code and application version when reporting issues.
