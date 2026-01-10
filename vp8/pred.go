// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vp8

// This file implements parsing the predictor modes, as specified in chapter
// 11 (intra prediction) and chapter 16 (inter prediction).

// Inter prediction modes (RFC 6386 Section 16.1).
const (
	mvModeNearest = iota // Use nearest motion vector.
	mvModeNear           // Use near motion vector.
	mvModeZero           // Use zero motion vector.
	mvModeNew            // Read a new motion vector.
	mvModeSplit          // Split mode (4x4 sub-block MVs).
)

// Reference frame types.
const (
	refFrameIntra  = 0 // Intra prediction (no reference frame).
	refFrameLast   = 1 // Last decoded frame.
	refFrameGolden = 2 // Golden reference frame.
	refFrameAltRef = 3 // Alternate reference frame.
)

// Inter prediction probability tables (RFC 6386 Section 16.1).
// yModeProb is used to decode the macroblock-level Y mode for inter frames.
var yModeProb = [4]uint8{112, 86, 140, 37}

// uvModeProb is used to decode the chroma mode for inter frames.
var uvModeProb = [3]uint8{162, 101, 204}

// mbSegmentTreeProbs is the probability for segment tree.
var mbSegmentTreeProbs = [3]uint8{255, 255, 255}

// Inter-frame macroblock mode probabilities.
// mvRefProb[i] is the probability that the reference frame is not INTRA.
var mvRefProb = [4]uint8{
	0,   // Not used (index 0 is for when we have no context).
	120, // Default probability for P-frame with no left/above context.
	120,
	120,
}

// subMvRefProb is the probability table for sub-block MV modes in SPLITMV.
// Indexed by context (based on left/above MVs being zero or same).
var subMvRefProb = [5][3]uint8{
	{147, 136, 18}, // Context 0: Normal
	{106, 145, 1},  // Context 1: Left zero
	{179, 121, 1},  // Context 2: Above zero
	{223, 1, 34},   // Context 3: Both zero
	{208, 1, 1},    // Context 4: Same
}

// SPLITMV partition types.
const (
	splitMV16x8 = 0 // 2 horizontal 16x8 partitions
	splitMV8x16 = 1 // 2 vertical 8x16 partitions
	splitMV8x8  = 2 // 4 8x8 partitions
	splitMV4x4  = 3 // 16 4x4 partitions
)

// mbSplitProb is the probability table for SPLITMV partition type.
var mbSplitProb = [3]uint8{110, 111, 150}

// Sub-MV modes within SPLITMV.
const (
	subMVLeft   = 0 // Use left sub-block's MV
	subMVAbove  = 1 // Use above sub-block's MV
	subMVZero   = 2 // Zero MV
	subMVNew    = 3 // Read new MV
)

// mbSplitCount is the number of partitions for each split type.
var mbSplitCount = [4]int{2, 2, 4, 16}

