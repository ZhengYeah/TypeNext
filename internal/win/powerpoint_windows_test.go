//go:build windows && amd64

package win

import (
	"crypto/sha256"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"
)

// A small actual IDispatch vtable exercises the Windows ABI, reverse argument
// order, VARIANT ownership, and the bounded native-object-model read path. It
// never attaches to PowerPoint or reads user documents.
type pptFakeDispatch struct {
	VTable *[96]uintptr
	values map[string]variant
	names  []string
	refs   int
	invoke func(string, uint16, []variant) (variant, uintptr)
	reads  []string
}

var pptFakeVTable = func() *[96]uintptr {
	v := new([96]uintptr)
	v[1] = syscall.NewCallback(func(o *pptFakeDispatch) uintptr {
		o.refs++
		return uintptr(o.refs)
	})
	v[2] = syscall.NewCallback(func(o *pptFakeDispatch) uintptr {
		o.refs--
		return uintptr(o.refs)
	})
	v[5] = syscall.NewCallback(func(o *pptFakeDispatch, iid uintptr, names **uint16, count, locale uintptr, id *int32) uintptr {
		if count != 1 {
			return 0x80020006
		}
		var units []uint16
		for p := *names; ; p = (*uint16)(unsafe.Add(unsafe.Pointer(p), 2)) {
			if *p == 0 {
				break
			}
			units = append(units, *p)
		}
		name := string(utf16.Decode(units))
		o.names = append(o.names, name)
		*id = int32(len(o.names))
		return 0
	})
	v[6] = syscall.NewCallback(func(o *pptFakeDispatch, id, iid, locale, flags uintptr, params *pptDispatchParams, result *variant, exception, argError uintptr) uintptr {
		name := o.names[int(id)-1]
		o.reads = append(o.reads, name)
		var args []variant
		if params.Count > 0 {
			args = unsafe.Slice(params.Args, params.Count)
		}
		if o.invoke != nil {
			value, hr := o.invoke(name, uint16(flags), args)
			if hr != 0x80020003 {
				*result = value
				return hr
			}
		}
		value, ok := o.values[name]
		if !ok {
			return 0x80020003
		}
		if value.VT == 9 {
			obj := *(**pptFakeDispatch)(unsafe.Pointer(&value.Data[0]))
			obj.refs++
		}
		*result = value
		return 0
	})
	return v
}()

//go:noinline
func pptFakeObject() *pptFakeDispatch {
	return &pptFakeDispatch{VTable: pptFakeVTable, values: map[string]variant{}, refs: 1}
}

func (o *pptFakeDispatch) com() *comObject { return (*comObject)(unsafe.Pointer(o)) }

func pptFakeObjectValue(o *pptFakeDispatch) variant {
	v := variant{VT: 9}
	*(**pptFakeDispatch)(unsafe.Pointer(&v.Data[0])) = o
	return v
}

func pptFakeString(s string) variant {
	units := utf16.Encode([]rune(s))
	var pointer uintptr
	if len(units) != 0 {
		pointer = uintptr(unsafe.Pointer(&units[0]))
	}
	b, _, _ := oleaut32.NewProc("SysAllocStringLen").Call(pointer, uintptr(len(units)))
	runtime.KeepAlive(units)
	v := variant{VT: 8}
	*(*uintptr)(unsafe.Pointer(&v.Data[0])) = b
	return v
}

type pptFakePresentation struct {
	document, pane, presentation, selection, caret, frame, text, shape, view, slide, shapes *pptFakeDispatch
	objects                                                                                 []*pptFakeDispatch
	spans                                                                                   [][2]int32
}

