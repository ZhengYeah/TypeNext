package core

import (
	"strings"
	"testing"
)

func FuzzTextBounds(f *testing.F) {
	for _, s := range []string{"", "plain text", "😀中文e\u0301", "\xff\xfea\xff", "a\xef\xbf\xbdb", strings.Repeat("document text 中文😀 ", 100)} {
		for _, n := range []uint16{0, 1, 3, 32, 2000} {
			f.Add(s, n)
		}
	}
	f.Fuzz(func(t *testing.T, s string, limit uint16) {
		n := int(limit)
		wantHead, wantTail := s, s
		runes := []rune(s)
		if len(runes) > n {
			wantHead, wantTail = string(runes[:n]), string(runes[len(runes)-n:])
		}
		if got := Head(s, n); got != wantHead {
			t.Fatalf("Head(%q, %d) = %q, want %q", s, n, got, wantHead)
		}
		if got := Tail(s, n); got != wantTail {
			t.Fatalf("Tail(%q, %d) = %q, want %q", s, n, got, wantTail)
		}
	})
}

func TestCleanSuggestionLineAndControlBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, raw, suffix, want string }{
		{"CRLF pairs", "a\r\nb\r\nc", "", "a b c"},
		{"adjacent line endings", "a\r\r\n\n\rb", "", "a    b"},
		{"control between line endings", "a\r\x00\nb", "", "a  b"},
		{"Unicode line endings", " 中文\u2028😀\u2029text", "", " 中文 😀 text"},
		{"bidi controls", "a\u202a\u202b\u202c\u202d\u202eb", "", "ab"},
		{"malformed UTF-8", "a\xff\xfeb", "", "a\ufffd\ufffdb"},
		{"four-rune suffix", " continuation中文😀。", "中文😀。", " continuation"},
		{"short Unicode suffix", " continuation中😀。", "中😀。", " continuation中😀。"},
		{"fence after controls", "\x00\t```text", "", ""},
		{"only controls and whitespace", "\x00\r\n\t\u2028\u202e", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CleanSuggestion(tc.raw, TextContext{Suffix: tc.suffix}, 240); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

var benchmarkTextResult string
var benchmarkFingerprintResult [32]byte

func BenchmarkTextBounds(b *testing.B) {
	for _, tc := range []struct {
		name string
		text string
	}{
		{"large_ASCII", strings.Repeat("document text ", 20000)},
		{"large_Unicode", strings.Repeat("document 中文😀 ", 20000)},
		{"bounded_Unicode", "document 中文😀"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			for _, op := range []struct {
				name string
				fn   func(string, int) string
			}{{"Head", Head}, {"Tail", Tail}} {
				b.Run(op.name, func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						benchmarkTextResult = op.fn(tc.text, 128)
					}
				})
			}
		})
	}
}

func BenchmarkTextFingerprint(b *testing.B) {
	t := TextContext{Window: 42, FocusID: "textbox", CaretID: "logical-caret", Prefix: strings.Repeat("document text 中文😀 ", 100), Suffix: strings.Repeat("following text ", 30)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkFingerprintResult = t.Fingerprint()
	}
}

func BenchmarkCleanSuggestion(b *testing.B) {
	t := TextContext{Prefix: "The document", Suffix: strings.Repeat("following text 中文😀 ", 30)}
	for _, tc := range []struct{ name, text string }{
		{"plain", strings.Repeat(" useful text 中文😀", 20)},
		{"controls", strings.Repeat(" useful\r\ntext\t中文😀\u202e", 20)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkTextResult = CleanSuggestion(tc.text, t, 240)
			}
		})
	}
}
