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
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"

	"github.com/huzpsb/hspdf/pkg/filter"
	"github.com/huzpsb/hspdf/pkg/pdfcpu/types"
)

// rasterImage is a decoded image ready for drawing.
type rasterImage struct {
	img     *image.NRGBA // full color image, nil for stencils
	stencil *image.Alpha // image mask
	w, h    int
}

// bufferBytes returns the approximate memory held by the decoded buffers.
func (ri *rasterImage) bufferBytes() int64 {
	if ri == nil {
		return 0
	}
	n := int64(ri.w) * int64(ri.h)
	total := n
	if ri.img != nil {
		total += n * 4
	}
	if ri.stencil != nil {
		total += n
	}
	return total
}

// pixelsWithinLimit reports whether w*h stays within limit. A limit <= 0
// disables the check; the division form avoids the int64 overflow of
// w*h for extreme dimensions.
func pixelsWithinLimit(w, h int64, limit int64) bool {
	if limit <= 0 {
		return true
	}
	if w <= 0 || h <= 0 {
		return false
	}
	return w <= limit/h
}

// bufferBytesWithinLimit reports whether w*h*bytesPerPixel stays within limit.
func bufferBytesWithinLimit(w, h, bytesPerPixel, limit int64) bool {
	if limit <= 0 {
		return true
	}
	if w <= 0 || h <= 0 || bytesPerPixel <= 0 {
		return false
	}
	if w > limit/bytesPerPixel {
		return false
	}
	return w*bytesPerPixel <= limit/h
}

// decodedRowBytes returns the byte length of one decoded image row and
// whether that length is representable (saturating arithmetic).
func decodedRowBytes(w, n, bpc int) (int64, bool) {
	if w < 0 || n < 0 || bpc < 0 {
		return 0, false
	}
	bits := int64(1)
	for _, v := range []int64{int64(w), int64(n), int64(bpc)} {
		if v > math.MaxInt64/bits {
			return 0, false
		}
		bits *= v
	}
	if bits > (math.MaxInt64-7)/8 {
		return 0, false
	}
	return (bits + 7) / 8, true
}

// lastFilterName returns the name of the last filter in the pipeline.
func lastFilterName(sd *types.StreamDict) string {
	if sd.FilterPipeline == nil || len(sd.FilterPipeline) == 0 {
		return ""
	}
	return sd.FilterPipeline[len(sd.FilterPipeline)-1].Name
}

