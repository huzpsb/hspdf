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
	"image"
	"image/jpeg"
	"strings"
	"testing"
	"time"

	"github.com/huzpsb/hspdf/pkg/filter"
	"github.com/huzpsb/hspdf/pkg/pdfcpu/model"
	"github.com/huzpsb/hspdf/pkg/pdfcpu/types"
)

// dctHeaderOnlyJPEG returns a truncated JPEG whose SOF header claims w x h
// but that carries no entropy data. The Go decoder allocates its frame
// buffers from the header before it fails, so such a stream must be rejected
// on its header dimensions alone.
func dctHeaderOnlyJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	b.Write([]byte{0xff, 0xd8}) // SOI

	sof := []byte{
		0x00, 0x0b, // length
		0x08,       // precision
		0x00, 0x00, // height
		0x00, 0x00, // width
		0x01, // nComp
		0x01, // component id
		0x11, // sampling
		0x00, // quant table
	}
	binary.BigEndian.PutUint16(sof[3:], uint16(h))
	binary.BigEndian.PutUint16(sof[5:], uint16(w))
	b.Write([]byte{0xff, 0xc0}) // SOF0
	b.Write(sof)

	b.Write([]byte{0xff, 0xdb, 0x00, 0x43, 0x00}) // DQT
	for i := 0; i < 64; i++ {
		b.WriteByte(0x01)
	}

	sos := []byte{
		0x00, 0x08, // length
		0x01, // ns
		0x01, // component
		0x00, // table selectors
		0x00, // Ss
		0x3f, // Se
		0x00, // Ah/Al
	}
	b.Write([]byte{0xff, 0xda}) // SOS
	b.Write(sos)
	return b.Bytes()
}

// newImageTestDevice returns a device wired to a fresh model context.
func newImageTestDevice(t *testing.T) *device {
	t.Helper()
	ctx, err := model.NewContext(bytes.NewReader([]byte("%PDF-1.7\n")), model.NewDefaultConfiguration())
	if err != nil {
		t.Fatal(err)
	}
	return newDevice(context.Background(), ctx, newCanvas(8, 8), ident())
}

func dctImageSD(raw []byte, w, h int) *types.StreamDict {
	return &types.StreamDict{
		Dict: types.Dict{
			"Type":             types.Name("XObject"),
			"Subtype":          types.Name("Image"),
			"Width":            types.Integer(w),
			"Height":           types.Integer(h),
			"BitsPerComponent": types.Integer(8),
			"ColorSpace":       types.Name("DeviceGray"),
		},
		Raw:            raw,
		FilterPipeline: []types.PDFFilter{{Name: filter.DCT}},
	}
}

// TestDecodeImageRejectsDCTHeaderBeyondLimits verifies that a JPEG whose
// internal header claims oversized dimensions is rejected before the decoder
// allocates its frame buffers. The dict may claim honest 4x4 dimensions.
func TestDecodeImageRejectsDCTHeaderBeyondLimits(t *testing.T) {
	d := newImageTestDevice(t)
	sd := dctImageSD(dctHeaderOnlyJPEG(t, 20000, 20000), 4, 4)

	_, err := d.decodeImage(sd, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("want dimension limit error, got %v", err)
	}
}

// TestDecodeImageAcceptsSmallDCTImage is the positive control: a real JPEG
// within the limits still decodes.
func TestDecodeImageAcceptsSmallDCTImage(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewGray(image.Rect(0, 0, 4, 4))
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	d := newImageTestDevice(t)
	ri, err := d.decodeImage(dctImageSD(buf.Bytes(), 4, 4), nil)
	if err != nil {
		t.Fatalf("small DCT image must decode: %v", err)
	}
	if ri.img == nil || ri.img.Bounds() != image.Rect(0, 0, 4, 4) {
		t.Fatalf("decoded bounds = %v, want 4x4", ri.img.Bounds())
	}
}

// solidRasterImage returns a fully opaque w x h raster.
func solidRasterImage(w, h int) *rasterImage {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	return &rasterImage{img: img, w: w, h: h}
}

// TestDrawImageHonorsCancellation verifies the per-row cancellation poll: a
// cancelled context aborts the pixel loop instead of painting the canvas.
func TestDrawImageHonorsCancellation(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()
	cv := newCanvas(64, 64)
	d := newDevice(c, nil, cv, ident())

	mustFinish(t, 5*time.Second, "drawImage cancelled", func() {
		d.drawImage(solidRasterImage(64, 64), mat{64, 0, 0, 64, 0, 0}, 1, rgba{255, 0, 0, 255})
	})
	// The loop must have returned before blending anything: the untouched
	// canvas is fully transparent.
	for i, px := range cv.img.Pix {
		if px != 0 {
			t.Fatalf("canvas pixel %d painted despite cancellation", i)
		}
	}
}

// TestDrawImageBudgetStopsRepeatedPainting verifies the cumulative draw
// budget: painting stops once the per-page pixel operation budget is
// exhausted and reports a warning.
func TestDrawImageBudgetStopsRepeatedPainting(t *testing.T) {
	cv := newCanvas(16, 16)
	d := newDevice(context.Background(), nil, cv, ident())
	d.imageDrawOpsLimit = 32

	m := mat{16, 0, 0, 16, 0, 0}
	ri := solidRasterImage(16, 16)
	col := rgba{0, 0, 0, 255}

	d.drawImage(ri, m, 1, col)
	if d.drawImageOps != 32 {
		t.Fatalf("draw ops = %d, want the budget cap 32", d.drawImageOps)
	}

	// A second draw on the exhausted budget returns without painting.
	painted := 0
	for i := 3; i < len(cv.img.Pix); i += 4 {
		if cv.img.Pix[i] != 0 {
			painted++
		}
	}
	d.drawImage(ri, m, 1, col)
	if d.drawImageOps != 32 {
		t.Fatalf("draw ops = %d after exhausted budget, want 32", d.drawImageOps)
	}
	painted2 := 0
	for i := 3; i < len(cv.img.Pix); i += 4 {
		if cv.img.Pix[i] != 0 {
			painted2++
		}
	}
	if painted2 != painted {
		t.Fatalf("second draw painted %d more pixels despite exhausted budget", painted2-painted)
	}
	if len(d.warnings) == 0 || !strings.Contains(d.warnings[0], "draw budget") {
		t.Fatalf("want draw budget warning, got %v", d.warnings)
	}
}