// mbSplitFillCount is how many 4x4 blocks each partition covers.
var mbSplitFillCount = [4][16]int{
	{8, 8},             // 16x8: 2 partitions of 8 blocks each
	{8, 8},             // 8x16: 2 partitions of 8 blocks each
	{4, 4, 4, 4},       // 8x8: 4 partitions of 4 blocks each
	{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, // 4x4: 16 partitions
}

// mbSplitFillOffset defines which 4x4 blocks belong to each partition.
// Block indices in raster order: 0-3 top row, 4-7 second row, etc.
var mbSplitFillOffset = [4][16][16]int{
	// 16x8: top 8 blocks, bottom 8 blocks
	{{0, 1, 2, 3, 4, 5, 6, 7}, {8, 9, 10, 11, 12, 13, 14, 15}},
	// 8x16: left 8 blocks, right 8 blocks
	{{0, 1, 4, 5, 8, 9, 12, 13}, {2, 3, 6, 7, 10, 11, 14, 15}},
	// 8x8: four 8x8 quadrants
	{{0, 1, 4, 5}, {2, 3, 6, 7}, {8, 9, 12, 13}, {10, 11, 14, 15}},
	// 4x4: each block individually
	{{0}, {1}, {2}, {3}, {4}, {5}, {6}, {7}, {8}, {9}, {10}, {11}, {12}, {13}, {14}, {15}},
}

// mvModeProb is the probability table for motion vector modes.
// Indexed by [nearest_mv == 0][near_mv == 0].
var mvModeProb = [2][2][4]uint8{
	// nearest_mv != 0
	{
		// near_mv != 0
		{7, 1, 1, 143},
		// near_mv == 0
		{14, 18, 14, 107},
	},
	// nearest_mv == 0
	{
		// near_mv != 0
		{135, 145, 67, 106},
		// near_mv == 0
		{8, 75, 40, 155},
	},
}

// parseMBModeInter parses the macroblock mode for inter frames.
// Returns true if this is an inter-predicted macroblock, false for intra.
// RFC 6386 Section 16.1.
func (d *Decoder) parseMBModeInter(mbx, mby int) bool {
	// First, determine if this macroblock uses intra or inter prediction.
	// Use prob_intra from the frame header (RFC 6386 Section 9.10, 16.1).
	// prob_intra is the probability that the decoded bit is 1 (meaning INTRA).
	// If bit is 0 (readBit returns false with high probability when prob is low),
	// it means INTER prediction.
	if d.fp.readBit(d.probIntra) {
		// Bit is 1: Intra macroblock.
		d.isInterMB = false
		d.refFrame = refFrameIntra
		d.IntraMBCount++
		return false
	}

	// Bit is 0: Inter macroblock - determine the reference frame.
	d.isInterMB = true
	d.InterMBCount++
	d.refFrame = d.parseRefFrame()

	// Parse the motion vector mode.
	d.parseMVMode(mbx, mby)

	return true
}

// getInterModeContext returns the context for inter mode prediction.
func (d *Decoder) getInterModeContext(mbx, mby int) int {
	ctx := 0
	if mbx > 0 && d.leftRefFrame != refFrameIntra {
		ctx++
	}
	if mby > 0 && d.aboveRefFrame != refFrameIntra {
		ctx++
	}
	return ctx
}

// getRefFrameProb returns the probability for reference frame selection.
func (d *Decoder) getRefFrameProb(ctx int) uint8 {
	// Default probabilities based on context.
	switch ctx {
	case 0:
		return 145 // No inter neighbors.
	case 1:
		return 165 // One inter neighbor.
	default:
		return 190 // Both neighbors are inter.
	}
}

// parseRefFrame parses the reference frame for an inter macroblock.
// RFC 6386 Section 16.1 describes the tree structure.
// prob_last is P(bit=1), where bit=0 means LAST, bit=1 means GOLDEN or ALTREF.
// prob_gf is P(bit=1), where bit=0 means GOLDEN, bit=1 means ALTREF.
func (d *Decoder) parseRefFrame() uint8 {
	// Use prob_last from frame header.
	// If bit is 0 (readBit returns false), use LAST frame.
	if !d.fp.readBit(d.probLast) {
		return refFrameLast
	}
	// Bit is 1, so choose between GOLDEN and ALTREF.
	// Use prob_gf from frame header.
	// If bit is 0 (readBit returns false), use GOLDEN frame.
	if !d.fp.readBit(d.probGF) {
		return refFrameGolden
	}
	return refFrameAltRef
}

// parseMVMode parses the motion vector mode for an inter macroblock.
func (d *Decoder) parseMVMode(mbx, mby int) {
	// Find the nearest and near motion vectors.
	nearest, near := d.findBestMV(mbx, mby)


	// Determine probabilities based on MV candidates.
	nearestZero := nearest.x == 0 && nearest.y == 0
	nearZero := near.x == 0 && near.y == 0
	prob := mvModeProb[btou(nearestZero)][btou(nearZero)]

	// Parse the MV mode using the probability tree.
	// Tree structure from libvpx: ZEROMV, NEARESTMV, NEARMV, NEWMV, SPLITMV
	if !d.fp.readBit(prob[0]) {
		// ZEROMV
		d.mvMode = mvModeZero
		d.mbMV = mvZero
		d.MVModeCount[mvModeZero]++
	} else if !d.fp.readBit(prob[1]) {
		// NEARESTMV
		d.mvMode = mvModeNearest
		d.mbMV = d.clampMV(nearest, mbx, mby)
		d.MVModeCount[mvModeNearest]++
	} else if !d.fp.readBit(prob[2]) {
		// NEARMV
		d.mvMode = mvModeNear
		d.mbMV = d.clampMV(near, mbx, mby)
		d.MVModeCount[mvModeNear]++
	} else if !d.fp.readBit(prob[3]) {
		// NEWMV
		d.mvMode = mvModeNew
		// Read the new MV and add to the nearest MV.
		deltaMV := d.readMV()
		d.mbMV = d.clampMV(addMV(nearest, deltaMV), mbx, mby)
		d.MVModeCount[mvModeNew]++
	} else {
		// SPLITMV - each sub-block has its own MV.
		d.mvMode = mvModeSplit
		d.MVModeCount[mvModeSplit]++
		d.parseSplitMV(mbx, mby, nearest)
	}
}

// parseSplitMV parses the SPLITMV mode where sub-blocks have individual MVs.
func (d *Decoder) parseSplitMV(mbx, mby int, nearest motionVector) {
	// Parse the partition type using tree: {-3, 2, -2, 4, -0, -1}
	// Tree structure: bit=0 → 4x4, bit=1,0 → 8x8, bit=1,1,0 → 16x8, bit=1,1,1 → 8x16
	var splitType int
	if !d.fp.readBit(mbSplitProb[0]) {
		splitType = splitMV4x4 // -3 = type 3
	} else if !d.fp.readBit(mbSplitProb[1]) {
		splitType = splitMV8x8 // -2 = type 2
	} else if !d.fp.readBit(mbSplitProb[2]) {
		splitType = splitMV16x8 // -0 = type 0
	} else {
		splitType = splitMV8x16 // -1 = type 1
	}

	// Initialize sub-block MVs to zero.
	for i := range d.subMV {
		d.subMV[i] = mvZero
	}

	// Get left and above sub-block MVs for context.
	// Left blocks are at indices 3, 7, 11, 15 of the previous MB.
	// Above blocks are at indices 12, 13, 14, 15 of the above MB.
	var leftMVs [4]motionVector  // Left edge sub-blocks (rows 0-3)
	var aboveMVs [4]motionVector // Above edge sub-blocks (cols 0-3)

	if mbx > 0 && d.leftRefFrame != refFrameIntra {
		// Use the right edge of the left MB's sub-MVs.
		// If left MB was not SPLITMV, use its mbMV for all.
		leftMVs[0] = d.leftMV
		leftMVs[1] = d.leftMV
		leftMVs[2] = d.leftMV
		leftMVs[3] = d.leftMV
	}
	if mby > 0 && d.aboveRefFrame != refFrameIntra {
		// Use the bottom edge of the above MB's sub-MVs.
		aboveMVs[0] = d.aboveMV
		aboveMVs[1] = d.aboveMV
		aboveMVs[2] = d.aboveMV
		aboveMVs[3] = d.aboveMV
	}

	// Parse MVs for each partition.
	numPartitions := mbSplitCount[splitType]
	for p := 0; p < numPartitions; p++ {
		// Determine the first block index in this partition.
		firstBlock := mbSplitFillOffset[splitType][p][0]

		// Get left and above MVs for this sub-block.
		blockRow := firstBlock / 4
		blockCol := firstBlock % 4

		var leftMV, aboveMV motionVector
		if blockCol == 0 {
			// Left edge of MB - use left MB's MV.
			leftMV = leftMVs[blockRow]
		} else {
			// Use MV from the block to the left within this MB.
			leftMV = d.subMV[firstBlock-1]
		}
		if blockRow == 0 {
			// Top edge of MB - use above MB's MV.
			aboveMV = aboveMVs[blockCol]
		} else {
			// Use MV from the block above within this MB.
			aboveMV = d.subMV[firstBlock-4]
		}

		// Determine the context for sub-MV mode.
		ctx := d.getSubMVContext(leftMV, aboveMV)
		prob := subMvRefProb[ctx]

		// Parse sub-MV mode.
		var subMV motionVector
		if !d.fp.readBit(prob[0]) {
			// LEFT - use left MV.
			subMV = leftMV
		} else if !d.fp.readBit(prob[1]) {
			// ABOVE - use above MV.
			subMV = aboveMV
		} else if !d.fp.readBit(prob[2]) {
			// ZERO - zero MV.
			subMV = mvZero
		} else {
			// NEW - read new MV, add to nearest.
			deltaMV := d.readMV()
			subMV = addMV(nearest, deltaMV)
		}

		// Clamp the MV.
		subMV = d.clampMV(subMV, mbx, mby)

		// Fill all blocks in this partition with the same MV.
		fillCount := mbSplitFillCount[splitType][p]
		for i := 0; i < fillCount; i++ {
			blockIdx := mbSplitFillOffset[splitType][p][i]
			d.subMV[blockIdx] = subMV
		}
	}

	// For neighbor prediction, use the MV of a representative block.
	// Typically block 15 (bottom-right) or average.
	d.mbMV = d.subMV[15]
}

// getSubMVContext returns the context for sub-MV mode based on left/above MVs.
func (d *Decoder) getSubMVContext(left, above motionVector) int {
	leftZero := left.x == 0 && left.y == 0
	aboveZero := above.x == 0 && above.y == 0
	same := left.x == above.x && left.y == above.y

	if same {
		return 4 // Same
	}
	if leftZero && aboveZero {
		return 3 // Both zero
	}
	if aboveZero {
		return 2 // Above zero
	}
	if leftZero {
		return 1 // Left zero
	}
	return 0 // Normal
}

// findBestMV finds the nearest and near motion vectors from neighboring macroblocks.
// RFC 6386 Section 16.2.
func (d *Decoder) findBestMV(mbx, mby int) (nearest, near motionVector) {
	// Collect MV candidates from neighbors.
	var candidates [3]motionVector
	var candidateRefs [3]uint8
	nCandidates := 0

	// Left neighbor.
	if mbx > 0 && d.leftRefFrame != refFrameIntra {
		candidates[nCandidates] = d.leftMV
		candidateRefs[nCandidates] = d.leftRefFrame
		nCandidates++
	}

	// Above neighbor.
	if mby > 0 && d.aboveRefFrame != refFrameIntra {
		candidates[nCandidates] = d.aboveMV
		candidateRefs[nCandidates] = d.aboveRefFrame
		nCandidates++
	}

	// Above-left neighbor.
	if mbx > 0 && mby > 0 && d.upRefFrame[mbx-1] != refFrameIntra {
		candidates[nCandidates] = d.upMV[mbx-1]
		candidateRefs[nCandidates] = d.upRefFrame[mbx-1]
		nCandidates++
	}

	// Apply sign bias correction and find best candidates.
	refBias := d.signBias[d.refFrame]
	for i := 0; i < nCandidates; i++ {
		if d.signBias[candidateRefs[i]] != refBias {
			// Invert the MV if sign bias differs.
			candidates[i].x = -candidates[i].x
			candidates[i].y = -candidates[i].y
		}
	}

	// Select nearest (first non-zero) and near (second different non-zero).
	for i := 0; i < nCandidates; i++ {
		if candidates[i].x != 0 || candidates[i].y != 0 {
			if nearest.x == 0 && nearest.y == 0 {
				nearest = candidates[i]
			} else if (candidates[i].x != nearest.x || candidates[i].y != nearest.y) &&
				(near.x == 0 && near.y == 0) {
				near = candidates[i]
			}
		}
	}

	return nearest, near
}

// parsePredModeY16Intra parses intra Y16 mode for non-keyframes.
func (d *Decoder) parsePredModeY16Intra(mbx int) {
	// For intra blocks in inter frames, use different probabilities.
	var p uint8
	if !d.fp.readBit(yModeProb[0]) {
		p = predDC
	} else if !d.fp.readBit(yModeProb[1]) {
		p = predVE
	} else if !d.fp.readBit(yModeProb[2]) {
		p = predHE
	} else {
		p = predTM
	}
	for i := 0; i < 4; i++ {
		d.upMB[mbx].pred[i] = p
		d.leftMB.pred[i] = p
	}
	d.predY16 = p
}

// parsePredModeC8Intra parses intra C8 mode for non-keyframes.
func (d *Decoder) parsePredModeC8Intra() {
	if !d.fp.readBit(uvModeProb[0]) {
		d.predC8 = predDC
	} else if !d.fp.readBit(uvModeProb[1]) {
		d.predC8 = predVE
	} else if !d.fp.readBit(uvModeProb[2]) {
		d.predC8 = predHE
	} else {
		d.predC8 = predTM
	}
}

func (d *Decoder) parsePredModeY16(mbx int) {
	var p uint8
	if !d.fp.readBit(156) {
		if !d.fp.readBit(163) {
			p = predDC
		} else {
			p = predVE
		}
	} else if !d.fp.readBit(128) {
		p = predHE
	} else {
		p = predTM
	}
	for i := 0; i < 4; i++ {
		d.upMB[mbx].pred[i] = p
		d.leftMB.pred[i] = p
	}
	d.predY16 = p
}

func (d *Decoder) parsePredModeC8() {
	if !d.fp.readBit(142) {
		d.predC8 = predDC
	} else if !d.fp.readBit(114) {
		d.predC8 = predVE
	} else if !d.fp.readBit(183) {
		d.predC8 = predHE
	} else {
		d.predC8 = predTM
	}
}

func (d *Decoder) parsePredModeY4(mbx int) {
	for j := 0; j < 4; j++ {
		p := d.leftMB.pred[j]
		for i := 0; i < 4; i++ {
			prob := &predProb[d.upMB[mbx].pred[i]][p]
			if !d.fp.readBit(prob[0]) {
				p = predDC
			} else if !d.fp.readBit(prob[1]) {
				p = predTM
			} else if !d.fp.readBit(prob[2]) {
				p = predVE
			} else if !d.fp.readBit(prob[3]) {
				if !d.fp.readBit(prob[4]) {
					p = predHE
				} else if !d.fp.readBit(prob[5]) {
					p = predRD
				} else {
					p = predVR
				}
			} else if !d.fp.readBit(prob[6]) {
				p = predLD
			} else if !d.fp.readBit(prob[7]) {
				p = predVL
			} else if !d.fp.readBit(prob[8]) {
				p = predHD
			} else {
				p = predHU
			}
			d.predY4[j][i] = p
			d.upMB[mbx].pred[i] = p
		}
		d.leftMB.pred[j] = p
	}
}

// predProb are the probabilities to decode a 4x4 region's predictor mode given
// the predictor modes of the regions above and left of it.
// These values are specified in section 11.5.
var predProb = [nPred][nPred][9]uint8{
	{
		{231, 120, 48, 89, 115, 113, 120, 152, 112},
		{152, 179, 64, 126, 170, 118, 46, 70, 95},
		{175, 69, 143, 80, 85, 82, 72, 155, 103},
		{56, 58, 10, 171, 218, 189, 17, 13, 152},
		{114, 26, 17, 163, 44, 195, 21, 10, 173},
		{121, 24, 80, 195, 26, 62, 44, 64, 85},
		{144, 71, 10, 38, 171, 213, 144, 34, 26},
		{170, 46, 55, 19, 136, 160, 33, 206, 71},
		{63, 20, 8, 114, 114, 208, 12, 9, 226},
		{81, 40, 11, 96, 182, 84, 29, 16, 36},
	},
	{
		{134, 183, 89, 137, 98, 101, 106, 165, 148},
		{72, 187, 100, 130, 157, 111, 32, 75, 80},
		{66, 102, 167, 99, 74, 62, 40, 234, 128},
		{41, 53, 9, 178, 241, 141, 26, 8, 107},
		{74, 43, 26, 146, 73, 166, 49, 23, 157},
		{65, 38, 105, 160, 51, 52, 31, 115, 128},
		{104, 79, 12, 27, 217, 255, 87, 17, 7},
		{87, 68, 71, 44, 114, 51, 15, 186, 23},
		{47, 41, 14, 110, 182, 183, 21, 17, 194},
		{66, 45, 25, 102, 197, 189, 23, 18, 22},
	},
	{
		{88, 88, 147, 150, 42, 46, 45, 196, 205},
		{43, 97, 183, 117, 85, 38, 35, 179, 61},
		{39, 53, 200, 87, 26, 21, 43, 232, 171},
		{56, 34, 51, 104, 114, 102, 29, 93, 77},
		{39, 28, 85, 171, 58, 165, 90, 98, 64},
		{34, 22, 116, 206, 23, 34, 43, 166, 73},
		{107, 54, 32, 26, 51, 1, 81, 43, 31},
		{68, 25, 106, 22, 64, 171, 36, 225, 114},
		{34, 19, 21, 102, 132, 188, 16, 76, 124},
		{62, 18, 78, 95, 85, 57, 50, 48, 51},
	},
	{
		{193, 101, 35, 159, 215, 111, 89, 46, 111},
		{60, 148, 31, 172, 219, 228, 21, 18, 111},
		{112, 113, 77, 85, 179, 255, 38, 120, 114},
		{40, 42, 1, 196, 245, 209, 10, 25, 109},
		{88, 43, 29, 140, 166, 213, 37, 43, 154},
		{61, 63, 30, 155, 67, 45, 68, 1, 209},
		{100, 80, 8, 43, 154, 1, 51, 26, 71},
		{142, 78, 78, 16, 255, 128, 34, 197, 171},
		{41, 40, 5, 102, 211, 183, 4, 1, 221},
		{51, 50, 17, 168, 209, 192, 23, 25, 82},
	},
	{
		{138, 31, 36, 171, 27, 166, 38, 44, 229},
		{67, 87, 58, 169, 82, 115, 26, 59, 179},
		{63, 59, 90, 180, 59, 166, 93, 73, 154},
		{40, 40, 21, 116, 143, 209, 34, 39, 175},
		{47, 15, 16, 183, 34, 223, 49, 45, 183},
		{46, 17, 33, 183, 6, 98, 15, 32, 183},
		{57, 46, 22, 24, 128, 1, 54, 17, 37},
		{65, 32, 73, 115, 28, 128, 23, 128, 205},
		{40, 3, 9, 115, 51, 192, 18, 6, 223},
		{87, 37, 9, 115, 59, 77, 64, 21, 47},
	},
	{
		{104, 55, 44, 218, 9, 54, 53, 130, 226},
		{64, 90, 70, 205, 40, 41, 23, 26, 57},
		{54, 57, 112, 184, 5, 41, 38, 166, 213},
		{30, 34, 26, 133, 152, 116, 10, 32, 134},
		{39, 19, 53, 221, 26, 114, 32, 73, 255},
		{31, 9, 65, 234, 2, 15, 1, 118, 73},
		{75, 32, 12, 51, 192, 255, 160, 43, 51},
		{88, 31, 35, 67, 102, 85, 55, 186, 85},
		{56, 21, 23, 111, 59, 205, 45, 37, 192},
		{55, 38, 70, 124, 73, 102, 1, 34, 98},
	},
	{
		{125, 98, 42, 88, 104, 85, 117, 175, 82},
		{95, 84, 53, 89, 128, 100, 113, 101, 45},
		{75, 79, 123, 47, 51, 128, 81, 171, 1},
		{57, 17, 5, 71, 102, 57, 53, 41, 49},
		{38, 33, 13, 121, 57, 73, 26, 1, 85},
		{41, 10, 67, 138, 77, 110, 90, 47, 114},
		{115, 21, 2, 10, 102, 255, 166, 23, 6},
		{101, 29, 16, 10, 85, 128, 101, 196, 26},
		{57, 18, 10, 102, 102, 213, 34, 20, 43},
		{117, 20, 15, 36, 163, 128, 68, 1, 26},
	},
	{
		{102, 61, 71, 37, 34, 53, 31, 243, 192},
		{69, 60, 71, 38, 73, 119, 28, 222, 37},
		{68, 45, 128, 34, 1, 47, 11, 245, 171},
		{62, 17, 19, 70, 146, 85, 55, 62, 70},
		{37, 43, 37, 154, 100, 163, 85, 160, 1},
		{63, 9, 92, 136, 28, 64, 32, 201, 85},
		{75, 15, 9, 9, 64, 255, 184, 119, 16},
		{86, 6, 28, 5, 64, 255, 25, 248, 1},
		{56, 8, 17, 132, 137, 255, 55, 116, 128},
		{58, 15, 20, 82, 135, 57, 26, 121, 40},
	},
	{
		{164, 50, 31, 137, 154, 133, 25, 35, 218},
		{51, 103, 44, 131, 131, 123, 31, 6, 158},
		{86, 40, 64, 135, 148, 224, 45, 183, 128},
		{22, 26, 17, 131, 240, 154, 14, 1, 209},
		{45, 16, 21, 91, 64, 222, 7, 1, 197},
		{56, 21, 39, 155, 60, 138, 23, 102, 213},
		{83, 12, 13, 54, 192, 255, 68, 47, 28},
		{85, 26, 85, 85, 128, 128, 32, 146, 171},
		{18, 11, 7, 63, 144, 171, 4, 4, 246},
		{35, 27, 10, 146, 174, 171, 12, 26, 128},
	},
	{
		{190, 80, 35, 99, 180, 80, 126, 54, 45},
		{85, 126, 47, 87, 176, 51, 41, 20, 32},
		{101, 75, 128, 139, 118, 146, 116, 128, 85},
		{56, 41, 15, 176, 236, 85, 37, 9, 62},
		{71, 30, 17, 119, 118, 255, 17, 18, 138},
		{101, 38, 60, 138, 55, 70, 43, 26, 142},
		{146, 36, 19, 30, 171, 255, 97, 27, 20},
		{138, 45, 61, 62, 219, 1, 81, 188, 64},
		{32, 41, 20, 117, 151, 142, 20, 21, 163},
		{112, 19, 12, 61, 195, 128, 48, 4, 24},
	},
}
