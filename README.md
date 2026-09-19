# TypeNext

**Writing autocompletion for Windows — local models or remote APIs**

TypeNext started as an attempt to bring VS Code's inline completion and inline chat to any application, in a lightweight way. Today it offers:

- **Works everywhere.** Independent of any editor, IDE, or browser — use it in browsers, editors, Office, and more.
- **Not an IME.** Keep your current input method, including Chinese, Japanese, and Korean IMEs.
- **Your choice of model.** Run a local model or connect to a cloud API.

TypeNext reads a bounded amount of existing text around the caret in an approved, accessible textbox, asks a model for a continuation, and shows a floating preview. The text is inserted only when you accept it. It is a desktop helper, not a registered Windows TSF IME: it uses UI Automation plus a few dedicated text adapters, such as one for PowerPoint.


https://github.com/user-attachments/assets/ca52d078-7969-4951-a03c-18c22873f3fe


> [!WARNING]
> Not applicable to Weixin/WeChat. It has restrictions and does not expose the caret or text to accessibility APIs.

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

The suggestion card streams text but cannot be accepted until a completed response has been checked. It does not take focus. Clicking the card does not insert it. Closing settings hides TypeNext; choose **Quit** from the tray to stop it.

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

Both scripts write `TypeNext.exe` to the project root. The Windows build runs all automated tests and static analysis; the cross-build runs portable tests and checks the Windows source before compiling. Portable tests use mock HTTP/HTTPS servers and test credentials. Live application compatibility and model responses require manual checks on Windows.

The icon artwork, ICO, resource definition, and compiled Windows resource are included in `assets/` and `cmd/typenext/`, so normal builds need no icon tools. Technical references are listed in [docs/SOURCES.md](docs/SOURCES.md).

## License

TypeNext source is MIT-licensed. The statically linked Go runtime/standard-library license is in `THIRD_PARTY_GO_LICENSE.txt`. No model weights, third-party API credits, or paid service subscriptions are included.
