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

package pdfcpu

import (
	"strings"
	"testing"
	"time"

	"github.com/huzpsb/hspdf/pkg/pdfcpu/model"
	"github.com/huzpsb/hspdf/pkg/pdfcpu/types"
)

// scanAll drains a content scanner, failing on errors other than wantErr substrings.
func scanAll(t *testing.T, bb []byte) {
	t.Helper()
	sc := newContentScanner(bb)
	for {
		op, err := sc.next()
		if err != nil {
			t.Errorf("scanner error: %v", err)
			return
		}
		if op.isZero() {
			return
		}
	}
}

// TestContentScannerJunkRunsAreIterative verifies that runs of single byte
// junk delimiters and repeated dictionaries do not recurse per byte: the
// pre-fix implementation consumed them through self recursion and crashed
// with a fatal stack overflow on multi megabyte junk runs.
func TestContentScannerJunkRunsAreIterative(t *testing.T) {
	for _, tc := range []struct {
		name string
		bb   []byte
	}{
		{"closingBraces", []byte(strings.Repeat("}", 4_000_000))},
		{"closingParens", []byte(strings.Repeat(")", 4_000_000))},
		{"junkGreaterThan", []byte(strings.Repeat(">", 4_000_000))},
		{"procBraces", []byte(strings.Repeat("{", 4_000_000))},
		{"dicts", []byte(strings.Repeat("<<>>", 1_000_000))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 30s leaves generous headroom on slow CI machines while still
			// catching quadratic blowups (the pre-fix code needed minutes).
			mustFinishPDFCPU(t, 30*time.Second, tc.name, func() {
				scanAll(t, tc.bb)
			})
		})
	}
}

// TestContentScannerArrayNestingCapped verifies deep array nesting fails
// with an error instead of unbounded recursion.
func TestContentScannerArrayNestingCapped(t *testing.T) {
	sc := newContentScanner([]byte(strings.Repeat("[", maxArrayNesting+5)))
	for {
		op, err := sc.next()
		if err != nil {
			if !strings.Contains(err.Error(), "array nesting") {
				t.Fatalf("want array nesting error, got %v", err)
			}
			return
		}
		if op.isZero() {
			t.Fatal("want array nesting error, got end of input")
		}
	}
}

// TestContentScannerArrayNestingAllowed keeps legitimate shallow nesting working.
func TestContentScannerArrayNestingAllowed(t *testing.T) {
	sc := newContentScanner([]byte("[ [ (a) 1 ] [ /Name ] ]"))
	op, err := sc.next()
	if err != nil {
		t.Fatal(err)
	}
	if op.arr == nil {
		t.Fatalf("want array operand, got %v", op)
	}
	if len(op.arr) != 2 {
		t.Fatalf("want 2 inner arrays, got %d", len(op.arr))
	}
}

// TestParseCIDWidthsRangeCap verifies the W range form expansion is capped:
// a three element array must not expand to millions of map entries
// (the pre-fix loop inserted one entry per CID up to 4294967295).
func TestParseCIDWidthsRangeCap(t *testing.T) {
	conf := model.NewDefaultConfiguration()
	xrt := &model.XRefTable{Table: map[int]*model.XRefTableEntry{}, Conf: conf}
	e := &extractor{ctx: &model.Context{XRefTable: xrt, Configuration: conf}}

	t.Run("huge range is skipped", func(t *testing.T) {
		f := &textFont{cidWidths: map[uint32]float64{}}
		w := types.Array{types.Integer(0), types.Integer(4_000_000_000), types.Integer(500)}
		mustFinishPDFCPU(t, 10*time.Second, "huge W range", func() {
			if err := e.parseCIDWidths(w, f); err != nil {
				t.Errorf("parseCIDWidths: %v", err)
			}
		})
		if len(f.cidWidths) != 0 {
			t.Errorf("entries = %d, want 0 (span cap)", len(f.cidWidths))
		}
	})

	t.Run("cumulative entries are capped", func(t *testing.T) {
		f := &textFont{cidWidths: map[uint32]float64{}}
		// 20 ranges of maxCIDWidthSpan+1 CIDs each exceed the total cap.
		var w types.Array
		for k := 0; k < 20; k++ {
			base := k * (maxCIDWidthSpan + 1)
			w = append(w, types.Integer(base), types.Integer(base+maxCIDWidthSpan), types.Integer(500))
		}
		mustFinishPDFCPU(t, 30*time.Second, "cumulative W entries", func() {
			if err := e.parseCIDWidths(w, f); err != nil {
				t.Errorf("parseCIDWidths: %v", err)
			}
		})
		if len(f.cidWidths) > maxCIDWidthEntries {
			t.Errorf("entries = %d exceeds cap %d", len(f.cidWidths), maxCIDWidthEntries)
		}
		if len(f.cidWidths) < maxCIDWidthEntries/2 {
			t.Errorf("entries = %d, cap should bind near %d", len(f.cidWidths), maxCIDWidthEntries)
		}
	})

	t.Run("oversized span is skipped", func(t *testing.T) {
		f := &textFont{cidWidths: map[uint32]float64{}}
		w := types.Array{types.Integer(0), types.Integer(maxCIDWidthSpan + 1), types.Integer(500)}
		if err := e.parseCIDWidths(w, f); err != nil {
			t.Fatal(err)
		}
		if len(f.cidWidths) != 0 {
			t.Errorf("oversized span produced %d entries, want 0", len(f.cidWidths))
		}
	})

	t.Run("negative range is skipped", func(t *testing.T) {
		f := &textFont{cidWidths: map[uint32]float64{}}
		w := types.Array{types.Integer(-5), types.Integer(100), types.Integer(500)}
		if err := e.parseCIDWidths(w, f); err != nil {
			t.Fatal(err)
		}
		if len(f.cidWidths) != 0 {
			t.Errorf("negative range produced %d entries, want 0", len(f.cidWidths))
		}
	})

	t.Run("valid range is kept", func(t *testing.T) {
		f := &textFont{cidWidths: map[uint32]float64{}}
		w := types.Array{types.Integer(10), types.Integer(12), types.Integer(500)}
		if err := e.parseCIDWidths(w, f); err != nil {
			t.Fatal(err)
		}
		if len(f.cidWidths) != 3 || f.cidWidths[11] != 500 {
			t.Errorf("valid range expansion broken: %v", f.cidWidths)
		}
	})
}

// TestParseCIDWidthsArrayFormCap verifies the array form cannot blow past the
// total entry cap via a huge base CID.
func TestParseCIDWidthsArrayFormCap(t *testing.T) {
	conf := model.NewDefaultConfiguration()
	xrt := &model.XRefTable{Table: map[int]*model.XRefTableEntry{}, Conf: conf}
	e := &extractor{ctx: &model.Context{XRefTable: xrt, Configuration: conf}}
	f := &textFont{cidWidths: map[uint32]float64{}}
	inner := types.Array{types.Integer(500), types.Integer(600)}
	w := types.Array{types.Integer(4_300_000_000), inner}
	if err := e.parseCIDWidths(w, f); err != nil {
		t.Fatal(err)
	}
	if len(f.cidWidths) != 0 {
		t.Errorf("out of range base CID produced %d entries, want 0", len(f.cidWidths))
	}
}
