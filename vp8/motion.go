// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vp8

// This file implements motion vector decoding for inter-frame prediction.
// See RFC 6386 Section 17 for details.

// motionVector represents a motion vector with quarter-pixel precision.
type motionVector struct {
	x, y int16 // Quarter-pixel precision.
}

// mvZero is the zero motion vector.
var mvZero = motionVector{0, 0}

// defaultMVProb is the default motion vector probability table.
// RFC 6386 Section 17.2.
var defaultMVProb = [2][19]uint8{
	// Horizontal component probabilities.
	{162, 128, 225, 146, 172, 147, 214, 39, 156, 128, 129, 132, 75, 145, 178, 206, 239, 254, 254},
	// Vertical component probabilities.
	{164, 128, 204, 170, 119, 235, 140, 230, 228, 128, 130, 130, 74, 148, 180, 203, 236, 254, 254},
}

// mvUpdateProb is the probability of updating each MV probability.
// RFC 6386 Section 17.2.
var mvUpdateProb = [2][19]uint8{
	{237, 246, 253, 253, 254, 254, 254, 254, 254, 254, 254, 254, 254, 254, 250, 250, 252, 254, 254},
	{231, 243, 245, 253, 254, 254, 254, 254, 254, 254, 254, 254, 254, 254, 251, 251, 254, 254, 254},
}

// Indices into the MV probability table.
const (
	mvpIsShort    = 0
	mvpSign       = 1
	mvpShort      = 2 // indices 2-8 for short MV values 1-7
	mvpBits       = 9 // indices 9-18 for long MV bits
)

// parseMVProb parses the motion vector probability updates.
// RFC 6386 Section 17.2.
func (d *Decoder) parseMVProb() {
	for i := 0; i < 2; i++ {
		for j := 0; j < 19; j++ {
			if d.fp.readBit(mvUpdateProb[i][j]) {
				d.mvProb[i][j] = uint8(d.fp.readUint(uniformProb, 7)) << 1
				if d.mvProb[i][j] == 0 {
					d.mvProb[i][j] = 1
				}
			}
		}
	}
}

// readMVComponent reads a single motion vector component.
// RFC 6386 Section 17.1.
func (d *Decoder) readMVComponent(comp int) int16 {
	p := &d.mvProb[comp]

	// Is it a long or short MV?
	if d.fp.readBit(p[mvpIsShort]) {
		// Long MV: read 3 high bits and 7 low bits.
		var mag int16

		// Read bits 3-9 (high bits).
		for i := 0; i < 3; i++ {
			if d.fp.readBit(p[mvpBits+i]) {
				mag |= 1 << uint(9-i)
			}
		}

		// Read bits 0-6 (low bits), starting from bit 6.
		for i := 9; i > 3; i-- {
			if d.fp.readBit(p[mvpBits+i-3]) {
				mag |= 1 << uint(i-3)
			}
		}

		// Add 8 (minimum value for long MV).
		mag += 8

		// Read sign bit.
		if d.fp.readBit(p[mvpSign]) {
			return -mag
		}
		return mag
	}

	// Short MV: tree decode values 0-7.
	var mag int16
	if d.fp.readBit(p[mvpShort]) {
		// 4, 5, 6, or 7
		if d.fp.readBit(p[mvpShort+2]) {
			// 6 or 7
			if d.fp.readBit(p[mvpShort+4]) {
				mag = 7
			} else {
				mag = 6
			}
		} else {
			// 4 or 5
			if d.fp.readBit(p[mvpShort+3]) {
				mag = 5
			} else {
				mag = 4
			}
		}
	} else {
		// 0, 1, 2, or 3
		if d.fp.readBit(p[mvpShort+1]) {
			// 2 or 3
			if d.fp.readBit(p[mvpShort+5]) {
				mag = 3
			} else {
				mag = 2
			}
		} else {
			// 0 or 1
			if d.fp.readBit(p[mvpShort+6]) {
				mag = 1
			} else {
				mag = 0
			}
		}
	}

	// Read sign if mag != 0.
	if mag != 0 && d.fp.readBit(p[mvpSign]) {
		return -mag
	}
	return mag
}

// readMV reads a full motion vector (both components).
func (d *Decoder) readMV() motionVector {
	return motionVector{
		y: d.readMVComponent(0) * 4, // Convert to quarter-pixel.
		x: d.readMVComponent(1) * 4, // Convert to quarter-pixel.
	}
}

// addMV adds two motion vectors.
func addMV(a, b motionVector) motionVector {
	return motionVector{x: a.x + b.x, y: a.y + b.y}
}

// clampMV clamps a motion vector to valid range based on macroblock position.
func (d *Decoder) clampMV(mv motionVector, mbx, mby int) motionVector {
	// Calculate the valid range for the motion vector.
	// The reference block must remain within the frame plus some margin.
	margin := int16(16 * 4) // 16 pixels in quarter-pixel units

	minX := int16((-mbx*16 - 16) * 4) - margin
	maxX := int16((d.mbw-mbx)*16*4) + margin
	minY := int16((-mby*16 - 16) * 4) - margin
	maxY := int16((d.mbh-mby)*16*4) + margin

	if mv.x < minX {
		mv.x = minX
	} else if mv.x > maxX {
		mv.x = maxX
	}
	if mv.y < minY {
		mv.y = minY
	} else if mv.y > maxY {
		mv.y = maxY
	}
	return mv
}
