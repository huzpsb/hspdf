/*
Copyright 2026 The pdfcpu Authors.

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
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"testing"

	"github.com/huzpsb/hspdf/pkg/pdfcpu/model"
	"github.com/huzpsb/hspdf/pkg/pdfcpu/types"
)

// TestXRefStreamEntryBudgetBoundsAmplification verifies the aggregate xref
// stream entry budget: at least the floor for tiny files, proportional to
// the input size, and capped by MaxObjectCount.
func TestXRefStreamEntryBudgetBoundsAmplification(t *testing.T) {
	conf := model.NewDefaultConfiguration()
	conf.Limits.MaxObjectCount = 10_000

	tiny, err := model.NewContext(bytes.NewReader(bytes.Repeat([]byte{'x'}, 100)), conf)
	if err != nil {
		t.Fatal(err)
	}
	if got := xRefStreamEntryBudget(tiny); got != 16*4096 {
		t.Fatalf("tiny file budget = %d, want the 4096 floor x16", got)
	}

	// A file below the MaxObjectCount density cap gets the file-size
	// proportional bound.
	mid, err := model.NewContext(bytes.NewReader(bytes.Repeat([]byte{'x'}, 1<<20)), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := xRefStreamEntryBudget(mid), int64(16*((1<<20)/2)); got != want {
		t.Fatalf("1MiB file budget = %d, want %d (file size / 2 x16)", got, want)
	}

	// A huge file is capped by MaxObjectCount (10_000 above).
	big, err := model.NewContext(bytes.NewReader(bytes.Repeat([]byte{'x'}, 1<<20)), conf)
	if err != nil {
		t.Fatal(err)
	}
	big.Read.FileSize = 1 << 40
	if got, want := xRefStreamEntryBudget(big), int64(16*10_000); got != want {
		t.Fatalf("huge file budget = %d, want the MaxObjectCount cap x16 = %d", got, want)
	}
}

// TestDeclaredXRefStreamEntries verifies the entry count derivation from
// /Index and /Size.
func TestDeclaredXRefStreamEntries(t *testing.T) {
	sd := &types.StreamDict{Dict: types.Dict{
		"Index": types.Array{types.Integer(0), types.Integer(6), types.Integer(100000), types.Integer(90000)},
	}}
	if got := declaredXRefStreamEntries(sd); got != 90006 {
		t.Fatalf("Index entries = %d, want 90006", got)
	}

	sd = &types.StreamDict{Dict: types.Dict{"Size": types.Integer(1234)}}
	if got := declaredXRefStreamEntries(sd); got != 1234 {
		t.Fatalf("Size entries = %d, want 1234", got)
	}

	sd = &types.StreamDict{Dict: types.Dict{}}
	if got := declaredXRefStreamEntries(sd); got != 0 {
		t.Fatalf("empty dict entries = %d, want 0", got)
	}
}

// xrefStreamBombPDF returns a small PDF whose single xref stream uses a
// compressed run of zero bytes to declare 90000 free entries (W [1 0 0],
// Index [0 6 100000 90000]). Every entry is one zero byte, so a ~2 KB file
// would previously populate a 90000 slot xref table.
func xrefStreamBombPDF(t *testing.T, bomb bool) []byte {
	t.Helper()

	// Decoded xref data: 6 type-1 entries for the real objects followed by
	// 90000 type-0 (free) entries. W = [1 0 0] -> one byte per entry.
	xref := make([]byte, 6+90000)
	for i := 0; i < 6; i++ {
		xref[i] = 0x01
	}
	if !bomb {
		xref = xref[:6]
	}
	var zbuf bytes.Buffer
	w := zlib.NewWriter(&zbuf)
	if _, err := w.Write(xref); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	xrefData := zbuf.Bytes()

	index := "0 6"
	size := 6
	if bomb {
		index = "0 6 100000 90000"
		size = 100006
	}

	var obj [][]byte
	obj = append(obj, nil)
	obj = append(obj, []byte("<< /Type /Catalog /Pages 2 0 R >>"))
	obj = append(obj, []byte("<< /Type /Pages /Kids [3 0 R] /Count 1 >>"))
	obj = append(obj, []byte("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> /Contents 4 0 R >>"))
	content := "q Q"
	obj = append(obj, []byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)))
	// Object 5 is the xref stream covering the real objects (and, for the
	// bomb, an additional range of declared free entries).
	obj = append(obj, []byte(fmt.Sprintf(
		"<< /Type /XRef /Size %d /Index [%s] /W [1 0 0] /Root 1 0 R /Length %d >>\nstream\n",
		size, index, len(xrefData))+string(xrefData)+"\nendstream"))

	var buf bytes.Buffer
	buf.WriteString("%PDF-1.5\n")
	offsets := make([]int, len(obj))
	for i := 1; i < len(obj); i++ {
		offsets[i] = buf.Len()
		buf.WriteString(fmt.Sprintf("%d 0 obj\n", i))
		buf.Write(obj[i])
		buf.WriteString("\nendobj\n")
	}
	startxref := offsets[5]
	fmt.Fprintf(&buf, "startxref\n%d\n%%%%EOF\n", startxref)
	return buf.Bytes()
}

// TestXRefStreamBombFreeOfEntriesReads is the positive control: the same
// crafted file without the declared free range parses into a 6 entry table,
// proving the bomb test exercises the entry budget and not the file shape.
func TestXRefStreamBombFreeOfEntriesReads(t *testing.T) {
	data := xrefStreamBombPDF(t, false)
	ctx, err := Read(context.Background(), bytes.NewReader(data), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatalf("honest xref stream PDF must read: %v", err)
	}
	if got := len(ctx.XRefTable.Table); got > 1000 {
		t.Fatalf("table holds %d entries, want the 6 real objects", got)
	}
}

// TestXRefStreamEntryBudgetRejectsBomb verifies that the oversized xref
// stream is charged against the aggregate budget instead of materializing
// its entries: parsing either fails or repairs, but the table is never
// populated with the declared free entries.
func TestXRefStreamEntryBudgetRejectsBomb(t *testing.T) {
	data := xrefStreamBombPDF(t, true)
	if len(data) > 8192 {
		t.Fatalf("bomb PDF is %d bytes, want a small file", len(data))
	}

	for _, mode := range []int{model.ValidationRelaxed, model.ValidationStrict} {
		conf := model.NewDefaultConfiguration()
		conf.ValidationMode = mode
		ctx, err := Read(context.Background(), bytes.NewReader(data), conf)
		if err == nil {
			t.Logf("mode %d: repaired, table entries = %d", mode, len(ctx.XRefTable.Table))
			if got := len(ctx.XRefTable.Table); got > 1000 {
				t.Fatalf("mode %d: table holds %d entries despite the budget", mode, got)
			}
			continue
		}
		t.Logf("mode %d: rejected: %v", mode, err)
		// A rejected file must fail for the budget itself or for the repair
		// path it triggers, never silently load the declared entries.
		if ctx != nil && len(ctx.XRefTable.Table) > 1000 {
			t.Fatalf("mode %d: failing read still populated %d entries", mode, len(ctx.XRefTable.Table))
		}
	}
}