func newPPTFake(t *testing.T, text string, start int32) *pptFakePresentation {
	t.Helper()
	p := new(pptFakePresentation)
	for _, destination := range []**pptFakeDispatch{&p.document, &p.pane, &p.presentation, &p.selection, &p.caret, &p.frame, &p.text, &p.shape, &p.view, &p.slide, &p.shapes} {
		*destination = pptFakeObject()
		p.objects = append(p.objects, *destination)
	}
	p.document.values = map[string]variant{"Active": pptIntArg(-1), "ViewType": pptIntArg(9), "ActivePane": pptFakeObjectValue(p.pane), "Presentation": pptFakeObjectValue(p.presentation), "Selection": pptFakeObjectValue(p.selection), "View": pptFakeObjectValue(p.view)}
	p.pane.values["ViewType"] = pptIntArg(1)
	p.presentation.values["ReadOnly"] = pptIntArg(0)
	p.presentation.invoke = func(name string, flags uint16, args []variant) (variant, uintptr) {
		if name == "FullName" {
			return pptFakeString("C:\\Example.pptx"), 0
		}
		return variant{}, 0x80020003
	}
	p.selection.values = map[string]variant{"Type": pptIntArg(3), "TextRange": pptFakeObjectValue(p.caret), "ShapeRange": pptFakeObjectValue(p.shapes)}
	p.caret.values = map[string]variant{"Length": pptIntArg(0), "Start": pptIntArg(start), "Parent": pptFakeObjectValue(p.frame)}
	p.frame.values = map[string]variant{"TextRange": pptFakeObjectValue(p.text), "Parent": pptFakeObjectValue(p.shape)}
	p.shape.values["Id"] = pptIntArg(12)
	p.shape.values["Type"] = pptIntArg(17)
	p.shapes.values["Count"] = pptIntArg(1)
	p.shapes.values["Item"] = pptFakeObjectValue(p.shape)
	p.view.values["Slide"] = pptFakeObjectValue(p.slide)
	p.slide.values["SlideID"] = pptIntArg(256)
	units := utf16.Encode([]rune(text))
	p.text.values["Length"] = pptIntArg(int32(len(units)))
	p.text.invoke = func(name string, flags uint16, args []variant) (variant, uintptr) {
		if name == "Text" {
			t.Error("attempted to read the complete text frame")
			return variant{}, 0x80020003
		}
		if name != "Characters" {
			return variant{}, 0x80020003
		}
		if flags != 1 || len(args) != 2 || args[0].VT != 3 || args[1].VT != 3 {
			t.Error("Characters must be invoked with two VT_I4 method arguments")
			return variant{}, 0x80020003
		}
		length := *(*int32)(unsafe.Pointer(&args[0].Data[0]))
		from := *(*int32)(unsafe.Pointer(&args[1].Data[0]))
		p.spans = append(p.spans, [2]int32{from, length})
		if from < 1 || length <= 0 || int(from+length-1) > len(units) {
			t.Errorf("unbounded Characters(%d,%d)", from, length)
			return variant{}, 0x80020003
		}
		r := pptFakeObject()
		p.objects = append(p.objects, r)
		r.values = map[string]variant{"Start": pptIntArg(from), "Length": pptIntArg(length)}
		r.invoke = func(name string, flags uint16, args []variant) (variant, uintptr) {
			if name == "Text" {
				return pptFakeString(string(utf16.Decode(units[from-1 : from+length-1]))), 0
			}
			return variant{}, 0x80020003
		}
		r.refs++
		return pptFakeObjectValue(r), 0
	}
	t.Cleanup(func() {
		for _, object := range p.objects {
			if object.refs != 1 {
				t.Errorf("COM reference leak/overrelease: refs=%d reads=%v", object.refs, object.reads)
			}
		}
	})
	return p
}

