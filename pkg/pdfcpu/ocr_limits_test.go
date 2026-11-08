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
	"image"
	"strings"
	"testing"

	"github.com/huzpsb/hspdf/pkg/pdfcpu/types"
)

// TestOCRImageToRGBDownscales verifies oversized images are downscaled to the
// OCR long side cap before being retained.
func TestOCRImageToRGBDownscales(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 3200, 100))
	w, h, rgb, err := ocrImageToRGB(img, ocrMaxImageDim)
	if err != nil {
		t.Fatal(err)
	}
	if w != ocrMaxImageDim {
		t.Errorf("width = %d, want %d", w, ocrMaxImageDim)
	}
	if h != 50 {
		t.Errorf("height = %d, want 50", h)
	}
	if len(rgb) != w*h*3 {
		t.Errorf("buffer length = %d, want %d", len(rgb), w*h*3)
	}
}

// TestOCRImageToRGBKeepsSmallImages verifies images within the cap keep
// their exact dimensions.
func TestOCRImageToRGBKeepsSmallImages(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 100, 50))
	w, h, rgb, err := ocrImageToRGB(img, ocrMaxImageDim)
	if err != nil {
		t.Fatal(err)
	}
	if w != 100 || h != 50 || len(rgb) != 100*50*3 {
		t.Fatalf("dims = %dx%d len = %d, want 100x50 len 15000", w, h, len(rgb))
	}
	// Gray 0 must map to black RGB.
	if rgb[0] != 0 || rgb[1] != 0 || rgb[2] != 0 {
		t.Fatalf("gray 0 mapped to %v", rgb[:3])
	}
}

// TestExtractOCRImagePixelCap verifies images above the OCR decode pixel cap
// are rejected before any decoding work.
func TestExtractOCRImagePixelCap(t *testing.T) {
	d := types.Dict{
		"Width":  types.Integer(10_000),
		"Height": types.Integer(10_000),
	}
	sd := &types.StreamDict{Dict: d}
	_, _, _, err := extractOCRImage(t.Context(), nil, sd, 1)
	if err == nil || !strings.Contains(err.Error(), "pixel decode limit") {
		t.Fatalf("want pixel decode limit error, got %v", err)
	}
}

// TestOCRJobRGBBudgetConstant documents the retention budget used by the
// planner (see planOCRPage).
func TestOCRJobRGBBudgetConstant(t *testing.T) {
	if ocrRGBBudget <= 0 {
		t.Fatalf("ocrRGBBudget = %d, want > 0", ocrRGBBudget)
	}
	if ocrMaxDecodePixels <= ocrMaxImageDim*ocrMaxImageDim {
		t.Fatalf("decode cap %d must exceed retained buffer cap %d",
			ocrMaxDecodePixels, ocrMaxImageDim*ocrMaxImageDim)
	}
}
