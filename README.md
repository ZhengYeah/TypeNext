# TypeNext

**Writing completion for Windows — local models or remote APIs — 0.1.2 preview**

TypeNext reads bounded existing text around the caret in an approved, accessible textbox, asks a model for a continuation, and shows a floating preview. It inserts the text only when you accept it.

**This is a desktop helper, not a registered Windows TSF IME and not universal in-editor ghost text.** It uses UI Automation. Word, Typora, WeChat, VS Code, and the Windows interface have not been live-tested in the delivery environment. Start with disposable text.

## New in 0.1.2: API support

Open **API settings…** to configure remote OpenAI Chat Completions-compatible endpoints, keys, and request options. Presets cover OpenAI, DeepSeek, OpenRouter, local Ollama, local compatible servers, and a custom compatible API. API keys entered in the UI are encrypted with current-user Windows DPAPI and bound to the endpoint. Environment-variable keys are also supported.

**Remote mode sends textbox context off your computer and may incur API charges.** It requires explicit endpoint approval. Automatic remote use has a separate opt-in, in addition to the existing Automatic suggestions setting. Default and migrated configurations remain local-only.

Read **[API_SETUP.md](API_SETUP.md)** for setup, provider URLs, key storage, privacy boundaries, and troubleshooting. Presets do not guarantee a particular model is available; enter the exact model ID from your account. Native Anthropic Messages and Responses-only APIs are not implemented.

## Upgrade

Quit the previous TypeNext from its tray menu. Closing the main window only hides it. Extract the new ZIP and run the new `TypeNext.exe`. Your settings remain at `%APPDATA%\TypeNext\config.json`; existing model, app, and custom-shortcut choices are preserved.

The 0.1.1 shortcut-conflict fix is retained. TypeNext keeps running when a shortcut cannot be registered. Open **Shortcuts…** to change it or enter `None` to disable it. Another application's shortcut is not overridden.

## Start with an API

1. Run `TypeNext.exe` on Windows x64. TypeNext itself needs no Python, Node, Go, or .NET runtime installation.
2. Open **API settings…**, choose a provider and **Use preset**, enter your model ID and key, and enable **Allow remote HTTPS API requests**.
3. Click **Test API (sample)** and approve the exact endpoint. The test sends a fixed sample, not application text. API usage can still be charged.
4. Close API settings, open the test pad, and request a continuation with **Ctrl+Shift+F9**. Accept the completed preview with **Ctrl+Shift+F10**.
5. Use **Inspect in 3s** in real applications before enabling automatic suggestions. Inspection displays accessible context locally and does not call the model.

API keys belong in the masked key field, not in screenshots, documents, or chat messages.

## Start with a local model

Install and start Ollama separately. For the existing example configuration, run:

```powershell
ollama pull qwen3:4b
```

Set Provider to `ollama`, Model to `qwen3:4b` (or another installed model), and Server URL to `http://127.0.0.1:11434`. Click **Test model**. It sends a fixed sample. TypeNext does not download or start models for you. A local server must also be configured not to forward prompts to cloud services.

For an OpenAI-compatible local server, select `openai-compatible`, enter its base URL (for example `http://127.0.0.1:1234/v1`), and use its exact loaded model ID. API settings also accepts a full `/chat/completions` endpoint. Local servers needing authentication can use the same saved-key or environment-key settings.

## Controls

| Action | Default shortcut |
|---|---|
| Read focused textbox and request continuation | Ctrl+Shift+F9 |
| Accept a finished suggestion | Ctrl+Shift+F10 |
| Cancel/dismiss | Esc |
| Pause/resume | Ctrl+Shift+F11 |
| Accept with Tab | Optional; initially off |

Shortcut fields accept names, not captured keypresses. Use Ctrl or Alt with optional Shift and an allowed key; `None` disables the action. **Apply shortcuts** shows Active, Disabled, or Unavailable. Defaults cannot be guaranteed free on every computer. Re-applying an unchanged active binding does not register it twice.

Automatic suggestions are off by default. Local automatic mode initially waits 750 ms after typing activity. Automatic remote requests additionally require a second permission and are spaced by at least three seconds. This is not a cost cap. Typing, focus changes, or edits cancel stale suggestions; cancellation cannot retract a remote request already sent.

