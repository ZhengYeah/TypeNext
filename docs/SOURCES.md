# Primary technical references

Engineering references for the implementation. These describe API contracts; application compatibility requires testing on Windows.

- Microsoft, UI Automation TextPattern overview: https://learn.microsoft.com/en-us/dotnet/framework/ui-automation/ui-automation-textpattern-overview
- Microsoft, UI Automation threading: https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-threading
- Microsoft, IUIAutomationTextRange::MoveEndpointByUnit: https://learn.microsoft.com/en-us/windows/win32/api/uiautomationclient/nf-uiautomationclient-iuiautomationtextrange-moveendpointbyunit
- Microsoft, SendInput and its privilege/input-state restrictions: https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-sendinput
- Microsoft's Windows SDK metadata header, used to verify interface GUIDs and method order: https://github.com/microsoft/win32metadata/blob/main/generation/WinSDK/RecompiledIdlHeaders/um/UIAutomationClient.h
- Microsoft, UI Automation support for standard controls: https://learn.microsoft.com/en-us/windows/win32/winauto/uiauto-controlsupport
- Microsoft, creating the Rich Edit control used by the test pad: https://learn.microsoft.com/en-us/windows/win32/controls/create-rich-edit-controls
- Microsoft, EM_SETTEXTMODE (plain text, set before adding content): https://learn.microsoft.com/en-us/windows/win32/controls/em-settextmode
- Ollama chat API (streaming, thinking, and keep-alive parameters): https://docs.ollama.com/api/chat
- Ollama Qwen3 4B example model: https://ollama.com/library/qwen3:4b

No external source code from these projects is bundled as a TypeNext dependency. The Go runtime/standard library is statically linked into the executable; its license is provided separately. The Windows API bindings are implemented in this source tree.

## PowerPoint and caret tracking

- Microsoft, [AccessibleObjectFromWindow](https://learn.microsoft.com/en-us/windows/win32/api/oleacc/nf-oleacc-accessibleobjectfromwindow): native Office object-model access. The PowerPoint adapter supports the `paneClassDC` and `mdiClass` window classes.
- Microsoft, [Selection.TextRange](https://learn.microsoft.com/en-us/office/vba/api/powerpoint.selection.textrange): accessing the selected text range; TypeNext deliberately supports fewer views than the Office API permits.
- Microsoft, [TextRange.Characters](https://learn.microsoft.com/en-us/office/vba/api/powerpoint.textrange.characters): bounded character slices, including the API's out-of-range clamping behavior.
- Microsoft, [IUIAutomationTextRange::CompareEndpoints](https://learn.microsoft.com/en-us/windows/win32/api/uiautomationclient/nf-uiautomationclient-iuiautomationtextrange-compareendpoints): comparing logical range positions independently of screen geometry.

## Shortcut registration

Microsoft RegisterHotKey reference: https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-registerhotkey

This documents duplicate registration behavior, MOD_NOREPEAT, modifiers, and F12/Windows-key restrictions. TypeNext reports registration failures rather than trying to take over another application's key.

## Model APIs and key storage

- OpenAI Chat Completions request/stream schema, token budget and model-dependent parameter support: https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create
- OpenAI authentication (Bearer API key): https://developers.openai.com/api/reference/overview
- DeepSeek OpenAI-compatible API and endpoint: https://api-docs.deepseek.com/
- DeepSeek thinking/non-thinking request options: https://api-docs.deepseek.com/guides/thinking_mode/
- OpenRouter compatible API and streaming: https://openrouter.ai/docs/api_reference/overview
- Microsoft CryptProtectData (current-user DPAPI, additional entropy and allocated-buffer ownership): https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata
- Microsoft CryptUnprotectData: https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptunprotectdata

Provider presets are convenience defaults. Remote model IDs are supplied by the user and must be tested with their provider and account.
