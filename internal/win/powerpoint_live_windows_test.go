//go:build windows && amd64

package win

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"typenext/internal/core"
	"unsafe"
)

var livePowerPoint = flag.Bool("typenext-live-powerpoint", false, "run the read-only live PowerPoint check")
var livePowerPointPane = flag.Uint64("typenext-live-pane", 0, "explicit HWND for a read-only native PowerPoint diagnostic")

// A separate diagnostic for a specifically identified PowerPoint pane, even
// when the shell/UI holding test output has focus. This never relaxes Capture's
// foreground checks, and is disabled unless the pane HWND is explicitly given.
func TestPowerPointLiveNativeRead(t *testing.T) {
	if *livePowerPointPane == 0 {
		t.Skip("explicit PowerPoint pane required")
	}
	pane := uintptr(*livePowerPointPane)
	_, process, err := processOf(pane)
	if err != nil || !strings.EqualFold(process, "powerpnt.exe") {
		t.Fatal("diagnostic target is not PowerPoint")
	}
	class := pptWindowClass(pane)
	if !strings.EqualFold(class, "mdiClass") && !strings.EqualFold(class, "paneClassDC") {
		t.Fatal("diagnostic target is not a PowerPoint native pane")
	}
	w, err := newUIA()
	if err != nil {
		t.Fatal(err)
	}
	defer close(w.jobs)
	results := make(chan error, 1)
	w.jobs <- func(_ *comObject) {
		var document *comObject
		hr, _, _ := pPPTAccessibleObjectFromWindow.Call(pane, 0xfffffff0, uintptr(unsafe.Pointer(&iidPPTDispatch)), uintptr(unsafe.Pointer(&document)))
		defer release(document)
		if failed(hr) || document == nil {
			results <- fmt.Errorf("native model unavailable: 0x%08x", uint32(hr))
			return
		}
		var previous core.TextContext
		for i := 0; i < 3; i++ {
			caret, err := pptSelection(document)
			if err != nil {
				results <- err
				return
			}
			prefix, suffix, err := pptContextText(caret, 1000, 200)
			snapshot := core.TextContext{CaretID: caret.identity(), Prefix: prefix, Suffix: suffix}
			caret.close()
			if err != nil {
				results <- err
				return
			}
			if i > 0 && snapshot.Fingerprint() != previous.Fingerprint() {
				results <- fmt.Errorf("native caret/context changed between reads")
				return
			}
			previous = snapshot
		}
		results <- nil
	}
	select {
	case err := <-results:
		if err != nil {
			t.Fatal(err)
		}
		t.Log("Live native model passed selection validation and three stable bounded reads; no text logged, sent, or inserted")
	case <-time.After(8 * time.Second):
		t.Fatal("native PowerPoint reader timed out")
	}
}

// Explicitly opt in with TYPENEXT_LIVE_POWERPOINT=1 and leave a text caret in
// PowerPoint. This uses the real reader, without printing text, contacting a
// model, selecting anything, or inserting input. Ordinary test runs skip it.
func TestPowerPointLiveRead(t *testing.T) {
	if !*livePowerPoint && os.Getenv("TYPENEXT_LIVE_POWERPOINT") != "1" {
		t.Skip("manual read-only PowerPoint check")
	}
	t.Log("Waiting for PowerPoint foreground focus; leave a blinking caret inside slide text")
	deadline := time.Now().Add(55 * time.Second)
	for {
		_, process, err := processOf(foreground())
		if err == nil && strings.EqualFold(process, "powerpnt.exe") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PowerPoint did not receive foreground focus")
		}
		time.Sleep(200 * time.Millisecond)
	}
	w, err := newUIA()
	if err != nil {
		t.Fatal(err)
	}
	defer close(w.jobs)
	var initial core.TextContext
	for i := 0; i < 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		capture, err := w.Capture(ctx, core.DefaultConfig(), 0)
		cancel()
		if err != nil {
			t.Fatalf("live capture %d: %v", i+1, err)
		}
		if capture.CaretID == "" || !strings.HasPrefix(capture.PositionSource, "PowerPoint") {
			t.Fatal("live read did not use the PowerPoint adapter with a logical caret")
		}
		if i == 0 {
			initial = capture
		} else if capture.Fingerprint() != initial.Fingerprint() {
			t.Fatal("live caret/context changed between reads")
		}
		if i < 2 {
			time.Sleep(300 * time.Millisecond)
		}
	}
	t.Logf("Live PowerPoint reader passed three stable captures (%d prefix / %d suffix characters); no text logged or inserted", len([]rune(initial.Prefix)), len([]rune(initial.Suffix)))
}
