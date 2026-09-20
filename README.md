# TypeNext

**Writing autocompletion for Windows — local models or remote APIs**

TypeNext started as an attempt to bring VS Code's inline completion and inline chat to any application, in a lightweight way. Today it offers:

- **Across applications.** Use one completion helper in supported browsers, editors, and Office text fields. Compatibility depends on each application's text and focus interfaces.
- **Not an IME.** Keep your current input method, including Chinese, Japanese, and Korean IMEs.
- **Your choice of model.** Run a local model or connect to a cloud API.

TypeNext reads a bounded amount of text around the caret in an approved application, asks a model for a continuation, and shows a floating preview. When accessible text is unavailable, it can reconstruct recent typing in the current field. Text is inserted only when you accept it. The context engine uses UI Automation, an in-memory keyboard tracker, and the existing PowerPoint adapter; no TSF service, input-method registration, or C++ DLL is required.


https://github.com/user-attachments/assets/ca52d078-7969-4951-a03c-18c22873f3fe


> [!NOTE]
> Weixin/WeChat support is experimental. Keyboard fallback may help with ordinary non-IME typing only when TypeNext can verify the focused field's identity and protection properties. This is not a compatibility guarantee. Chinese/Japanese/Korean input still requires readable committed text from UI Automation or an existing adapter.

> [!TIP]
> If you are experienced with Go and Windows development, and interested in contributing, feel free to join. I may not have time to refine the codebase or add features, but I can review pull requests and discuss design.

## Start with an API

1. Run `TypeNext.exe` on Windows x64. TypeNext itself needs no Python, Node, Go, or .NET runtime installation.
2. Open **API settings…**, choose a provider and **Use preset**, enter your model ID and key, and enable **Allow remote HTTPS API requests**.
3. Click **Test API (sample)** and approve the exact endpoint. The test sends a fixed sample, not application text. API usage can still be charged.
4. Close API settings, open the test pad, and request a continuation with **Ctrl+Space**. Accept the completed preview with **Ctrl+Shift+F10**.
5. (Optional) Use **Inspect in 3s** in real applications before enabling automatic suggestions. Inspection displays accessible context locally and does not call the model.

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
| Read focused textbox and request continuation | Ctrl+Space |
| Accept a finished suggestion | Ctrl+Shift+F10 |
| Cancel/dismiss | Esc |
| Pause/resume | Ctrl+Shift+F11 |
| Accept with Tab | On by default; configurable in settings |

Automatic suggestions are off by default. Enable **Automatic suggestions** in the main window. For a remote API, also enable **Allow automatic remote requests** in API settings and approve the endpoint. With only the first switch enabled, remote completion remains manual; the main status text explains this.

The default typing-pause delay is 1000 ms. Settings are saved in `%APPDATA%\TypeNext\config.json`; [config.example.json](config.example.json) lists the factory defaults. Saved settings override those defaults.

## Text context and keyboard fallback

TypeNext checks text capabilities even when the focused control reports `Custom`, `Pane`, or another type. It prefers `TextPattern2`/`TextPattern` and searches a small number of nearby ancestors and descendants for a provider associated with the active caret. A `ValuePattern` or legacy accessible value can supplement tracked typing, but its text alone does not establish the caret position.

**Keyboard context fallback** is enabled by default. Disable its checkbox in the main window, or set `keyboard_context_tracking` to `false`, to use only readable UIA/adapter context. The tracker retains at most 12,000 Unicode characters for the current focused field; focus changes, pause, and settings changes discard its history. Readable UIA snapshots replace the tracked state.

Inspection reports the context source and synchronization state. `synchronized` means an authoritative read supplied the text/caret; `tracked` is reconstructed context with lower confidence; `unknown` or `uncertain` means completion needs a fresh read or new usable typing. Confidence is a quality indicator, not a measured probability of correctness.

Keyboard fallback cannot recover existing text it never observed. Clipboard operations, undo/redo, mouse repositioning, and ambiguous navigation clear tracked context. IME/CJK input and dead-key composition are not reconstructed. App-generated edits or rejected keystrokes can remain undetected while UIA is unavailable. See [the context engine and Windows validation checklist](docs/CONTEXT_ENGINE.md) for supported edits and remaining limits.

## Approve applications

The default executable allowlist is:

```text
chrome.exe
msedge.exe
notepad.exe
typora.exe
winword.exe
powerpnt.exe
```

This grants permission to attempt a read; it is **not a tested compatibility list**. Browser approval (`chrome.exe` or `msedge.exe`) applies to accessible textboxes across that browser, not one website. The built-in test pad is separately allowed.

Password-manager applications, credential dialogs, and terminal hosts on the built-in blocklist remain blocked even if added. This list is not exhaustive, and ordinary textboxes can contain sensitive material. TypeNext's own API-settings fields are not used as completion context.

Keyboard fallback uses the same application and field checks. Fields with unknown protection properties or no verifiable focused identity remain disabled; unsupported text access does not override a known password, read-only, or selection restriction.

## Privacy and insertion limitations

TypeNext does not write source text, generated suggestions, or plaintext keys to its configuration or logs. Saved keys use endpoint-bound, current-user DPAPI ciphertext; encrypted local storage is not protection from malware running as you. Keys and text necessarily exist in memory while used. OS paging/crash dumps and model-server behavior are outside TypeNext's control.

Remote inference requires HTTPS, certificate validation, explicit endpoint approval, and a separate automatic-use permission. Redirects and proxy environment variables are disabled. URL credentials/query-string secrets are not accepted. Local endpoints can still forward or log prompts; a loopback URL does not prove local inference. Remote providers may retain data under their policies.

Before insertion, focus and context are checked again after acceptance-key release. Text is inserted with Unicode keyboard input, not the clipboard. Newlines, tabs, and control characters are removed or replaced; TypeNext never deliberately presses Enter or clicks Send. These checks followed by `SendInput` are not an atomic edit transaction. A small cross-process edit/focus race remains.

Elevated apps and custom controls can block input. Do not use TypeNext for passwords, secrets, banking forms, or other high-stakes text. Always review a suggestion before accepting it.

## Build and verification

There are no external Go module dependencies. Build with Go 1.23 or later. The executable is unsigned.

On Windows x64:

```powershell
.\scripts\Build.ps1
```

Cross-build on Linux/macOS:

```sh
sh scripts/build.sh
```

Both scripts write `TypeNext.exe` to the project root. The Windows build runs all automated tests and static analysis; the cross-build runs portable tests and checks the Windows source before compiling. Portable tests cover the shadow editor and use mock HTTP/HTTPS servers with test credentials. Windows tests additionally exercise mocked COM providers, keyboard translation decisions, and owned sample controls. These tests do not establish live application compatibility. Use the [Windows validation matrix](docs/CONTEXT_ENGINE.md#windows-validation-matrix) before claiming support for a specific application or version.

## License

TypeNext source is MIT-licensed. The statically linked Go runtime/standard-library license is in `THIRD_PARTY_GO_LICENSE.txt`. No model weights, third-party API credits, or paid service subscriptions are included.
