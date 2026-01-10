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
	// Use arithmetic that handles negative MVs correctly.
	mvx := int(mv.x)
	mvy := int(mv.y)
	// For negative values, we need floor division and positive modulo.
	baseX := mbx*16 + (mvx >> 2)
	baseY := mby*16 + (mvy >> 2)
	fracX := mvx & 3
	fracY := mvy & 3
	if fracX < 0 {
		fracX += 4
		baseX--
	}
	if fracY < 0 {
		fracY += 4
		baseY--
	}

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
	mvx := int(mv.x)
	mvy := int(mv.y)

	// Calculate positions (chroma is 8x8, at half resolution).
	// MV is in quarter-pixel luma units, convert to eighth-pixel chroma units.
	baseX := mbx*8 + (mvx >> 3)
	baseY := mby*8 + (mvy >> 3)
	fracX := mvx & 7
	fracY := mvy & 7
	if fracX < 0 {
		fracX += 8
		baseX--
	}
	if fracY < 0 {
		fracY += 8
		baseY--
	}

	// Process Cb and Cr planes.
	// ybrBX=8, ybrBY=18 for Cb; ybrRX=24, ybrRY=18 for Cr.
	d.interPredictChromaPlane(baseX, baseY, fracX, fracY, ref.Cb, ref.CStride, 8, 18)  // Cb
	d.interPredictChromaPlane(baseX, baseY, fracX, fracY, ref.Cr, ref.CStride, 24, 18) // Cr
}

