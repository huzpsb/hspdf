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
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"strings"
	"testing"
	"time"
)

// Regression tests for the resource budgets guarding the renderer against
// malformed input (dash patterns, font program interpreters, ToUnicode
// CMaps, content stream arrays and shading functions).

// ---- dashPath -------------------------------------------------------------

// TestDashPathDenormalPattern verifies that denormal dash lengths combined
// with a negative phase terminate: adding a denormal total to a negative
// phase leaves the float64 bit-identical, so the previous phase
// normalization loop never made progress.
func TestDashPathDenormalPattern(t *testing.T) {
	sps := []subPath{{pts: []pt{{0, 0}, {1000, 0}}}}
	tiny := math.Nextafter(0, 1)
	mustFinish(t, 3*time.Second, "dashPath denormal + negative phase", func() {
		_ = dashPath(sps, []float64{tiny, tiny}, -1)
	})
	mustFinish(t, 3*time.Second, "dashPath denormal entries", func() {
		_ = dashPath(sps, []float64{1e-300, 1e-300}, 0)
	})
	// NaN and Inf entries degrade to an undashed stroke.
	if got := dashPath(sps, []float64{math.NaN(), 1}, 0); len(got) != 1 {
		t.Errorf("NaN dash must fall back to the undashed path, got %d segments", len(got))
	}
	if got := dashPath(sps, []float64{math.Inf(1), 1}, 0); len(got) != 1 {
		t.Errorf("Inf dash must fall back to the undashed path, got %d segments", len(got))
	}
}

// TestDashPathNormal verifies normal patterns keep dashing, including a
// dotted [0 4] pattern with a negative phase.
func TestDashPathNormal(t *testing.T) {
	sps := []subPath{{pts: []pt{{0, 0}, {10, 0}}}}
	if got := dashPath(sps, []float64{3, 2}, 0); len(got) == 0 {
		t.Error("normal dash pattern produced no segments")
	}
	if got := dashPath(sps, []float64{0, 4}, -7); len(got) == 0 {
		t.Error("dotted pattern with negative phase produced no segments")
	}
}

// ---- CFF charstring interpreter -------------------------------------------

// cffSelfCallingSubr returns a font whose single glyph and single local subr
// both issue m self-calls: the interpreter work multiplies as ~m^depth and
// only the step budget bounds it.
func cffSelfCallingSubr(m int) *cffFont {
	subr := bytes.Repeat([]byte{32, 10}, m) // push(-107) + callsubr(self)
	cs := append(bytes.Repeat([]byte{32, 10}, m), 14)
	return &cffFont{charStrings: [][]byte{cs}, localSubrs: [][]byte{subr}, upem: 1000}
}

// TestCFFSubrStepBudget verifies self-calling subrs terminate quickly
// instead of multiplying into an unbounded interpreter run.
func TestCFFSubrStepBudget(t *testing.T) {
	for _, m := range []int{12, 20, 50} {
		cf := cffSelfCallingSubr(m)
		mustFinish(t, 2*time.Second, "cffGlyphOutline self-calling subr", func() {
			_ = cffGlyphOutline(cf, 0)
		})
	}
}

// TestCFFOutlinePointBudget verifies a charstring flooding the outline with
// points is truncated at maxCFFInterpPoints.
func TestCFFOutlinePointBudget(t *testing.T) {
	// push(1) followed by 199999 push(1)+hlineto pairs.
	prog := append([]byte{140}, bytes.Repeat([]byte{140, 6}, 199999)...)
	prog = append(prog, 14)
	cf := &cffFont{charStrings: [][]byte{prog}, upem: 1000}
	mustFinish(t, 3*time.Second, "cffGlyphOutline point flood", func() {
		sps := cffGlyphOutline(cf, 0)
		var pts int
		for _, sp := range sps {
			pts += len(sp.pts)
		}
		if pts > maxCFFInterpPoints+1000 {
			t.Errorf("outline points %d exceed budget %d", pts, maxCFFInterpPoints)
		}
	})
}

// ---- TTF composite glyphs -------------------------------------------------

// buildCompositeTTF returns a 9 glyph font where glyphs 0..7 are composites
// with m references each to their successor: resolving glyph 0 used to
// multiply as m^8 before memoization and the point budget.
func buildCompositeTTF(m int) *ttfFont {
	simple := []byte{
		0, 1, 0, 0, 0, 0, 0, 0, 0, 0, // 1 contour + bbox
		0, 2, 0, 0, // endPts (3 points), instruction length
		0x01, 0x01, 0x01, // on-curve flags, 2-byte deltas
		0, 0, 0, 10, 0, 20, // x deltas
		0, 0, 0, 10, 0, 0, // y deltas
	}
	glyphs := make([][]byte, 9)
	glyphs[8] = simple
	for g := 7; g >= 0; g-- {
		b := []byte{0xFF, 0xFF, 0, 0, 0, 0, 0, 0, 0, 0} // composite header
		for i := 0; i < m; i++ {
			// MORE_COMPONENTS | ARGS_ARE_XY_VALUES, index g+1, 1-byte args.
			b = append(b, 0x00, 0x22, byte((g+1)>>8), byte(g+1), 0, 0)
		}
		glyphs[g] = b
	}
	var glyf []byte
	loca := make([]byte, 20)
	off := 0
	for i, g := range glyphs {
		binary.BigEndian.PutUint16(loca[i*2:], uint16(off/2))
		glyf = append(glyf, g...)
		off += len(g)
	}
	binary.BigEndian.PutUint16(loca[18:], uint16(off/2))
	return &ttfFont{upem: 1000, numGlyphs: 9, locaFormat: 0, loca: loca, glyf: glyf}
}

