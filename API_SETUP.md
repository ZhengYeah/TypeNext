# TypeNext 0.1.2 — API setup

## Upgrade

Quit the old TypeNext from its system-tray menu. Closing its main window only hides it. Extract the new release and run the new `TypeNext.exe`. Existing model, approved-app, and shortcut settings in `%APPDATA%\TypeNext\config.json` are preserved. Remote API access and automatic remote requests are **off by default** after migration.

## Connect a provider

1. Open **API settings…** in the main window or system-tray menu.
2. Choose a preset and click **Use preset**. This only fills fields; it sends nothing.
3. Enter the **exact model ID** available to your API account and paste your key into the masked **API key** field. Model IDs are intentionally not prefilled for remote providers.
4. For an external provider, check **Allow remote HTTPS API requests**. Leave **Allow automatic remote requests** unchecked initially.
5. Click **Test API (sample)**, then approve the displayed endpoint. The settings are saved and the test sends a fixed sample sentence, not text from another application. A successful test displays the model's continuation.
6. Close API settings. Open the test pad and press your Suggest shortcut (default **Ctrl+Shift+F9**), then accept with **Ctrl+Shift+F10**. Test disposable text in your real applications before relying on the tool.

No local model or GPU is required when using a remote API. TypeNext does not provide API credits or a subscription. Requests, including tests, may incur your provider's API charges. Do not paste a provider key into a chat or share it in screenshots.

## Presets and URL forms

| Preset | API URL field | Protocol |
|---|---|---|
| OpenAI API | `https://api.openai.com/v1` | OpenAI Chat Completions |
| DeepSeek API | `https://api.deepseek.com/chat/completions` | OpenAI-compatible Chat Completions |
| OpenRouter API | `https://openrouter.ai/api/v1` | OpenAI-compatible Chat Completions |
| Custom | Your provider's HTTPS API base or full chat endpoint | OpenAI-compatible Chat Completions |
| Ollama (local) | `http://127.0.0.1:11434` | Ollama `/api/chat` |
| OpenAI-compatible (local) | For example, `http://127.0.0.1:1234/v1` | OpenAI-compatible Chat Completions |

A base URL ending in `/v1` gets `/chat/completions` appended. A full endpoint ending in `/chat/completions` is used directly. A bare compatible hostname without a path defaults to `/v1/chat/completions`. Custom base paths are preserved. The DeepSeek preset uses the full endpoint explicitly.

Native Anthropic Messages, OpenAI Responses-only, Azure endpoints requiring query-string versions or non-Bearer authentication, and arbitrary custom authentication headers are **not implemented**. A compatible gateway may work, but the gateway also receives your text and key. No particular model or account entitlement is guaranteed by a preset. No live commercial-provider calls were made during this build.

## Request options

**Output tokens** is the generation budget, not the context limit. Remote presets fill 256 tokens and a 60-second timeout. The allowed budget is 8–4096; visible suggestions remain capped at 320 characters by default. For reasoning models, internal reasoning can consume the token budget before visible text appears. Prefer a model/mode suitable for short text completion; increase the budget only deliberately.

**Token parameter:** use `max_completion_tokens` for the OpenAI preset. Many compatible providers use `max_tokens`; the setting is selectable. Never send both. This is a provider/model compatibility choice, not a privacy setting.

**Temperature:** uncheck **Send temperature = 0.2** when a model rejects this parameter. OpenAI and OpenRouter presets omit it by default.

**Reasoning effort:** `(omit / provider default)` sends no reasoning-effort field. Choose another value only when the provider and model support it. Requesting `none` is not supported by every model.

**Request non-thinking mode:** sends `think: false` to Ollama. For the official `api.deepseek.com` Chat Completions endpoint, it sends `thinking: {"type":"disabled"}`. It does not force non-thinking behavior on arbitrary gateways or OpenAI models. The setting can be turned off.

Changing a provider, base path, host, or port requires approval for the new canonical remote request URL before text can be sent. Changing the model at an already approved endpoint does not require a second endpoint approval.

## API-key storage

A key entered in API settings is encrypted with **current-user Windows DPAPI** before being saved in `config.json`. It is associated with the exact canonical request endpoint and uses that endpoint as additional encryption entropy. Keys for up to 32 endpoints can be retained. Switching the endpoint does not reuse another endpoint's saved key.

