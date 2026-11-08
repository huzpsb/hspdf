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
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// mustFinishPDFCPU fails the test if fn does not return within d. Use
// t.Errorf (not Fatalf) inside fn: it runs on its own goroutine.
func mustFinishPDFCPU(t *testing.T, d time.Duration, name string, fn func()) {
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

func hex4PDFCPU(v int) string {
	const digits = "0123456789ABCDEF"
	v &= 0xFFFF
	return string([]byte{digits[v>>12&0xF], digits[v>>8&0xF], digits[v>>4&0xF], digits[v&0xF]})
}

// TestToUnicodeCMapAggregateCap verifies that a ToUnicode stream whose
// bfranges expand to far more than maxToUnicodeMappings entries is capped
// instead of allocating hundreds of millions of map entries.
func TestToUnicodeCMapAggregateCap(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("/CIDInit /ProcSet findresource begin\n12 dict begin\nbegincmap\n")
	sb.WriteString("1 begincodespacerange\n<0000> <FFFF>\nendcodespacerange\n")
	for b := 0; b < 10; b++ {
		sb.WriteString("65536 beginbfrange\n")
		for i := 0; i < 300; i++ {
			lo := b*65536 + i*256
			hi := lo + 255
			sb.WriteString("<" + hex4PDFCPU(lo) + "> <" + hex4PDFCPU(hi) + "> <0041>\n")
		}
		sb.WriteString("endbfrange\n")
	}
	sb.WriteString("endcmap\nCMapName currentdict /CMap defineresource pop\nend\nend\n")
	mustFinishPDFCPU(t, 20*time.Second, "parseToUnicodeCMap aggregate cap", func() {
		cu, err := parseToUnicodeCMap([]byte(sb.String()))
		if err != nil {
			t.Errorf("parseToUnicodeCMap: %v", err)
			return
		}
		if got := len(cu.mappings); got > maxToUnicodeMappings {
			t.Errorf("mapping count %d exceeds cap %d", got, maxToUnicodeMappings)
		}
	})
}

// overlappingFormat12Cmap builds a cmap format 12 subtable whose groups all
// cover the full rune space: without the insertion cap this loop ran
// nGroups * 2^20.67 iterations.
func overlappingFormat12Cmap(nGroups int) []byte {
	b := make([]byte, 16+12*nGroups)
	binary.BigEndian.PutUint16(b[0:], 12)
	binary.BigEndian.PutUint32(b[4:], uint32(16+12*nGroups))
	binary.BigEndian.PutUint32(b[12:], uint32(nGroups))
	for g := 0; g < nGroups; g++ {
		e := b[16+12*g:]
		binary.BigEndian.PutUint32(e[0:], 0)
		binary.BigEndian.PutUint32(e[4:], 0x10FFFF)
		binary.BigEndian.PutUint32(e[8:], 1)
	}
	return b
}

// TestParseCmapSubtableFormat12Cap verifies the total insertion cap for
// overlapping format 12 groups.
func TestParseCmapSubtableFormat12Cap(t *testing.T) {
	data := overlappingFormat12Cmap(20000)
	mustFinishPDFCPU(t, 20*time.Second, "parseCmapSubtable format 12 cap", func() {
		out := map[rune]uint16{}
		if err := parseCmapSubtable(data, out); err != nil {
			t.Errorf("parseCmapSubtable: %v", err)
			return
		}
		if got := len(out); got > maxCmapMappings {
			t.Errorf("mapping count %d exceeds cap %d", got, maxCmapMappings)
		}
	})
}
