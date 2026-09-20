//go:build windows && amd64

package uia

import "context"

// adjacentCaretPosition uses only cloned ranges from the focused textbox.
// Empty caret ranges often have no rectangle. The following glyph also covers
// a caret at the start of a field, where there is no preceding glyph to inspect.
func adjacentCaretPosition(ctx context.Context, caret *comObject) (x, y, height int32, ok bool) {
	return adjacentCaretPositionWithin(ctx, caret, nil)
}

func adjacentCaretPositionWithin(ctx context.Context, caret, boundary *comObject) (x, y, height int32, ok bool) {
	if rangeWithinBoundary(caret, boundary) != nil {
		return 0, 0, 0, false
	}
	for _, direction := range []struct{ endpoint, count int }{{0, -1}, {1, 1}} {
		if ctx.Err() != nil {
			return 0, 0, 0, false
		}
		r, err := cloneRange(caret)
		if err != nil {
			continue
		}
		var box rect
		if ctx.Err() == nil && moveEnd(r, direction.endpoint, direction.count) == nil && clampRangeToBoundary(r, boundary) == nil && ctx.Err() == nil {
			box, ok = rangeRect(r)
		}
		release(r)
		if ok && ctx.Err() == nil {
			x = box.Right
			if direction.count > 0 {
				x = box.Left
			}
			return x, box.Bottom, box.Bottom - box.Top, true
		}
	}
	return 0, 0, 0, false
}
