# TypeNext changelog

## 0.1.2 preview — 17 September 2026

- Add an API settings window and tray action, with local, OpenAI, DeepSeek, OpenRouter, and custom OpenAI-compatible presets. Remote presets deliberately require a user-selected model ID.
- Add HTTPS remote inference with explicit endpoint approval. Keep local-only behavior and automatic-remote permission off by default.
- Save endpoint-specific API keys with current-user Windows DPAPI; support environment-variable fallback. No plaintext keys are written by TypeNext.
- Accept API base URLs or full Chat Completions URLs without duplicating the endpoint path.
- Add output-token parameter selection, token budget, timeout, optional temperature/reasoning settings, and sample-only API testing.
- Support SSE streaming, multiline events, and ordinary JSON fallback; ignore reasoning-only output and safely reject malformed/oversized responses.
- Add context clipping at the network boundary, TLS certificate validation, redirect/proxy blocking, safe authentication/rate-limit errors, and no automatic retries.
- Require a separate permission for automatic remote requests; space them by at least three seconds.
- Preserve the 0.1.1 configurable-shortcut fix and existing model/application settings.
- Expand portable HTTP/HTTPS mock tests and add Windows-only DPAPI runtime tests (compiled, not run in the delivery environment).

The Windows UI, real credentials, and live model-provider compatibility remain unverified. See docs/VERIFICATION.md.

## 0.1.1 preview — 15 September 2026

- Do not exit on a global-shortcut registration failure; other shortcuts and settings remain available.
- Add a Shortcuts window accessible from settings and the tray, with editable Suggest/Accept/Pause keys and per-action status.
- Change defaults to Ctrl+Shift+F9, Ctrl+Shift+F10, and Ctrl+Shift+F11. No default is guaranteed available on every computer.
- Support explicit None to disable a key; keep tray pause and optional Tab acceptance usable.
- Preserve existing model and application settings when loading old configurations.
- Retain unchanged successful registrations, support key swaps, retry failed registrations, and report actual Windows errors.
- Update active-key hints and keyboard-hook matching. Wait for the configured acceptance key before insertion.
- Add portable tests for parsing, validation, migration, conflicts, all-keys-unavailable, retries, swaps, disable, cleanup, and release failures.

Windows hotkey and GUI behavior still require live testing. This remains an unsigned experimental desktop helper, not a registered IME.