// decodeImage decodes an image XObject or inline image stream into a
// rasterImage. csObj may be nil for image masks.
func (d *device) decodeImage(sd *types.StreamDict, csObj types.Object) (*rasterImage, error) {
	wv, _, err := d.ctx.DereferenceIntegerEntry(sd.Dict, "Width")
	if err != nil {
		return nil, err
	}
	hv, _, err := d.ctx.DereferenceIntegerEntry(sd.Dict, "Height")
	if err != nil {
		return nil, err
	}
	if wv == nil || hv == nil {
		return nil, errors.New("image: missing dimensions")
	}
	w, h := wv.Value(), hv.Value()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("image: invalid dimensions %dx%d", w, h)
	}
	if limit := d.maxImagePixels(); !pixelsWithinLimit(int64(w), int64(h), limit) {
		return nil, fmt.Errorf("image: %dx%d pixels exceeds limit of %d", w, h, limit)
	}

	imgMask := false
	if mv, _, err := d.ctx.DereferenceBooleanEntry(sd.Dict, "ImageMask"); err == nil && mv != nil {
		imgMask = mv.Value()
	}

	var bpc int
	if v, _, err := d.ctx.DereferenceIntegerEntry(sd.Dict, "BitsPerComponent"); err == nil && v != nil {
		bpc = v.Value()
	} else {
		bpc = 1 // image masks default to 1
	}
	if bpc != 1 && bpc != 2 && bpc != 4 && bpc != 8 && bpc != 16 {
		return nil, fmt.Errorf("image: invalid BitsPerComponent %d", bpc)
	}

	// Decode the raw data according to the filter pipeline.
	data, err := d.imageBytes(sd)
	if err != nil {
		return nil, err
	}

	ri := &rasterImage{w: w, h: h}

	lf := lastFilterName(sd)
	if lf == filter.DCT {
		im, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("image: dct: %w", err)
		}
		if ck, ok := im.(*image.CMYK); ok && hasAdobeMarker(data) {
			// Adobe CMYK JPEGs store the components inverted (255 = no ink).
			b := ck.Bounds()
			n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
			for y := b.Min.Y; y < b.Max.Y; y++ {
				for x := b.Min.X; x < b.Max.X; x++ {
					q := ck.CMYKAt(x, y)
					inv := color.CMYK{255 - q.C, 255 - q.M, 255 - q.Y, 255 - q.K}
					r, g, bb, _ := color.NRGBAModel.Convert(inv).RGBA()
					n.SetNRGBA(x-b.Min.X, y-b.Min.Y, color.NRGBA{uint8(r >> 8), uint8(g >> 8), uint8(bb >> 8), 255})
				}
			}
			ri.img = n
		} else {
			ri.img = toNRGBA(im)
		}
		if limit := d.maxImageBytes(); !bufferBytesWithinLimit(int64(w), int64(h), 4, limit) {
			return nil, fmt.Errorf("image: %dx%d exceeds %d byte buffer limit", w, h, limit)
		}
		if imgMask {
			ri.stencil = alphaFromNRGBA(ri.img)
			ri.img = nil
		}
		return ri, nil
	}
	if lf == filter.JPX || lf == filter.JBIG2 {
		return nil, fmt.Errorf("image: filter %s: %w", lf, errUnsupported)
	}

	// Color space and decode array.
	var cs *colorSpace
	if !imgMask {
		if csObj == nil {
			csObj = sd.Dict["ColorSpace"]
		}
		var err error
		cs, err = d.parseColorSpace(csObj)
		if err != nil {
			// Default to gray to stay robust.
			cs = csGray
		}
	} else {
		cs = csGray
	}
	decode := d.decodeArray(sd.Dict["Decode"], cs.n, imgMask)

	// The decoded row size implied by the declared dimensions must stay
	// within the image byte limit: corrupt entries (huge /ColorSpace
	// component counts combined with /Width) would otherwise overflow the
	// row arithmetic or allocate unbounded buffers. The raw data footprint
	// is rowBytes*h, the output buffers are bounded by the pixel limit.
	n := 1
	if cs != nil {
		n = cs.n
	}
	rowBytes, ok := decodedRowBytes(w, n, bpc)
	if !ok {
		return nil, fmt.Errorf("image: %dx%d bpc=%d n=%d exceeds size limits", w, h, bpc, n)
	}
	if limit := d.maxImageBytes(); limit > 0 && rowBytes > limit/int64(h) {
		return nil, fmt.Errorf("image: %dx%d bpc=%d n=%d exceeds %d byte data limit", w, h, bpc, n, limit)
	}

	if imgMask {
		ri.stencil = decodeStencil(data, w, h, bpc, decode)
		return ri, nil
	}

	ri.img = decodeRaster(data, w, h, bpc, cs, decode)
	return ri, nil
}

// hasAdobeMarker reports whether a JPEG stream carries the Adobe APP14
// marker, which denotes inverted CMYK/YCCK component storage.
func hasAdobeMarker(data []byte) bool {
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xFF {
			return false // desynchronized
		}
		marker := data[i+1]
		if marker == 0xDA { // start of scan: no APP14 before the data
			return false
		}
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if segLen < 2 {
			return false
		}
		if marker == 0xEE && i+2+segLen <= len(data) {
			payload := data[i+4 : i+2+segLen]
			if len(payload) >= 12 && string(payload[:5]) == "Adobe" {
				return true
			}
		}
		i += 2 + segLen
	}
	return false
}

// imageBytes returns the decoded byte stream of an image.
func (d *device) imageBytes(sd *types.StreamDict) ([]byte, error) {
	if sd.FilterPipeline == nil {
		if sd.Content == nil {
			return sd.Raw, nil
		}
		return sd.Content, nil
	}
	if err := sd.DecodeWithLimit(d.decodeLimit()); err != nil {
		return nil, fmt.Errorf("image: decode: %w", err)
	}
	return sd.Content, nil
}

