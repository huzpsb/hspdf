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
	"testing"
	"time"
)

// TestContentStreamDeduplicationScales verifies duplicate detection over
// many equal length but distinct content streams stays fast: the pre-fix
// length prefilter scanned the whole cache and ran one EqualObjects call per
// cached stream, which is quadratic in the number of content streams.
func TestContentStreamDeduplicationScales(t *testing.T) {
	const n = 5000
	ctx := testOptimizeContext(t)

	mustFinishPDFCPU(t, 60*time.Second, "content stream dedup scaling", func() {
		for i := 1; i <= n; i++ {
			// Same length for every stream, distinct bytes.
			sd := optimizeTestStream(nil, fmt.Sprintf("q 1 0 0 1 %06d 0 cm Q", i))
			ir, err := optimizeContentStreamUsage(t.Context(), ctx, sd, i)
			if err != nil {
				t.Errorf("stream %d: %v", i, err)
				return
			}
			if ir != nil {
				t.Errorf("distinct stream %d deduplicated to %v", i, ir)
				return
			}
		}
		if len(ctx.Optimize.ContentStreamCache) != n {
			t.Errorf("cache size = %d, want %d", len(ctx.Optimize.ContentStreamCache), n)
		}
	})
}

// TestContentStreamDeduplicationDuplicateStillFound verifies the hash index
// still detects true duplicates after many distinct streams.
func TestContentStreamDeduplicationDuplicateStillFound(t *testing.T) {
	ctx := testOptimizeContext(t)
	const n = 1000
	for i := 1; i <= n; i++ {
		sd := optimizeTestStream(nil, fmt.Sprintf("q 1 0 0 1 %06d 0 cm Q", i))
		if _, err := optimizeContentStreamUsage(t.Context(), ctx, sd, i); err != nil {
			t.Fatal(err)
		}
	}
	dup := optimizeTestStream(nil, "q 1 0 0 1 000042 0 cm Q")
	ir, err := optimizeContentStreamUsage(t.Context(), ctx, dup, n+1)
	if err != nil {
		t.Fatal(err)
	}
	if ir == nil || ir.ObjectNumber.Value() != 42 {
		t.Fatalf("duplicate not detected: %v", ir)
	}
}

// TestContentStreamHashesRebuiltFromCache verifies the lazily rebuilt index
// covers cache entries populated directly (e.g. by tests or after a reset).
func TestContentStreamHashesRebuiltFromCache(t *testing.T) {
	ctx := testOptimizeContext(t)
	ctx.Optimize.ContentStreamCache[100] = optimizeTestStream(nil, "q Q")
	if ctx.Optimize.ContentStreamHashes != nil {
		t.Fatal("precondition: index must start nil")
	}
	hashes := contentStreamHashes(ctx)
	total := 0
	for _, objNrs := range hashes {
		total += len(objNrs)
	}
	if total != 1 {
		t.Fatalf("index covers %d objects, want 1", total)
	}
}