func TestPowerPointBoundedContext(t *testing.T) {
	p := newPPTFake(t, strings.Repeat("a", 10000)+"hello世界"+strings.Repeat("z", 10000), 10008)
	caret, err := pptSelection(p.document.com())
	if err != nil {
		t.Fatal(err)
	}
	defer caret.close()
	prefix, suffix, err := pptContextText(caret, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if prefix != "llo世界" || suffix != "zzz" {
		t.Fatalf("prefix=%q suffix=%q", prefix, suffix)
	}
	if !reflect.DeepEqual(p.spans, [][2]int32{{9998, 10}, {10008, 6}}) {
		t.Fatalf("unexpected range requests: %v", p.spans)
	}
	if caret.identity() != fmt.Sprintf("presentation:%x/slide:256/shape:12/start:10008", sha256.Sum256([]byte("C:\\Example.pptx"))) {
		t.Fatal(caret.identity())
	}
}

func TestPowerPointContextEdgesAndUnicode(t *testing.T) {
	for _, tc := range []struct {
		text           string
		start          int32
		before, after  int
		prefix, suffix string
		spans          int
	}{
		{"", 1, 5, 5, "", "", 0},
		{"abc", 1, 5, 5, "", "abc", 1},
		{"abc", 4, 5, 5, "abc", "", 1},
		{"abc", 2, 5, 0, "a", "", 1},
		{"a😀界🚀z", 5, 2, 2, "😀界", "🚀z", 2},
	} {
		t.Run(fmt.Sprintf("%q/%d/%d", tc.text, tc.start, tc.after), func(t *testing.T) {
			p := newPPTFake(t, tc.text, tc.start)
			caret, err := pptSelection(p.document.com())
			if err != nil {
				t.Fatal(err)
			}
			defer caret.close()
			prefix, suffix, err := pptContextText(caret, tc.before, tc.after)
			if err != nil || prefix != tc.prefix || suffix != tc.suffix || len(p.spans) != tc.spans {
				t.Fatalf("got %q | %q spans=%v error=%v", prefix, suffix, p.spans, err)
			}
		})
	}
}

func TestPowerPointRejectsUnsafeSelectionsBeforeText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*pptFakePresentation)
	}{
		{"inactive window", func(p *pptFakePresentation) { p.document.values["Active"] = pptIntArg(0) }},
		{"slide sorter", func(p *pptFakePresentation) { p.document.values["ViewType"] = pptIntArg(7) }},
		{"notes pane", func(p *pptFakePresentation) { p.pane.values["ViewType"] = pptIntArg(3) }},
		{"read only", func(p *pptFakePresentation) { p.presentation.values["ReadOnly"] = pptIntArg(-1) }},
		{"unknown protection", func(p *pptFakePresentation) { delete(p.presentation.values, "ReadOnly") }},
		{"selected shape", func(p *pptFakePresentation) { p.selection.values["Type"] = pptIntArg(2) }},
		{"selected text", func(p *pptFakePresentation) { p.caret.values["Length"] = pptIntArg(2) }},
		{"invalid start", func(p *pptFakePresentation) { p.caret.values["Start"] = pptIntArg(0) }},
		{"past end", func(p *pptFakePresentation) { p.caret.values["Start"] = pptIntArg(20) }},
		{"missing shape identity", func(p *pptFakePresentation) { delete(p.shape.values, "Id") }},
		{"missing presentation identity", func(p *pptFakePresentation) { p.presentation.invoke = nil }},
		{"table", func(p *pptFakePresentation) { p.shape.values["Type"] = pptIntArg(19) }},
		{"group", func(p *pptFakePresentation) { p.shape.values["Type"] = pptIntArg(6) }},
		{"SmartArt", func(p *pptFakePresentation) { p.shape.values["Type"] = pptIntArg(24) }},
		{"chart", func(p *pptFakePresentation) { p.shape.values["Type"] = pptIntArg(3) }},
		{"multiple shapes", func(p *pptFakePresentation) { p.shapes.values["Count"] = pptIntArg(2) }},
		{"table cell sharing container ID", func(p *pptFakePresentation) {
			container := pptFakeObject()
			p.objects = append(p.objects, container)
			container.values["Id"] = p.shape.values["Id"]
			container.values["Type"] = pptIntArg(19)
			p.shapes.values["Item"] = pptFakeObjectValue(container)
		}},
		{"mismatched shape", func(p *pptFakePresentation) {
			different := pptFakeObject()
			p.objects = append(p.objects, different)
			different.values["Id"] = pptIntArg(13)
			p.shapes.values["Item"] = pptFakeObjectValue(different)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPPTFake(t, "example", 2)
			tc.change(p)
			caret, err := pptSelection(p.document.com())
			if caret != nil {
				caret.close()
			}
			if err == nil {
				t.Fatal("unsafe selection was accepted")
			}
			for _, object := range p.objects {
				for _, name := range object.reads {
					if name == "Text" || name == "Characters" {
						t.Fatal("read text before validating selection")
					}
				}
			}
		})
	}
}

