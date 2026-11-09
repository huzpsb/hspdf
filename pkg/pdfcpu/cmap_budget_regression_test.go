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
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Regression tests for the ToUnicode CMap budgets of the text extractor: a
// bfrange entry copies its destination string once per mapped code, so the
// mapping count alone does not bound memory.

// mustFinishExtract fails the test if fn does not return in time.
// fn runs on its own goroutine: use t.Errorf (not Fatalf) inside fn.
func mustFinishExtract(t *testing.T, d time.Duration, name string, fn func()) {
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

// TestToUnicodeCMapAmplificationBounded verifies a bfrange with a large
// destination stops at the byte budget. Before the budget the same input
// (8KB) allocated 1.78GB and retained ~1.4GB.
func TestToUnicodeCMapAmplificationBounded(t *testing.T) {
	dst := strings.Repeat("0041", 2048)
	cmap := fmt.Sprintf("beginbfrange\n<0000> <FFFF> <%s>\nendbfrange\n", dst)
	mustFinishExtract(t, 5*time.Second, "parseToUnicodeCMap bfrange amplification", func() {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		cu, err := parseToUnicodeCMap([]byte(cmap))
		if err != nil {
			t.Errorf("parseToUnicodeCMap: %v", err)
			return
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		if retained := after.HeapAlloc - before.HeapAlloc; retained > 64<<20 {
			t.Errorf("retained %d bytes: amplification insufficiently bounded", retained)
		}
		// One entry of overshoot past the budget is accepted by design
		// (the check runs before the allocation).
		if cu.bytes > maxToUnicodeBytes+maxToUnicodeDstBytes {
			t.Errorf("byte budget exceeded: %d", cu.bytes)
		}
	})
}

// TestToUnicodeCMapHugeDestinationDropped verifies a single oversized
// destination is dropped instead of being multiplied.
func TestToUnicodeCMapHugeDestinationDropped(t *testing.T) {
	dst := strings.Repeat("0041", 5000) // 20000 bytes > maxToUnicodeDstBytes
	cmap := fmt.Sprintf("beginbfrange\n<0000> <FFFF> <%s>\nendbfrange\n", dst)
	cu, err := parseToUnicodeCMap([]byte(cmap))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cu.mappings); got != 0 {
		t.Fatalf("oversized destination must be dropped, got %d mappings", got)
	}
}

// TestToUnicodeCMapStreamSizeCap verifies oversized streams are rejected.
func TestToUnicodeCMapStreamSizeCap(t *testing.T) {
	if _, err := parseToUnicodeCMap(make([]byte, maxToUnicodeStreamBytes+1)); err == nil {
		t.Fatal("expected stream size cap error")
	}
}

// TestToUnicodeCMapNormalMappings verifies ordinary CMaps still decode.
func TestToUnicodeCMapNormalMappings(t *testing.T) {
	cmap := "begincodespacerange\n<00> <FF>\nendcodespacerange\n" +
		"beginbfchar\n<41> <0041>\n<42> <0042>\nendbfchar\n" +
		"beginbfrange\n<61> <7a> <0061>\nendbfrange\n"
	cu, err := parseToUnicodeCMap([]byte(cmap))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := cu.mappings[0x41]; !ok || s != "A" {
		t.Fatalf("bfchar mapping lost: %q", s)
	}
	if s, ok := cu.mappings[0x7a]; !ok || s != "z" {
		t.Fatalf("bfrange mapping lost: %q", s)
	}
	if cu.codeLen != 1 {
		t.Fatalf("codeLen = %d, want 1", cu.codeLen)
	}
}