// decodeArray resolves the /Decode array or returns the default.
func (d *device) decodeArray(o types.Object, n int, imgMask bool) [][2]float64 {
	def := make([][2]float64, n)
	for i := range def {
		def[i] = [2]float64{0, 1}
	}
	if imgMask {
		def[0] = [2]float64{0, 1}
	}
	a, err := d.ctx.DereferenceArray(o)
	if err != nil || a == nil {
		return def
	}
	var out [][2]float64
	for i := 0; i+1 < len(a); i += 2 {
		v0, err0 := d.ctx.DereferenceNumber(a[i])
		v1, err1 := d.ctx.DereferenceNumber(a[i+1])
		if err0 == nil && err1 == nil {
			out = append(out, [2]float64{v0, v1})
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

// decodeRaster converts raw samples into an NRGBA image.
func decodeRaster(data []byte, w, h, bpc int, cs *colorSpace, decode [][2]float64) *image.NRGBA {
	n := cs.n
	rowBytes, ok := decodedRowBytes(w, n, bpc)
	if !ok {
		return nil
	}
	if int64(h) > int64(len(data))/rowBytes {
		// Truncated stream: only decode (and allocate) the rows present.
		h = int(int64(len(data)) / rowBytes)
		if h <= 0 {
			return nil
		}
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	comps := make([]float64, n)
	maxV := float64(uint64(1)<<uint(bpc) - 1)
	for y := 0; y < h; y++ {
		off := int(int64(y) * rowBytes)
		if int64(off)+rowBytes > int64(len(data)) {
			break // truncated stream: stop at the last complete row
		}
		row := data[off:]
		for x := 0; x < w; x++ {
			ok := true
			for c := 0; c < n; c++ {
				bitOff := (x*n + c) * bpc
				v, valid := sampleBitsAt(row, bitOff, bpc)
				if !valid {
					ok = false
					break
				}
				f := v / maxV
				if c < len(decode) {
					dr := decode[c]
					f = dr[0] + f*(dr[1]-dr[0])
				}
				comps[c] = clamp(f, 0, 1)
			}
			if !ok {
				break
			}
			col := cs.resolve(comps)
			i := img.PixOffset(x, y)
			img.Pix[i] = col.r
			img.Pix[i+1] = col.g
			img.Pix[i+2] = col.b
			img.Pix[i+3] = 255
		}
	}
	return img
}

// sampleBitsAt reads the bpc-bit sample at the given bit offset.
func sampleBitsAt(b []byte, bitOff, bpc int) (float64, bool) {
	byteOff := bitOff / 8
	if byteOff >= len(b) {
		return 0, false
	}
	shift := uint(8 - bpc - bitOff%8)
	maxV := uint64(1)<<uint(bpc) - 1
	return float64(uint64(b[byteOff]>>shift) & maxV), true
}

// decodeStencil converts a 1 bit image mask into an alpha stencil where a
// sample of 1 means "masked out" (transparent).
func decodeStencil(data []byte, w, h, bpc int, decode [][2]float64) *image.Alpha {
	rowBytes := (w*bpc + 7) / 8
	if rowBytes > 0 {
		if rows := int64(len(data)) / int64(rowBytes); int64(h) > rows {
			// Truncated stream: only allocate the rows present.
			h = int(rows)
		}
	}
	if h <= 0 {
		return nil
	}
	m := image.NewAlpha(image.Rect(0, 0, w, h))
	inv := len(decode) > 0 && decode[0][0] == 1
	for y := 0; y < h; y++ {
		row := data[y*rowBytes:]
		if len(row) < rowBytes {
			break
		}
		for x := 0; x < w; x++ {
			v, _ := sampleBitsAt(row, x*bpc, bpc)
			opaque := v == 0
			if inv {
				opaque = !opaque
			}
			if opaque {
				m.Pix[m.PixOffset(x, y)] = 255
			}
		}
	}
	return m
}

// applySMask multiplies the alpha channel of img with the given soft mask.
// A soft mask is a luminance image: its gray value defines the opacity.
func applySMask(img *image.NRGBA, sm *rasterImage) {
	if img == nil || sm == nil || sm.img == nil && sm.stencil == nil {
		return
	}
	sw, sh := sm.w, sm.h
	if sw <= 0 || sh <= 0 {
		return
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		sy := y * sh / b.Dy()
		for x := b.Min.X; x < b.Max.X; x++ {
			sx := x * sw / b.Dx()
			var a uint32 = 255
			if sm.stencil != nil {
				a = uint32(sm.stencil.AlphaAt(sx, sy).A)
			} else {
				c := sm.img.NRGBAAt(sx, sy)
				a = (uint32(c.R) + uint32(c.G) + uint32(c.B)) / 3
			}
			i := img.PixOffset(x, y)
			img.Pix[i+3] = uint8(uint32(img.Pix[i+3]) * a / 255)
		}
	}
}

// applyMaskStencil applies a stencil /Mask (samples of 1 are transparent).
func applyMaskStencil(img *image.NRGBA, mk *rasterImage) {
	if img == nil || mk == nil || mk.stencil == nil {
		return
	}
	sw, sh := mk.w, mk.h
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		sy := y * sh / b.Dy()
		for x := b.Min.X; x < b.Max.X; x++ {
			sx := x * sw / b.Dx()
			if mk.stencil.AlphaAt(sx, sy).A == 0 {
				i := img.PixOffset(x, y)
				img.Pix[i+3] = 0
			}
		}
	}
}

// loadXObjectImage loads and caches an image XObject.
func (d *device) loadXObjectImage(objNr int, sd *types.StreamDict) (*rasterImage, error) {
	if ri, ok := d.images[objNr]; ok {
		return ri, nil
	}
	ri, err := d.decodeImage(sd, sd.Dict["ColorSpace"])
	if err != nil {
		return nil, err
	}
	// Soft mask / explicit mask.
	if ri.img != nil {
		if o, found := sd.Dict.Find("SMask"); found && o != nil {
			if smsd, _, err := d.ctx.DereferenceStreamDict(o); err == nil && smsd != nil {
				if sm, err := d.decodeImage(smsd, smsd.Dict["ColorSpace"]); err == nil {
					applySMask(ri.img, sm)
				}
			}
		}
		if o, found := sd.Dict.Find("Mask"); found && o != nil {
			if mksd, _, err := d.ctx.DereferenceStreamDict(o); err == nil && mksd != nil {
				if mk, err := d.decodeImage(mksd, mksd.Dict["ColorSpace"]); err == nil {
					if mk.stencil != nil {
						applyMaskStencil(ri.img, mk)
					} else if mk.img != nil {
						applySMask(ri.img, mk)
					}
				}
			}
		}
	}
	if size := ri.bufferBytes(); len(d.images) < 512 && d.imgBytes+size <= d.maxImageBytes() {
		d.images[objNr] = ri
		d.imgBytes += size
	}
	return ri, nil
}

// drawImagePlaceholder marks the unit square transformed by m as an image
// the renderer could not decode (JBIG2, JPEG2000).
func (d *device) drawImagePlaceholder(m mat) {
	rect := subPath{pts: []pt{{0, 0}, {1, 0}, {1, 1}, {0, 1}, {0, 0}}}
	sps := transformPath([]subPath{rect}, m)
	d.cv.fill(sps, false, rgba{168, 168, 176, 40}, 1, d.gs.clip)
	savedStroke := d.gs.strokeComps
	savedCS := d.gs.strokeCS
	savedAlpha := d.gs.strokeAlpha
	d.gs.strokeCS = csGray
	d.gs.strokeComps = []float64{0.62}
	d.gs.strokeAlpha = 1
	d.strokeDevice(sps, 1, 1)
	d.gs.strokeComps = savedStroke
	d.gs.strokeCS = savedCS
	d.gs.strokeAlpha = savedAlpha
}

// drawImage draws ri transformed by m (unit square -> device space).
func (d *device) drawImage(ri *rasterImage, m mat, alpha float64, stencilColor rgba) {
	if ri == nil || alpha <= 0 {
		return
	}
	// Device bbox of the transformed unit square.
	corners := []pt{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range corners {
		dx, dy := m.apply(c.x, c.y)
		x0 = math.Min(x0, dx)
		y0 = math.Min(y0, dy)
		x1 = math.Max(x1, dx)
		y1 = math.Max(y1, dy)
	}
	px0 := clampInt(int(math.Floor(x0)), 0, d.cv.w-1)
	py0 := clampInt(int(math.Floor(y0)), 0, d.cv.h-1)
	px1 := clampInt(int(math.Ceil(x1)), 0, d.cv.w)
	py1 := clampInt(int(math.Ceil(y1)), 0, d.cv.h)
	if px0 >= px1 || py0 >= py1 {
		return
	}
	inv, ok := m.invert()
	if !ok {
		return
	}
	// Source footprint per destination pixel for minification filtering.
	det := math.Abs(m.det())
	var fw, fh float64
	if det > 1e-12 {
		fw = float64(ri.w) * math.Sqrt(det)
		fh = fw
	}
	avg := fw > 1.5 || fh > 1.5
	samples := 2
	if fw > 3 || fh > 3 {
		samples = 3
	}

	for py := py0; py < py1; py++ {
		for px := px0; px < px1; px++ {
			u, v := inv.apply(float64(px)+0.5, float64(py)+0.5)
			if u < 0 || u >= 1 || v < 0 || v >= 1 {
				continue
			}
			a := alpha
			if d.gs.clip != nil {
				a *= float64(d.gs.clip.AlphaAt(px, py).A) / 255
			}
			if a <= 0.004 {
				continue
			}
			var c rgba
			if ri.stencil != nil {
				sa := sampleStencil(ri.stencil, u, v)
				if sa == 0 {
					continue
				}
				c = rgba{stencilColor.r, stencilColor.g, stencilColor.b, uint8(float64(stencilColor.a) * sa)}
			} else if avg {
				c = sampleArea(ri.img, u, v, fw, fh, samples)
			} else {
				c = sampleNRGBA(ri.img, u, v)
			}
			if c.a == 0 {
				continue
			}
			d.cv.blend(px, py, c, float64(c.a)/255*a)
		}
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// sampleNRGBA samples img at normalized position (u,v). v=0 is the BOTTOM of
// the image in PDF image space, which is the LAST row of the raster data.
func sampleNRGBA(img *image.NRGBA, u, v float64) rgba {
	if img == nil {
		return rgba{}
	}
	b := img.Bounds()
	x := clampInt(int(u*float64(b.Dx())), 0, b.Dx()-1)
	y := clampInt(int((1-v)*float64(b.Dy())), 0, b.Dy()-1)
	c := img.NRGBAAt(b.Min.X+x, b.Min.Y+y)
	return rgba{c.R, c.G, c.B, c.A}
}

// sampleArea samples a footprint around (u,v) averaging samples^2 points.
func sampleArea(img *image.NRGBA, u, v, fw, fh float64, n int) rgba {
	if img == nil {
		return rgba{}
	}
	var r, g, b, a, cnt uint32
	for j := 0; j < n; j++ {
		vv := v + (float64(j)+0.5)/float64(n)*fh/float64(img.Bounds().Dy()) - 0.5*fh/float64(img.Bounds().Dy())
		for i := 0; i < n; i++ {
			uu := u + (float64(i)+0.5)/float64(n)*fw/float64(img.Bounds().Dx()) - 0.5*fw/float64(img.Bounds().Dx())
			if uu < 0 || uu >= 1 || vv < 0 || vv >= 1 {
				continue
			}
			c := sampleNRGBA(img, uu, vv)
			r += uint32(c.r)
			g += uint32(c.g)
			b += uint32(c.b)
			a += uint32(c.a)
			cnt++
		}
	}
	if cnt == 0 {
		return sampleNRGBA(img, u, v)
	}
	n2 := cnt
	return rgba{uint8(r / n2), uint8(g / n2), uint8(b / n2), uint8(a / n2)}
}

// sampleStencil samples a stencil mask at normalized position (u,v) (v=0 at
// the bottom of the image space) and returns opacity in [0,1].
func sampleStencil(m *image.Alpha, u, v float64) float64 {
	if m == nil {
		return 0
	}
	b := m.Bounds()
	x := clampInt(int(u*float64(b.Dx())), 0, b.Dx()-1)
	y := clampInt(int((1-v)*float64(b.Dy())), 0, b.Dy()-1)
	return float64(m.AlphaAt(b.Min.X+x, b.Min.Y+y).A) / 255
}

func toNRGBA(im image.Image) *image.NRGBA {
	if n, ok := im.(*image.NRGBA); ok {
		return n
	}
	b := im.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := color.NRGBAModel.Convert(im.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
			out.SetNRGBA(x, y, c)
		}
	}
	return out
}

func alphaFromNRGBA(img *image.NRGBA) *image.Alpha {
	b := img.Bounds()
	m := image.NewAlpha(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			m.SetAlpha(x, y, color.Alpha{A: img.NRGBAAt(x, y).A})
		}
	}
	return m
}
