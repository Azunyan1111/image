// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vp8

import "image"

// This file implements inter-frame prediction (motion compensation).
// See RFC 6386 Section 14 for details on subpixel interpolation.

// subpelFilter is the 6-tap Wiener filter for subpixel interpolation.
// RFC 6386 Section 14.4.
// Index is the fractional position (0-7), representing 0, 1/8, 2/8, ... 7/8.
var subpelFilter = [8][6]int16{
	{0, 0, 128, 0, 0, 0},     // 0/8 (integer position)
	{0, -6, 123, 12, -1, 0},  // 1/8
	{2, -11, 108, 36, -8, 1}, // 2/8 = 1/4
	{0, -9, 93, 50, -6, 0},   // 3/8
	{3, -16, 77, 77, -16, 3}, // 4/8 = 1/2
	{0, -6, 50, 93, -9, 0},   // 5/8
	{1, -8, 36, 108, -11, 2}, // 6/8 = 3/4
	{0, -1, 12, 123, -6, 0},  // 7/8
}

// bilinearFilter is used for chroma interpolation.
// Index is the fractional position (0-7).
var bilinearFilter = [8][2]int16{
	{128, 0},
	{112, 16},
	{96, 32},
	{80, 48},
	{64, 64},
	{48, 80},
	{32, 96},
	{16, 112},
}

// clip255 clips a value to the range [0, 255].
func clip255(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// interPredictLuma performs inter prediction for the 16x16 luma block.
// mv is in quarter-pixel units.
func (d *Decoder) interPredictLuma(mbx, mby int, ref *image.YCbCr, mv motionVector) {
	// Calculate the integer and fractional parts of the MV.
	// MV is in quarter-pixel units, so divide by 4 for integer, modulo 4 for fraction.
	baseX := mbx*16 + int(mv.x>>2)
	baseY := mby*16 + int(mv.y>>2)
	fracX := int(mv.x & 3)
	fracY := int(mv.y & 3)

	// Convert quarter-pixel fraction to eighth-pixel for filter lookup.
	// Quarter-pixel 0,1,2,3 maps to eighth-pixel 0,2,4,6.
	filterX := fracX * 2
	filterY := fracY * 2

	// Perform 2D subpixel interpolation using separable filters.
	// First apply horizontal filter to get intermediate values,
	// then apply vertical filter.

	// Intermediate buffer for horizontal filtering result.
	// We need extra rows for the vertical filter tap.
	var temp [21][16]int16

	// Horizontal filter: process 21 rows (16 + 5 extra for vertical taps).
	for row := -2; row < 19; row++ {
		srcY := baseY + row
		// Clamp srcY to valid range.
		if srcY < 0 {
			srcY = 0
		} else if srcY >= ref.Rect.Max.Y {
			srcY = ref.Rect.Max.Y - 1
		}

		for col := 0; col < 16; col++ {
			if filterX == 0 {
				// Integer horizontal position - just copy.
				srcX := baseX + col
				if srcX < 0 {
					srcX = 0
				} else if srcX >= ref.Rect.Max.X {
					srcX = ref.Rect.Max.X - 1
				}
				temp[row+2][col] = int16(ref.Y[srcY*ref.YStride+srcX]) << 7
			} else {
				// Apply 6-tap filter.
				var sum int16
				flt := subpelFilter[filterX]
				for t := 0; t < 6; t++ {
					srcX := baseX + col + t - 2
					if srcX < 0 {
						srcX = 0
					} else if srcX >= ref.Rect.Max.X {
						srcX = ref.Rect.Max.X - 1
					}
					sum += flt[t] * int16(ref.Y[srcY*ref.YStride+srcX])
				}
				temp[row+2][col] = sum
			}
		}
	}

	// Vertical filter: process 16x16 output.
	for row := 0; row < 16; row++ {
		for col := 0; col < 16; col++ {
			var val int
			if filterY == 0 {
				// Integer vertical position.
				val = int(temp[row+2][col] + 64) >> 7
			} else {
				// Apply 6-tap filter to intermediate values.
				var sum int
				flt := subpelFilter[filterY]
				for t := 0; t < 6; t++ {
					sum += int(flt[t]) * int(temp[row+t][col])
				}
				// Round and normalize.
				val = (sum + 8192) >> 14
			}
			// Store in ybr workspace.
			d.ybr[1+row][8+col] = clip255(val)
		}
	}
}

// interPredictChroma performs inter prediction for the 8x8 chroma blocks.
// mv is in quarter-pixel units for luma, we scale for chroma.
func (d *Decoder) interPredictChroma(mbx, mby int, ref *image.YCbCr, mv motionVector) {
	// For 4:2:0 subsampling, chroma is half the luma resolution.
	// The MV for chroma is derived from the luma MV.
	// We average the MV and scale it.
	chromaMV := motionVector{
		x: mv.x,
		y: mv.y,
	}

	// Calculate positions (chroma is 8x8, at half resolution).
	baseX := mbx*8 + int(chromaMV.x>>3)
	baseY := mby*8 + int(chromaMV.y>>3)
	fracX := int(chromaMV.x & 7)
	fracY := int(chromaMV.y & 7)

	// Process Cb and Cr planes.
	d.interPredictChromaPlane(baseX, baseY, fracX, fracY, ref.Cb, ref.CStride, 8)  // Cb -> ybr offset 8
	d.interPredictChromaPlane(baseX, baseY, fracX, fracY, ref.Cr, ref.CStride, 24) // Cr -> ybr offset 24
}

// interPredictChromaPlane performs bilinear interpolation for one chroma plane.
func (d *Decoder) interPredictChromaPlane(baseX, baseY, fracX, fracY int, plane []uint8, stride int, ybrXOffset int) {
	// Chroma uses bilinear interpolation (RFC 6386 Section 14.5).
	fltX := bilinearFilter[fracX]
	fltY := bilinearFilter[fracY]

	// Calculate plane dimensions.
	planeWidth := stride
	planeHeight := len(plane) / stride

	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			// Get source positions.
			x0 := baseX + col
			x1 := x0 + 1
			y0 := baseY + row
			y1 := y0 + 1

			// Clamp to valid range.
			if x0 < 0 {
				x0 = 0
			}
			if x0 >= planeWidth {
				x0 = planeWidth - 1
			}
			if x1 < 0 {
				x1 = 0
			}
			if x1 >= planeWidth {
				x1 = planeWidth - 1
			}
			if y0 < 0 {
				y0 = 0
			}
			if y0 >= planeHeight {
				y0 = planeHeight - 1
			}
			if y1 < 0 {
				y1 = 0
			}
			if y1 >= planeHeight {
				y1 = planeHeight - 1
			}

			// Get source pixels.
			p00 := int(plane[y0*stride+x0])
			p01 := int(plane[y0*stride+x1])
			p10 := int(plane[y1*stride+x0])
			p11 := int(plane[y1*stride+x1])

			// Bilinear interpolation.
			// First interpolate horizontally, then vertically.
			h0 := (p00*int(fltX[0]) + p01*int(fltX[1]) + 64) >> 7
			h1 := (p10*int(fltX[0]) + p11*int(fltX[1]) + 64) >> 7
			val := (h0*int(fltY[0]) + h1*int(fltY[1]) + 64) >> 7

			// Store in ybr workspace.
			d.ybr[17+row][ybrXOffset+col] = clip255(val)
		}
	}
}