func TestPowerPointCaretIdentitySeparatesPositions(t *testing.T) {
	original := pptCaret{start: 12, slideID: 256, shapeID: 3}
	for _, changed := range []pptCaret{{start: 13, slideID: 256, shapeID: 3}, {start: 12, slideID: 257, shapeID: 3}, {start: 12, slideID: 256, shapeID: 4}} {
		if original.identity() == changed.identity() {
			t.Fatal("distinct caret positions shared an identity")
		}
	}
	differentDocument := original
	differentDocument.presentationID = sha256.Sum256([]byte("another presentation"))
	if original.identity() == differentDocument.identity() {
		t.Fatal("different presentations shared an identity")
	}
}

func TestPowerPointDispatchABI(t *testing.T) {
	if unsafe.Sizeof(pptDispatchParams{}) != 24 || unsafe.Sizeof(pptException{}) != 64 {
		t.Fatalf("incorrect COM ABI: DISPPARAMS=%d EXCEPINFO=%d", unsafe.Sizeof(pptDispatchParams{}), unsafe.Sizeof(pptException{}))
	}
}

func TestPowerPointRejectsClampedRangeBeforeText(t *testing.T) {
	frame, returned := pptFakeObject(), pptFakeObject()
	frame.values["Characters"] = pptFakeObjectValue(returned)
	returned.values = map[string]variant{"Start": pptIntArg(4), "Length": pptIntArg(2)}
	if _, err := pptTextSpan(frame.com(), 5, 2); err == nil {
		t.Fatal("clamped range was accepted")
	}
	if !reflect.DeepEqual(returned.reads, []string{"Start"}) || returned.refs != 1 {
		t.Fatalf("unexpected reads/references: %v refs=%d", returned.reads, returned.refs)
	}
	returned.reads = nil
	returned.values["Start"] = pptIntArg(5)
	returned.values["Length"] = pptIntArg(1)
	if _, err := pptTextSpan(frame.com(), 5, 2); err == nil {
		t.Fatal("shortened range was accepted")
	}
	if !reflect.DeepEqual(returned.reads, []string{"Start", "Length"}) || returned.refs != 1 {
		t.Fatalf("unexpected reads/references: %v refs=%d", returned.reads, returned.refs)
	}
}

func TestPowerPointGeometryConvertsPointsToScreen(t *testing.T) {
	document, caret := pptFakeObject(), pptFakeObject()
	caret.values = map[string]variant{"BoundLeft": pptFloatArg(100.5), "BoundTop": pptFloatArg(50), "BoundHeight": pptFloatArg(12)}
	document.invoke = func(name string, flags uint16, args []variant) (variant, uintptr) {
		if flags != 1 || len(args) != 1 || args[0].VT != 4 {
			t.Error("screen conversion requires a VT_R4 method argument")
			return variant{}, 0x80020003
		}
		n := *(*float32)(unsafe.Pointer(&args[0].Data[0]))
		if name == "PointsToScreenPixelsX" {
			return pptFloatArg(n*2 + 10), 0
		}
		if name == "PointsToScreenPixelsY" {
			return pptFloatArg(n*2 + 20), 0
		}
		return variant{}, 0x80020003
	}
	x, y, height, ok := pptRangePosition(document.com(), caret.com())
	if !ok || x != 211 || y != 144 || height != 24 {
		t.Fatalf("unexpected screen position: %d,%d h=%d ok=%v", x, y, height, ok)
	}
	caret.values["BoundHeight"] = pptFloatArg(0)
	if _, _, _, ok := pptRangePosition(document.com(), caret.com()); ok {
		t.Fatal("empty bounding box must use the visual fallback")
	}
}
