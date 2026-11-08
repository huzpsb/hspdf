/*
Copyright 2026 huzpsb.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package render

import (
	"strings"
	"testing"
	"time"
)

// mustFinish fails the test if fn does not return within the given duration.
// fn runs on its own goroutine: use t.Errorf (not Fatalf) inside fn.
func mustFinish(t *testing.T, d time.Duration, name string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Errorf("%s: did not finish within %s", name, d)
	}
}

func hex4(v int) string {
	const digits = "0123456789ABCDEF"
	v &= 0xFFFF
	return string([]byte{digits[v>>12&0xF], digits[v>>8&0xF], digits[v>>4&0xF], digits[v&0xF]})
}

// TestToUnicodeRangeArraySpanGuard verifies that an array form bfrange over
// the full 4 byte code space terminates quickly instead of iterating over
// 2^32 codes (previously only addRange carried the span guard).
func TestToUnicodeRangeArraySpanGuard(t *testing.T) {
	bb := []byte("1 beginbfrange\n<00000000> <FFFFFFFF> [ <0041> ]\nendbfrange\n")
	mustFinish(t, 5*time.Second, "parseToUnicode range array", func() {
		tu, err := parseToUnicode(bb)
		if err != nil {
			t.Errorf("parseToUnicode: %v", err)
			return
		}
		// The guarded fallback maps the base code only.
		if got := len(tu.maps); got != 1 {
			t.Errorf("expected 1 mapping from span guarded range, got %d", got)
		}
	})
}

// TestToUnicodeAggregateCap verifies that many 65536 wide bfranges cannot
// expand to millions of mappings (aggregate cap).
func TestToUnicodeAggregateCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("300 beginbfrange\n")
	for i := 0; i < 300; i++ {
		lo := i * 65536
		hi := lo + 65535
		sb.WriteString("<0000" + hex4(lo) + "> <0000" + hex4(hi) + "> <0041>\n")
	}
	sb.WriteString("endbfrange\n")
	mustFinish(t, 10*time.Second, "parseToUnicode aggregate cap", func() {
		tu, err := parseToUnicode([]byte(sb.String()))
		if err != nil {
			t.Errorf("parseToUnicode: %v", err)
			return
		}
		if got := len(tu.maps); got > maxToUnicodeMappings {
			t.Errorf("mapping count %d exceeds cap %d", got, maxToUnicodeMappings)
		}
	})
}

// TestEvalSampledInputDimensionCap verifies a sampled function with an
// excessive input dimension is rejected instead of iterating 2^nIn corners.
func TestEvalSampledInputDimensionCap(t *testing.T) {
	fn := &pdfFunction{fnType: 0, bps: 8}
	for i := 0; i < 40; i++ {
		fn.size = append(fn.size, 2)
		fn.domain = append(fn.domain, [2]float64{0, 1})
	}
	fn.rng = [][2]float64{{0, 1}}
	fn.samples = []byte{0}
	mustFinish(t, 5*time.Second, "evalSampled dimension cap", func() {
		if _, err := evalSampled(fn, make([]float64, 40)); err == nil {
			t.Errorf("expected dimension limit error")
		}
	})
}

// TestDecodeRasterTruncatedStream verifies a truncated image stream neither
// panics nor allocates the full declared image height.
func TestDecodeRasterTruncatedStream(t *testing.T) {
	mustFinish(t, 5*time.Second, "decodeRaster truncated", func() {
		got := decodeRaster([]byte("0123456789"), 1000, 1000, 8, csRGB, nil)
		// Less than one complete row: no decodable rows at all.
		if got != nil {
			t.Errorf("expected no image for sub row data, got %v", got.Bounds())
		}
	})
	// Exactly one complete row decodes exactly one row.
	got := decodeRaster(make([]byte, 3000), 1000, 1000, 8, csRGB, nil)
	if got == nil || got.Bounds().Dy() != 1 {
		t.Errorf("expected 1 decoded row, got %v", got)
	}
	if s := decodeStencil(make([]byte, 3), 24, 100, 1, nil); s == nil || s.Bounds().Dy() != 1 {
		t.Error("expected 1 stencil row")
	}
	if s := decodeStencil(nil, 24, 100, 1, nil); s != nil {
		t.Error("expected nil stencil for empty data")
	}
}

// TestPixelsWithinLimitOverflow verifies the overflow safe product checks.
func TestPixelsWithinLimitOverflow(t *testing.T) {
	const huge = int64(4) << 30 // product would overflow int64
	if pixelsWithinLimit(huge, huge, 100) {
		t.Error("huge dimensions must not pass the pixel limit")
	}
	if !pixelsWithinLimit(10, 10, 100) {
		t.Error("10x10 must pass a 100 pixel limit")
	}
	if pixelsWithinLimit(11, 10, 100) {
		t.Error("11x10 must not pass a 100 pixel limit")
	}
	if !pixelsWithinLimit(1, 1, 0) { // limit <= 0 disables
		t.Error("disabled limit must pass")
	}
	if bufferBytesWithinLimit(huge, huge, 4, 1<<20) {
		t.Error("huge buffer must not pass the byte limit")
	}
	if rb, ok := decodedRowBytes(1<<40, 1<<30, 16); ok || rb != 0 {
		t.Error("saturating row size must report overflow")
	}
}

// TestSortCrossingsLarge verifies the O(n log n) crossing sort on a size
// that used to be quadratic (100k crossings stalled the insertion sort).
func TestSortCrossingsLarge(t *testing.T) {
	const n = 100000
	x := make([]float64, n)
	d := make([]float64, n)
	for i := 0; i < n; i++ {
		x[i] = float64(n - i)
		d[i] = 1
	}
	mustFinish(t, 5*time.Second, "sortCrossings", func() {
		sortCrossings(x, d)
		for i := 1; i < n; i++ {
			if x[i-1] > x[i] {
				t.Errorf("not sorted at %d", i)
				return
			}
		}
	})
}

// TestRasterizeCancellation verifies the cancel hook aborts rasterization
// and leaves the coverage buffer clean.
func TestRasterizeCancellation(t *testing.T) {
	cv := newCanvas(64, 64)
	cv.cancel = func() bool { return true }
	sps := []subPath{{pts: []pt{{0, 0}, {64, 0}, {64, 64}, {0, 64}, {0, 0}}}}
	if _, ok := cv.rasterize(sps, false); ok {
		t.Fatal("rasterize must report cancellation")
	}
	for i, v := range cv.cov {
		if v != 0 {
			t.Fatalf("coverage buffer dirty at %d: %v", i, v)
		}
	}
}

// nonMonotonicEndsGlyph builds a simple glyf entry whose endPtsOfContours
// decrease (60000 followed by 3): deriving the point count from the last
// entry used to let contour 0 index far past the point arrays.
func nonMonotonicEndsGlyph() []byte {
	g := make([]byte, 28)
	g[1] = 2 // numberOfContours = 2
	// endPts: 60000, 3
	g[10], g[11] = 0xEA, 0x60 // 60000
	g[12], g[13] = 0x00, 0x03 // 3
	// instructionLength = 0 (g[14:16])
	for i := 16; i < 20; i++ {
		g[i] = 0x01 // flags: onCurve, 1 byte positive deltas
	}
	for i := 20; i < 28; i++ {
		g[i] = 1 // x/y deltas
	}
	return g
}

// TestSimpleGlyphOutlineNonMonotonicEnds verifies the degraded outline for
// a corrupt glyph instead of an out of range panic.
func TestSimpleGlyphOutlineNonMonotonicEnds(t *testing.T) {
	if out := simpleGlyphOutline(nonMonotonicEndsGlyph(), 2); out != nil {
		t.Fatalf("expected no outline, got %d subpaths", len(out))
	}
	// Also through the outline reader (adds the panic guard).
	tf := &ttfFont{
		numGlyphs:  1,
		locaFormat: 0,
		loca:       []byte{0x00, 0x00, 0x00, 0x0E}, // one glyph spanning 28 bytes
		glyf:       nonMonotonicEndsGlyph(),
	}
	if out := ttfGlyphOutline(tf, 0, 0); out != nil {
		t.Fatalf("expected no outline, got %d subpaths", len(out))
	}
}

// TestGroupCanvasBudget verifies nested group canvases are refused once the
// budget is exhausted.
func TestGroupCanvasBudget(t *testing.T) {
	d := &device{pageW: 4096, pageH: 4096}
	budget := d.auxCanvasBytes()
	need := int64(4096) * 4096 * 8
	if budget < need {
		t.Fatalf("budget %d smaller than one canvas %d", budget, need)
	}
	cv1, ok := d.acquireGroupCanvas(4096, 4096)
	if !ok {
		t.Fatal("first group canvas must fit the budget")
	}
	cv2, ok := d.acquireGroupCanvas(4096, 4096)
	if !ok {
		t.Fatal("second group canvas must fit the budget")
	}
	if _, ok := d.acquireGroupCanvas(4096, 4096); ok {
		t.Fatal("third group canvas must exceed the budget")
	}
	d.releaseGroupCanvas(cv2)
	if _, ok := d.acquireGroupCanvas(4096, 4096); !ok {
		t.Fatal("released budget must be reusable")
	}
	d.releaseGroupCanvas(cv1)
	d.releaseGroupCanvas(cv2)
	if d.groupDepth != 0 || d.groupBytes != 0 {
		t.Fatalf("budget not fully released: depth=%d bytes=%d", d.groupDepth, d.groupBytes)
	}
	// Depth cap independent of the byte budget: a page whose budget covers
	// more than maxGroupDepth minimal canvases still stops at the depth cap.
	small := &device{pageW: 16, pageH: 1}
	for i := 0; i < maxGroupDepth; i++ {
		if _, ok := small.acquireGroupCanvas(1, 1); !ok {
			t.Fatalf("group canvas %d within depth cap must be granted", i+1)
		}
	}
	if _, ok := small.acquireGroupCanvas(1, 1); ok {
		t.Fatal("depth cap must refuse further nesting")
	}
}

// TestPathBudgetTruncates verifies the path point budget caps path
// construction and resets at the painting operator.
func TestPathBudgetTruncates(t *testing.T) {
	cv := newCanvas(8, 8)
	d := newDevice(nil, nil, cv, ident())
	for i := 0; i < maxPathPoints+50; i++ {
		d.applyOperator("l", []token{{kind: tokNum, num: 1}, {kind: tokNum, num: 1}})
	}
	if d.pathPoints > maxPathPoints {
		t.Fatalf("path points %d exceed the cap", d.pathPoints)
	}
	if len(d.curPath) > maxPathPoints {
		t.Fatalf("current path holds %d points", len(d.curPath))
	}
	if len(d.warnings) == 0 {
		t.Fatal("expected a path limit warning")
	}
	d.applyOperator("f", nil)
	if d.pathPoints != 0 {
		t.Fatalf("painting must reset the point budget, got %d", d.pathPoints)
	}
}