// TestTTFCompositeGlyphBudget verifies chained composite references resolve
// quickly and within the point budget.
func TestTTFCompositeGlyphBudget(t *testing.T) {
	for _, m := range []int{18, 50, 100} {
		tf := buildCompositeTTF(m)
		mustFinish(t, 3*time.Second, "ttfGlyphOutline chained composites", func() {
			sps := ttfGlyphOutline(tf, 0, 0)
			var pts int
			for _, sp := range sps {
				pts += len(sp.pts)
			}
			if pts > maxTTFCompositePoints+1000 {
				t.Errorf("m=%d: outline points %d exceed budget %d", m, pts, maxTTFCompositePoints)
			}
		})
	}
}

// ---- ToUnicode CMap budgets ------------------------------------------------

// TestToUnicodeByteBudget verifies a bfrange with a large destination stops
// expanding at the byte budget instead of copying the destination 65536
// times. Destinations are stored as UTF-16BE hex strings (4 chars per unit).
func TestToUnicodeByteBudget(t *testing.T) {
	dst := strings.Repeat("0041", 1024) // 4096 chars: within the per-entry cap
	cmap := "beginbfrange\n<0000> <FFFF> <" + dst + ">\nendbfrange\n"
	mustFinish(t, 5*time.Second, "parseToUnicode byte budget", func() {
		tu, err := parseToUnicode([]byte(cmap))
		if err != nil {
			t.Errorf("parseToUnicode: %v", err)
			return
		}
		// Budget 16MB / ~4KB per entry: well below the 65536 code span.
		if got := len(tu.maps); got > 10000 {
			t.Errorf("byte budget did not stop the expansion: %d mappings", got)
		}
	})
}

// TestToUnicodeHugeDestinationDropped verifies a single oversized
// destination is dropped instead of being multiplied.
func TestToUnicodeHugeDestinationDropped(t *testing.T) {
	dst := strings.Repeat("0041", 5000) // 20000 chars > maxToUnicodeDstChars
	cmap := "beginbfrange\n<0000> <FFFF> <" + dst + ">\nendbfrange\n"
	tu, err := parseToUnicode([]byte(cmap))
	if err == nil && len(tu.maps) != 0 {
		t.Fatalf("oversized destination must be dropped, got %d mappings", len(tu.maps))
	}
}

// TestToUnicodeStreamSizeCap verifies oversized CMap streams are rejected.
func TestToUnicodeStreamSizeCap(t *testing.T) {
	if _, err := parseToUnicode(make([]byte, maxToUnicodeStreamBytes+1)); err == nil {
		t.Fatal("expected stream size cap error")
	}
}

// TestToUnicodeNormalMappings verifies ordinary CMaps still decode
// (destinations are stored as UTF-16BE hex strings).
func TestToUnicodeNormalMappings(t *testing.T) {
	cmap := "beginbfchar\n<0041> <0042>\nendbfchar\nbeginbfrange\n<0061> <007A> <0061>\nendbfrange\n"
	tu, err := parseToUnicode([]byte(cmap))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := tu.maps[0x41]; !ok || s != "0042" {
		t.Errorf("bfchar mapping lost: %q", s)
	}
	if s, ok := tu.maps[0x7A]; !ok || s != "007A" {
		t.Errorf("bfrange mapping lost: %q", s)
	}
}

// ---- content stream arrays --------------------------------------------------

// TestContentArrayFlood verifies unbalanced bracket runs and huge arrays
// neither hang nor accumulate unbounded token memory.
func TestContentArrayFlood(t *testing.T) {
	d := newDevice(context.Background(), nil, newCanvas(8, 8), ident())
	mustFinish(t, 5*time.Second, "execContent bracket flood", func() {
		if err := d.execContent(bytes.Repeat([]byte("[ "), 100000)); err != nil {
			t.Errorf("execContent: %v", err)
		}
	})
	d2 := newDevice(context.Background(), nil, newCanvas(8, 8), ident())
	mustFinish(t, 5*time.Second, "execContent element flood", func() {
		var sb strings.Builder
		sb.WriteString("[ ")
		for i := 0; i < 200000; i++ {
			sb.WriteString("0 ")
		}
		sb.WriteString("] TJ")
		if err := d2.execContent([]byte(sb.String())); err != nil {
			t.Errorf("execContent: %v", err)
		}
	})
}

// ---- shading ---------------------------------------------------------------

// TestShadingHugeCalculatorFunction verifies a maximal type 4 function is
// block sampled instead of being interpreted per pixel.
func TestShadingHugeCalculatorFunction(t *testing.T) {
	tokens := strings.Fields(strings.TrimRight(strings.Repeat("1 ", 1<<20), " "))
	fn := &pdfFunction{
		fnType:  4,
		prog:    parsePSNodes(tokens),
		nTokens: len(tokens),
		rng:     [][2]float64{{0, 1}, {0, 1}, {0, 1}},
	}
	d := newDevice(nil, nil, newCanvas(64, 64), ident())
	sh := &shading{shType: 1, fn: fn, cs: csGray}
	// The budget bounds the work to millions of token evaluations; without
	// block sampling this multiplies into per-pixel interpretation (minutes
	// to hours), so a generous wall clock still proves boundedness on slow
	// runners.
	mustFinish(t, 30*time.Second, "paintShading huge calculator function", func() {
		d.paintShading(sh, ident())
	})
}