// copyBlockFromRef copies a block from the reference frame without interpolation.
func (d *Decoder) copyBlockFromRef(mbx, mby int, ref *image.YCbCr) {
	d.copyBlockFromRefWithOffset(mbx, mby, ref, 0, 0)
}

// copyBlockFromRefWithOffset copies a block from the reference frame with an offset.
func (d *Decoder) copyBlockFromRefWithOffset(mbx, mby int, ref *image.YCbCr, offsetX, offsetY int) {
	// Copy luma (16x16).
	for row := 0; row < 16; row++ {
		srcY := mby*16 + row + offsetY
		if srcY < 0 {
			srcY = 0
		} else if srcY >= ref.Rect.Max.Y {
			srcY = ref.Rect.Max.Y - 1
		}
		for col := 0; col < 16; col++ {
			srcX := mbx*16 + col + offsetX
			if srcX < 0 {
				srcX = 0
			} else if srcX >= ref.Rect.Max.X {
				srcX = ref.Rect.Max.X - 1
			}
			d.ybr[1+row][8+col] = ref.Y[srcY*ref.YStride+srcX]
		}
	}

	// Copy chroma (8x8 each).
	chromaOffsetX := offsetX / 2
	chromaOffsetY := offsetY / 2
	for row := 0; row < 8; row++ {
		srcY := mby*8 + row + chromaOffsetY
		if srcY < 0 {
			srcY = 0
		} else if srcY >= ref.Rect.Max.Y/2 {
			srcY = ref.Rect.Max.Y/2 - 1
		}
		for col := 0; col < 8; col++ {
			srcX := mbx*8 + col + chromaOffsetX
			if srcX < 0 {
				srcX = 0
			} else if srcX >= ref.Rect.Max.X/2 {
				srcX = ref.Rect.Max.X/2 - 1
			}
			d.ybr[17+row][8+col] = ref.Cb[srcY*ref.CStride+srcX]
			d.ybr[17+row][24+col] = ref.Cr[srcY*ref.CStride+srcX]
		}
	}
}

// performInterPrediction performs motion-compensated prediction for a macroblock.
func (d *Decoder) performInterPrediction(mbx, mby int) {
	ref := d.getRefFrame(d.refFrame)
	if ref == nil {
		// No reference frame available, fill with default gray.
		for row := 0; row < 16; row++ {
			for col := 0; col < 16; col++ {
				d.ybr[1+row][8+col] = 128
			}
		}
		for row := 0; row < 8; row++ {
			for col := 0; col < 8; col++ {
				d.ybr[17+row][8+col] = 128
				d.ybr[17+row][24+col] = 128
			}
		}
		return
	}

	mv := d.mbMV

	// For zero MV, always copy from reference.
	if mv.x == 0 && mv.y == 0 {
		// Zero MV - simple copy.
		d.copyBlockFromRef(mbx, mby, ref)
	} else if mv.x&3 == 0 && mv.y&3 == 0 {
		// Integer MV (but non-zero) - simple copy with offset.
		d.copyBlockFromRefWithOffset(mbx, mby, ref, int(mv.x>>2), int(mv.y>>2))
	} else {
		// Subpixel interpolation required.
		d.interPredictLuma(mbx, mby, ref, mv)
		d.interPredictChroma(mbx, mby, ref, mv)
	}
}
