package core

import (
	"context"
	"net/http"
	"testing"
)

func TestSuggestionSpacing(t *testing.T) {
	tests := []struct{ name, prefix, raw, want string }{
		{"sentence", "All done.", "Next sentence.", " Next sentence."},
		{"comma", "Hello,", "world!", " world!"},
		{"colon", "Remember:", "check the result.", " check the result."},
		{"semicolon", "It works;", "keep going.", " keep going."},
		{"question", "Ready?", "Let's go.", " Let's go."},
		{"exclamation", "Great!", "Try again.", " Try again."},
		{"closing quote", "He said \"yes.\"", "Then left.", " Then left."},
		{"closing parenthesis", "It works (usually)", "and is simple.", " and is simple."},
		{"accented Latin", "A café.", "Émile arrives.", " Émile arrives."},
		{"existing model space", "All done.", " Next sentence.", " Next sentence."},
		{"existing document space", "All done. ", "Next sentence.", "Next sentence."},
		{"line break", "All done.\n", "Next sentence.", "Next sentence."},
		{"nonbreaking space", "All done.\u00a0", "Next sentence.", "Next sentence."},
		{"word fragment", "hel", "lo", "lo"},
		{"word fragment and phrase", "This is usef", "ul for testing.", "ul for testing."},
		{"ambiguous word boundary", "This can", "help.", "help."},
		{"correct word boundary", "This can", " help.", " help."},
		{"punctuation continuation", "Hello", ", world!", ", world!"},
		{"opening parenthesis", "Hello (", "world)", "world)"},
		{"contraction", "don'", "t stop.", "t stop."},
		{"curly contraction", "don’", "t stop.", "t stop."},
		{"hyphen", "well-", "known", "known"},
		{"decimal", "The value is 3.", "14", "14"},
		{"time", "Meet at 12:", "30", "30"},
		{"URL", "Visit https://example.", "com", "com"},
		{"web address", "Visit www.example.", "com", "com"},
		{"email", "Email me@example.", "com", "com"},
		{"unfinished abbreviation", "Use e.", "g. a short example", "g. a short example"},
		{"unfinished initialism", "The U.", "S. team", "S. team"},
		{"unfinished dotted initialism", "The U.S.", "A. team", "A. team"},
		{"bare domain", "Visit example.", "com", "com"},
		{"bare domain and phrase", "Visit example.", "com for details.", "com for details."},
		{"member name", "fmt.", "Println", "Println"},
		{"member call", "fmt.", "Println()", "Println()"},
		{"unfinished first word", "Done.", "Next", "Next"},
		{"Chinese", "该方法可以", "保护用户隐私。", "保护用户隐私。"},
		{"Chinese punctuation", "完成。", "下一句。", "下一句。"},
		{"Chinese with ASCII punctuation", "完成.", "Next", "Next"},
		{"Chinese continuation", "Done.", "下一句。", "下一句。"},
		{"empty prefix", "", "Hello.", "Hello."},
		{"empty suggestion", "Done.", "", ""},
		{"reasoning removed", "Done.", "<think>private</think>Next.", " Next."},
		{"prefix echo removed", "Done.", "Done.Next.", " Next."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanSuggestion(tt.raw, TextContext{Prefix: tt.prefix}, 320)
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			// Insertion sanitizes the finished preview again without source context.
			if inserted := CleanSuggestion(got, TextContext{}, 320); inserted != got {
				t.Fatalf("insertion changed preview %q to %q", got, inserted)
			}
		})
	}
	if got := CleanSuggestion("Next sentence.", TextContext{Prefix: "Done."}, 5); got != " Next" {
		t.Fatalf("added space must count toward the length limit: %q", got)
	}
}

func TestCompletionSpacingMatchesPreview(t *testing.T) {
	for _, tt := range []struct{ name, provider, contentType, body string }{
		{"Ollama", "ollama", "application/x-ndjson", "{\"message\":{\"content\":\"Next\"},\"done\":false}\n{\"message\":{\"content\":\" sentence.\"},\"done\":true}\n"},
		{"compatible stream", "openai-compatible", "text/event-stream", "data: {\"choices\":[{\"delta\":{\"content\":\"Next\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" sentence.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"},
		{"compatible JSON", "openai-compatible", "application/json", `{"choices":[{"message":{"content":"Next sentence."},"finish_reason":"stop"}]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Provider = tt.provider
			client := NewClient()
			client.HTTP.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				return response(tt.contentType, tt.body), nil
			})
			var preview string
			got, err := client.Complete(context.Background(), cfg, TextContext{Prefix: "Done."}, func(partial string) {
				preview = partial
				if partial != "Next" && partial != " Next sentence." {
					t.Errorf("unexpected preview spacing: %q", partial)
				}
			})
			if err != nil || got != " Next sentence." || preview != got {
				t.Fatalf("result=%q, preview=%q, err=%v", got, preview, err)
			}
		})
	}
}
