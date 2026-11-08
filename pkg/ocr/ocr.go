// Copyright 2026 huzpsb.
// Use of this source code is governed by an Apache-2.0 license that can be found in the LICENSE file.

package ocr

import (
	"fmt"
	"image"
	"image/draw"
)

// Recognize detects and recognizes text lines in img.
func Recognize(img image.Image) ([]Line, error) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("ocr: empty image")
	}
	return RecognizeRGB(ImageToRGB(img), w, h)
}

// DetectRGB runs only the detection stage and returns lines with boxes
// filled in (Text empty). Splitting det from rec lets a host fan the
// recognition stage out across processes (e.g. web workers): rec lines are
// independent of each other, so any subset can be recognized separately.
func DetectRGB(rgb []uint8, w, h int) ([]Line, error) {
	p, err := getPipeline()
	if err != nil {
		return nil, err
	}
	return p.runDet(rgb, w, h), nil
}

// RecognizeLinesRGB runs only the recognition stage over lines previously
// returned by DetectRGB, filling Text and RecScore in place.
func RecognizeLinesRGB(rgb []uint8, w, h int, lines []Line) {
	p, err := getPipeline()
	if err != nil {
		return
	}
	p.runRec(rgb, w, h, lines)
}

// RecognizeRGB runs the full det+rec pipeline on an RGB8 image.
func RecognizeRGB(rgb []uint8, w, h int) ([]Line, error) {
	lines, err := DetectRGB(rgb, w, h)
	if err != nil {
		return nil, err
	}
	RecognizeLinesRGB(rgb, w, h, lines)
	return lines, nil
}

// ImageToRGB converts any image to tightly packed RGB8 (top-down).
func ImageToRGB(img image.Image) []uint8 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	out := make([]uint8, w*h*3)
	j := 0
	for i := 0; i < len(dst.Pix); i += 4 {
		out[j] = dst.Pix[i]
		out[j+1] = dst.Pix[i+1]
		out[j+2] = dst.Pix[i+2]
		j += 3
	}
	return out
}
