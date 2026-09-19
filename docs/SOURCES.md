# Primary technical references

Consulted for this implementation on 15 September 2026. These are engineering references, not evidence of successful end-to-end testing of TypeNext.

- Microsoft, UI Automation TextPattern overview: https://learn.microsoft.com/en-us/dotnet/framework/ui-automation/ui-automation-textpattern-overview
- Microsoft, UI Automation threading: https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-threading
- Microsoft, IUIAutomationTextRange::MoveEndpointByUnit: https://learn.microsoft.com/en-us/windows/win32/api/uiautomationclient/nf-uiautomationclient-iuiautomationtextrange-moveendpointbyunit
- Microsoft, SendInput and its privilege/input-state restrictions: https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput
- Microsoft's Windows SDK metadata header, used to verify interface GUIDs and method order: https://github.com/microsoft/win32metadata/blob/main/generation/WinSDK/RecompiledIdlHeaders/um/UIAutomationClient.h
- Ollama chat API (streaming, thinking, and keep-alive parameters): https://docs.ollama.com/api/chat
- Ollama Qwen3 4B example model: https://ollama.com/library/qwen3:4b

No external source code from these projects is bundled as a TypeNext dependency. The Go runtime/standard library is statically linked into the executable; its license is provided separately. The Windows API bindings are implemented in this source tree.

## PowerPoint and caret stability update — checked 19 September 2026

- Microsoft, [AccessibleObjectFromWindow](https://learn.microsoft.com/en-us/windows/win32/api/oleacc/nf-oleacc-accessibleobjectfromwindow): the Office 2000 table lists `OBJID_NATIVEOM` on PowerPoint's `paneClassDC` as exposing a `DocumentWindow`. A read-only probe of the installed Office16 desktop PowerPoint on 19 September 2026 confirmed that its corresponding window class is `mdiClass`, with a native document object and collapsed text selection. The adapter supports both classes.
- Microsoft, [Selection.TextRange](https://learn.microsoft.com/en-us/office/vba/api/powerpoint.selection.textrange): accessing the selected text range; TypeNext deliberately supports fewer views than the Office API permits.
- Microsoft, [TextRange.Characters](https://learn.microsoft.com/en-us/office/vba/api/powerpoint.textrange.characters): bounded character slices, including the API's out-of-range clamping behavior.
- Microsoft, [IUIAutomationTextRange::CompareEndpoints](https://learn.microsoft.com/en-us/windows/win32/api/uiautomationclient/nf-uiautomationclient-iuiautomationtextrange-compareendpoints): comparing logical range positions independently of screen geometry.

These references establish API contracts, not live PowerPoint compatibility or a guarantee that every accessibility provider retains caret ranges correctly. The PowerPoint and suggestion-stability manual cases remain in `WINDOWS_TEST_PLAN.md`.

## Shortcut registration update

Microsoft RegisterHotKey reference (checked 15 September 2026): https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-registerhotkey

This documents duplicate registration behavior, MOD_NOREPEAT, modifiers, and F12/Windows-key restrictions. TypeNext reports registration failures rather than trying to take over another application's key.

## API support update — checked 17 September 2026

- OpenAI Chat Completions request/stream schema, token budget and model-dependent parameter support: https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create
- OpenAI authentication (Bearer API key): https://developers.openai.com/api/reference/overview
- DeepSeek OpenAI-compatible API and endpoint: https://api-docs.deepseek.com/
- DeepSeek thinking/non-thinking request options: https://api-docs.deepseek.com/guides/thinking_mode/
- OpenRouter compatible API and streaming: https://openrouter.ai/docs/api_reference/overview
- Microsoft CryptProtectData (current-user DPAPI, additional entropy and allocated-buffer ownership): https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata
- Microsoft CryptUnprotectData: https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptunprotectdata

Provider presets are convenience defaults, not a claim of live certification with every model/account. Remote model IDs are intentionally left for the user to supply. The original API release environment did not provide real commercial API credentials, a Windows runtime, or a successful download route for a newer Go compiler. The later PowerPoint/stability update was tested and built on Windows; see VERIFICATION.md for the checks performed and remaining live-test limits.