// interPredictChromaPlane performs bilinear interpolation for one chroma plane.
// ybrYOffset is the Y offset in the ybr workspace (18 for both Cb and Cr).
func (d *Decoder) interPredictChromaPlane(baseX, baseY, fracX, fracY int, plane []uint8, stride int, ybrXOffset, ybrYOffset int) {
	// Chroma uses bilinear interpolation (RFC 6386 Section 14.5).
	fltX := bilinearFilter[fracX]
	fltY := bilinearFilter[fracY]

	// Calculate plane dimensions from the reference frame.
	// Note: stride may be larger than actual width due to padding.
	planeHeight := len(plane) / stride

	for row := 0; row < 8; row++ {
		for col := 0; col < 8; col++ {
			// Get source positions.
			x0 := baseX + col
			x1 := x0 + 1
			y0 := baseY + row
			y1 := y0 + 1

			// Clamp to valid range.
			// Use stride for X bounds (conservative - actual width may be smaller).
			if x0 < 0 {
				x0 = 0
			}
			if x0 >= stride {
				x0 = stride - 1
			}
			if x1 < 0 {
				x1 = 0
			}
			if x1 >= stride {
				x1 = stride - 1
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

			// Store in ybr workspace at correct offset.
			d.ybr[ybrYOffset+row][ybrXOffset+col] = clip255(val)
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
	// ybrBY=18, ybrRY=18 (not 17!)
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
			d.ybr[18+row][8+col] = ref.Cb[srcY*ref.CStride+srcX]
			d.ybr[18+row][24+col] = ref.Cr[srcY*ref.CStride+srcX]
		}
	}
}

// performInterPrediction performs motion-compensated prediction for a macroblock.
func (d *Decoder) performInterPrediction(mbx, mby int) {
	ref := d.getRefFrame(d.refFrame)
	if ref == nil {
		// No reference frame available, fill with default gray.
		// ybrYY=1 for luma, ybrBY=ybrRY=18 for chroma.
		for row := 0; row < 16; row++ {
			for col := 0; col < 16; col++ {
				d.ybr[1+row][8+col] = 128
			}
		}
		for row := 0; row < 8; row++ {
			for col := 0; col < 8; col++ {
				d.ybr[18+row][8+col] = 128
				d.ybr[18+row][24+col] = 128
			}
		}
		return
	}

	// Check if SPLITMV mode.
	if d.mvMode == mvModeSplit {
		d.interPredictSplit(mbx, mby, ref)
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

// interPredictSplit performs inter prediction for SPLITMV mode.
// Each 4x4 luma block has its own MV.
func (d *Decoder) interPredictSplit(mbx, mby int, ref *image.YCbCr) {
	// Process each 4x4 luma block.
	for blockIdx := 0; blockIdx < 16; blockIdx++ {
		mv := d.subMV[blockIdx]
		blockRow := blockIdx / 4
		blockCol := blockIdx % 4

		// Base position for this 4x4 block.
		baseX := mbx*16 + blockCol*4
		baseY := mby*16 + blockRow*4

		d.interPredict4x4Luma(baseX, baseY, ref, mv, blockRow, blockCol)
	}

	// For chroma, compute average MV for each 8x8 chroma block.
	// Each 8x8 chroma block corresponds to an 8x8 luma region (4 sub-blocks).
	for chromaRow := 0; chromaRow < 2; chromaRow++ {
		for chromaCol := 0; chromaCol < 2; chromaCol++ {
			// Collect MVs from the 4 corresponding luma blocks.
			var sumX, sumY int
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					lumaBlockIdx := (chromaRow*2+dy)*4 + chromaCol*2 + dx
					sumX += int(d.subMV[lumaBlockIdx].x)
					sumY += int(d.subMV[lumaBlockIdx].y)
				}
			}
			// Average MV (with proper rounding).
			avgMV := motionVector{
				x: int16((sumX + 2) >> 2),
				y: int16((sumY + 2) >> 2),
			}

			// Base position for this 4x4 chroma block.
			chromaBaseX := mbx*8 + chromaCol*4
			chromaBaseY := mby*8 + chromaRow*4

			d.interPredict4x4Chroma(chromaBaseX, chromaBaseY, ref, avgMV, chromaRow, chromaCol)
		}
	}
}

// interPredict4x4Luma performs inter prediction for a single 4x4 luma block.
func (d *Decoder) interPredict4x4Luma(baseX, baseY int, ref *image.YCbCr, mv motionVector, blockRow, blockCol int) {
	mvx := int(mv.x)
	mvy := int(mv.y)

	// Calculate positions.
	srcBaseX := baseX + (mvx >> 2)
	srcBaseY := baseY + (mvy >> 2)
	fracX := mvx & 3
	fracY := mvy & 3
	if fracX < 0 {
		fracX += 4
		srcBaseX--
	}
	if fracY < 0 {
		fracY += 4
		srcBaseY--
	}

	filterX := fracX * 2
	filterY := fracY * 2

	// Destination in ybr workspace.
	dstY := 1 + blockRow*4
	dstX := 8 + blockCol*4

	if filterX == 0 && filterY == 0 {
		// Integer position - direct copy.
		for row := 0; row < 4; row++ {
			for col := 0; col < 4; col++ {
				srcY := srcBaseY + row
				srcX := srcBaseX + col
				if srcY < 0 {
					srcY = 0
				} else if srcY >= ref.Rect.Max.Y {
					srcY = ref.Rect.Max.Y - 1
				}
				if srcX < 0 {
					srcX = 0
				} else if srcX >= ref.Rect.Max.X {
					srcX = ref.Rect.Max.X - 1
				}
				d.ybr[dstY+row][dstX+col] = ref.Y[srcY*ref.YStride+srcX]
			}
		}
		return
	}

	// Subpixel interpolation for 4x4 block.
	var temp [9][4]int16 // 4+5 rows for vertical filtering

	// Horizontal filter.
	for row := -2; row < 7; row++ {
		srcY := srcBaseY + row
		if srcY < 0 {
			srcY = 0
		} else if srcY >= ref.Rect.Max.Y {
			srcY = ref.Rect.Max.Y - 1
		}

		for col := 0; col < 4; col++ {
			if filterX == 0 {
				srcX := srcBaseX + col
				if srcX < 0 {
					srcX = 0
				} else if srcX >= ref.Rect.Max.X {
					srcX = ref.Rect.Max.X - 1
				}
				temp[row+2][col] = int16(ref.Y[srcY*ref.YStride+srcX]) << 7
			} else {
				var sum int16
				flt := subpelFilter[filterX]
				for t := 0; t < 6; t++ {
					srcX := srcBaseX + col + t - 2
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

	// Vertical filter.
	for row := 0; row < 4; row++ {
		for col := 0; col < 4; col++ {
			var val int
			if filterY == 0 {
				val = int(temp[row+2][col]+64) >> 7
			} else {
				var sum int
				flt := subpelFilter[filterY]
				for t := 0; t < 6; t++ {
					sum += int(flt[t]) * int(temp[row+t][col])
				}
				val = (sum + 8192) >> 14
			}
			d.ybr[dstY+row][dstX+col] = clip255(val)
		}
	}
}

// interPredict4x4Chroma performs inter prediction for a 4x4 chroma block.
func (d *Decoder) interPredict4x4Chroma(baseX, baseY int, ref *image.YCbCr, mv motionVector, blockRow, blockCol int) {
	mvx := int(mv.x)
	mvy := int(mv.y)

	// Chroma MV is in luma quarter-pixels, convert to chroma eighth-pixels.
	srcBaseX := baseX + (mvx >> 3)
	srcBaseY := baseY + (mvy >> 3)
	fracX := mvx & 7
	fracY := mvy & 7
	if fracX < 0 {
		fracX += 8
		srcBaseX--
	}
	if fracY < 0 {
		fracY += 8
		srcBaseY--
	}

	fltX := bilinearFilter[fracX]
	fltY := bilinearFilter[fracY]

	planeHeight := len(ref.Cb) / ref.CStride

	// Destination in ybr workspace.
	// Cb at row 18, col 8; Cr at row 18, col 24.
	dstCbY := 18 + blockRow*4
	dstCbX := 8 + blockCol*4
	dstCrY := 18 + blockRow*4
	dstCrX := 24 + blockCol*4

	for row := 0; row < 4; row++ {
		for col := 0; col < 4; col++ {
			x0 := srcBaseX + col
			x1 := x0 + 1
			y0 := srcBaseY + row
			y1 := y0 + 1

			// Clamp.
			if x0 < 0 {
				x0 = 0
			}
			if x0 >= ref.CStride {
				x0 = ref.CStride - 1
			}
			if x1 < 0 {
				x1 = 0
			}
			if x1 >= ref.CStride {
				x1 = ref.CStride - 1
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

			// Cb.
			p00 := int(ref.Cb[y0*ref.CStride+x0])
			p01 := int(ref.Cb[y0*ref.CStride+x1])
			p10 := int(ref.Cb[y1*ref.CStride+x0])
			p11 := int(ref.Cb[y1*ref.CStride+x1])
			h0 := (p00*int(fltX[0]) + p01*int(fltX[1]) + 64) >> 7
			h1 := (p10*int(fltX[0]) + p11*int(fltX[1]) + 64) >> 7
			val := (h0*int(fltY[0]) + h1*int(fltY[1]) + 64) >> 7
			d.ybr[dstCbY+row][dstCbX+col] = clip255(val)

			// Cr.
			p00 = int(ref.Cr[y0*ref.CStride+x0])
			p01 = int(ref.Cr[y0*ref.CStride+x1])
			p10 = int(ref.Cr[y1*ref.CStride+x0])
			p11 = int(ref.Cr[y1*ref.CStride+x1])
			h0 = (p00*int(fltX[0]) + p01*int(fltX[1]) + 64) >> 7
			h1 = (p10*int(fltX[0]) + p11*int(fltX[1]) + 64) >> 7
			val = (h0*int(fltY[0]) + h1*int(fltY[1]) + 64) >> 7
			d.ybr[dstCrY+row][dstCrX+col] = clip255(val)
		}
	}
}