The suggestion card streams text but cannot be accepted until a completed response has been checked. It does not take focus. Clicking the card does not insert it. Closing settings hides TypeNext; choose **Quit** from the tray to stop it.

## Approve applications

The default executable allowlist is:

```text
notepad.exe
winword.exe
typora.exe
wechat.exe
weixin.exe
```

This grants permission to attempt a read; it is **not a tested compatibility list**. Add `Code.exe` for VS Code deliberately. Browser approval (`chrome.exe` or `msedge.exe`) applies to accessible textboxes across that browser, not one website. The built-in test pad is separately allowed.

Password-manager applications, credential dialogs, and terminal hosts on the built-in blocklist remain blocked even if added. This list is not exhaustive, and ordinary textboxes can contain sensitive material. TypeNext's own API-settings fields are not used as completion context.

## What text can it read?

A dedicated UI Automation worker reads the focused editable/document element only. It requires a readable text pattern and a reliable collapsed selection/caret. It checks password, focus, enabled, and available read-only properties. By default it reads up to **1000 characters before** and **200 after** the caret. The network client independently enforces these limits.

There is no clipboard, OCR, select-all operation, rolling typed-character transcript, full-document scan, or scraping of conversation panes. UI Automation runtime IDs, window handles, and caret coordinates stay in the process. Only the bounded prefix/suffix and a fixed instruction enter the model request.

An editor can expose only a fragment or no usable text. TypeNext does not guess that the caret is at the end. A TSF service or app-specific adapter would be needed for controls the UIA reader cannot access. API support does not change this limitation.

## Privacy and insertion limitations

TypeNext does not write source text, generated suggestions, or plaintext keys to its configuration or logs. Saved keys use endpoint-bound, current-user DPAPI ciphertext; encrypted local storage is not protection from malware running as you. Keys and text necessarily exist in memory while used. OS paging/crash dumps and model-server behavior are outside TypeNext's control.

Remote inference requires HTTPS, certificate validation, explicit endpoint approval, and a separate automatic-use permission. Redirects and proxy environment variables are disabled. URL credentials/query-string secrets are not accepted. Local endpoints can still forward or log prompts; a loopback URL does not prove local inference. Remote providers may retain data under their policies.

Before insertion, focus and context are checked again after acceptance-key release. Text is inserted with Unicode keyboard input, not the clipboard. Newlines, tabs, and control characters are removed or replaced; TypeNext never deliberately presses Enter or clicks Send. These checks followed by `SendInput` are not an atomic edit transaction. A small cross-process edit/focus race remains.

Commit Chinese/Japanese IME composition before requesting or accepting suggestions. Modern TSF-only composition detection is not comprehensive. Elevated apps and custom controls can block input. Do not use this preview for passwords, secrets, banking forms, or other high-stakes text. Always review a suggestion before accepting it.

## Build and verification

The release is unsigned. Do not disable security tools to run it. Review the source and rebuild when appropriate. **The supplied binary was built with the older Go toolchain available in the delivery environment; rebuild with a currently maintained Go release before production use.** The actual version and checks are recorded in `docs/VERIFICATION.md`.

There are no external Go module dependencies. On Windows x64, with a maintained Go toolchain:

```powershell
.\scripts\Build.ps1
```

This runs portable tests, Windows-specific ABI and DPAPI tests, static analysis, and compilation. The DPAPI tests use a dummy key, not your credentials. Cross-build on Linux/macOS:

```sh
go test -race -cover ./internal/core
GOOS=windows GOARCH=amd64 go vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath \
  -ldflags='-H=windowsgui -s -w' -o TypeNext.exe ./cmd/typenext
```

Portable tests use mock HTTP/HTTPS servers and test-only credentials. No live commercial model provider or user API key was used for this release. Cross-compilation does not verify Windows UI/DPAPI runtime behavior. Read `docs/VERIFICATION.md` and `docs/WINDOWS_TEST_PLAN.md` before wider use. The included GitHub Actions workflow has not been run for this delivery.

## License

TypeNext source is MIT-licensed. The statically linked Go runtime/standard-library license is in `THIRD_PARTY_GO_LICENSE.txt`. No model weights, third-party API credits, or paid service subscriptions are included.