Leave the key field blank to keep a previously saved key for the displayed endpoint. To replace it, type a new key. To remove its encrypted local copy, check **Remove this endpoint's key** and Apply with the key field blank. Deletion here does not revoke the provider's key; revoke it through the provider when needed.

A saved key takes priority. Without one for the current endpoint, TypeNext reads the named **Key environment** variable, if nonempty. Presets use `OPENAI_API_KEY`, `DEEPSEEK_API_KEY`, `OPENROUTER_API_KEY`, or `TYPENEXT_API_KEY`. Enter the variable's name, not the key, in that field. Environment variables are inherited at launch: fully restart TypeNext after changing them. Clearing this field disables environment fallback. Removing a saved key does not prevent a separately configured environment key from being used.

DPAPI is encryption at rest, not protection against malware running under your Windows account. Keys must exist in process memory while used. Moving the configuration to another user/computer usually requires re-entering the key. If a saved key cannot be decrypted, TypeNext fails rather than silently falling back to an environment key.

## Privacy and usage control

The network payload contains a fixed completion instruction plus bounded text before and after the caret (initially 1000 and 200 characters). It does not include the executable name, window ID, accessibility runtime ID, or caret coordinates. The provider still receives normal request metadata and your API credential if configured.

**A remote API is not local inference.** The provider may process or retain text under its policies. Do not use confidential messages, research drafts, or organizational data without permission. App approval is not a sensitivity detector; ordinary textboxes can contain secrets even when they are not password fields. TypeNext's password checks depend on application accessibility properties.

Automatic remote completion requires BOTH **Automatic suggestions** in the main window and **Allow automatic remote requests** in API settings. It may send unfinished text after pauses. Such requests are spaced by at least three seconds and are never retried automatically. This throttle is not a spending cap; configure limits in your provider account. Manual requests and fixed-sample tests are explicit actions, not subject to the automatic throttle. Cancelling a request cannot retract text already sent or guarantee that the provider stops billing.

Remote URLs must use HTTPS with a valid certificate. Redirects, URL credentials, query strings, and environment/system HTTP proxies are not supported. A proxy-required corporate network may therefore fail to connect. HTTP is allowed only for literal loopback or `localhost`. Local servers can themselves forward or log requests; TypeNext cannot verify that they run entirely offline.

The official OpenAI endpoint is requested with `store: false`. This is NOT a guarantee of zero provider-side retention. TypeNext does not log keys, source textbox text, or model responses to disk, but OS memory paging, crash dumps, server logs, and same-account malware are outside that guarantee.

## Troubleshooting

| Result | Check |
|---|---|
| Remote APIs are off | Enable remote HTTPS in API settings and Apply. |
| Endpoint not approved | Apply API settings and approve the exact displayed URL. |
| HTTP 401 | Key, environment-variable inheritance, and saved-key precedence. |
| HTTP 403 | Key permissions and account access to the model. |
| HTTP 404 | Exact model ID and base/full endpoint URL. |
| HTTP 400 / 422 | Token parameter, temperature, reasoning options, and provider format. |
| HTTP 429 | Provider quota/rate limits; no automatic retry is performed. |
| Cannot reach API | Connectivity, URL, valid TLS certificate, and whether a redirect/proxy is required. |
| Cannot unlock saved key | Re-enter the key on this Windows account; do not edit ciphertext manually. |
| No usable continuation | Choose non-thinking completion, omit unsupported reasoning options, or raise the token budget. |
| Model works, app fails | Use **Inspect in 3s**. API support does not fix inaccessible editor controls. |

See `docs/VERIFICATION.md` for the actual test scope and build-toolchain limitation. This is an unsigned experimental build, not production-hardened software.

## References checked for this update

- OpenAI Chat Completions: https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create
- OpenAI authentication: https://developers.openai.com/api/reference/overview
- DeepSeek endpoint and compatibility: https://api-docs.deepseek.com/
- DeepSeek thinking mode: https://api-docs.deepseek.com/guides/thinking_mode/
- OpenRouter API: https://openrouter.ai/docs/api_reference/overview
- Windows DPAPI: https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata
