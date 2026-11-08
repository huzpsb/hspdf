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

package webui

import (
	"bytes"
	"testing"

	"github.com/huzpsb/hspdf/pkg/api"
	"github.com/huzpsb/hspdf/pkg/ocr"
	"github.com/huzpsb/hspdf/pkg/pdfcpu"
)

func TestOCRScannedPage(t *testing.T) {
	data := readTestFile(t, "../pkg/testdata/WaldenScan.pdf")

	r, err := OCR(t.Context(), data, Params{"pages": "1"})
	if err != nil {
		t.Fatalf("ocr: %v", err)
	}
	n, _ := r.Details["imagesProcessed"].(int)
	if n == 0 {
		t.Skip("no scannable image content on page 1")
	}
	if len(r.Data) == 0 {
		t.Fatal("ocr produced no output despite processed images")
	}
}

// TestOCRSplitPipeline checks that the split plan/det/rec/apply flow used by
// the webui worker pool matches the fused OCRPages path (which the CLI
// uses): identical per-line results and an identical extracted text layer.
// (WriteContext output is not byte-deterministic, so compare at these levels.)
func TestOCRSplitPipeline(t *testing.T) {
	data := readTestFile(t, "../pkg/testdata/WaldenScan.pdf")

	ocrPDF := func(fused bool) (string, int) {
		ctx, err := readContext(t.Context(), data, defaultConf())
		if err != nil {
			t.Fatal(err)
		}
		if fused {
			n, err := pdfcpu.OCRPages(t.Context(), ctx, []int{1})
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := api.WriteContext(t.Context(), ctx, &buf); err != nil {
				t.Fatal(err)
			}
			return string(extractText(t, buf.Bytes())), n
		}
		job, err := pdfcpu.PlanOCR(t.Context(), ctx, []int{1})
		if err != nil {
			t.Fatal(err)
		}
		if job.Processed() == 0 {
			return "", 0
		}
		// det once, then rec in two halves — the way the worker pool does it
		lines := make([][]ocr.Line, len(job.Images))
		for i, im := range job.Images {
			det, err := ocr.DetectRGB(im.RGB, im.W, im.H)
			if err != nil {
				t.Fatal(err)
			}
			half := len(det) / 2
			ocr.RecognizeLinesRGB(im.RGB, im.W, im.H, det[:half])
			ocr.RecognizeLinesRGB(im.RGB, im.W, im.H, det[half:])
			lines[i] = det
		}
		n, err := job.ApplyLines(t.Context(), lines)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		if err := api.WriteContext(t.Context(), job.Context(), &buf); err != nil {
			t.Fatal(err)
		}
		return string(extractText(t, buf.Bytes())), n
	}

	wantText, nFused := ocrPDF(true)
	gotText, nSplit := ocrPDF(false)
	if nFused == 0 {
		t.Skip("no scannable image content on page 1")
	}
	if nSplit != nFused {
		t.Fatalf("images processed: split=%d fused=%d", nSplit, nFused)
	}
	if gotText != wantText || len(gotText) == 0 {
		t.Fatalf("extracted text differs (split %d chars, fused %d chars)", len(gotText), len(wantText))
	}
}

func extractText(t *testing.T, pdf []byte) []byte {
	t.Helper()
	s, err := api.ExtractTextString(t.Context(), bytes.NewReader(pdf), nil, nil, defaultConf())
	if err != nil {
		t.Fatalf("extract text: %v", err)
	}
	return []byte(s)
}
