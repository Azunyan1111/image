// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vp8

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

// IVF file format constants.
const (
	ivfHeaderSize      = 32
	ivfFrameHeaderSize = 12
)

// ivfHeader represents the IVF file header.
type ivfHeader struct {
	Signature     [4]byte  // "DKIF"
	Version       uint16   // Should be 0
	HeaderLength  uint16   // Should be 32
	FourCC        [4]byte  // "VP80"
	Width         uint16   // Frame width
	Height        uint16   // Frame height
	TimebaseNum   uint32   // Timebase numerator
	TimebaseDen   uint32   // Timebase denominator
	NumFrames     uint32   // Number of frames
	Unused        uint32   // Reserved
}

// parseIVFHeader parses the IVF file header.
func parseIVFHeader(r io.Reader) (*ivfHeader, error) {
	var h ivfHeader
	if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
		return nil, err
	}
	if string(h.Signature[:]) != "DKIF" {
		return nil, io.ErrUnexpectedEOF
	}
	if string(h.FourCC[:]) != "VP80" {
		return nil, io.ErrUnexpectedEOF
	}
	return &h, nil
}

// readIVFFrame reads one frame from an IVF file.
func readIVFFrame(r io.Reader) ([]byte, uint64, error) {
	var frameSize uint32
	var timestamp uint64
	if err := binary.Read(r, binary.LittleEndian, &frameSize); err != nil {
		return nil, 0, err
	}
	if err := binary.Read(r, binary.LittleEndian, &timestamp); err != nil {
		return nil, 0, err
	}
	data := make([]byte, frameSize)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, 0, err
	}
	return data, timestamp, nil
}

func TestDecodeKeyframe(t *testing.T) {
	// Read the test video file.
	path := filepath.Join("testdata", "simple_video.ivf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test data not found: %v", err)
	}

	r := bytes.NewReader(data)
	h, err := parseIVFHeader(r)
	if err != nil {
		t.Fatalf("parseIVFHeader: %v", err)
	}

	t.Logf("IVF: %dx%d, %d frames", h.Width, h.Height, h.NumFrames)

	// Read and decode the first frame (keyframe).
	frameData, _, err := readIVFFrame(r)
	if err != nil {
		t.Fatalf("readIVFFrame: %v", err)
	}

	d := NewDecoder()
	d.Init(bytes.NewReader(frameData), len(frameData))

	fh, err := d.DecodeFrameHeader()
	if err != nil {
		t.Fatalf("DecodeFrameHeader: %v", err)
	}

	if !fh.KeyFrame {
		t.Error("expected first frame to be a keyframe")
	}
	t.Logf("Frame 0: keyframe=%v, width=%d, height=%d", fh.KeyFrame, fh.Width, fh.Height)

	img, err := d.DecodeFrame()
	if err != nil {
		t.Fatalf("DecodeFrame: %v", err)
	}

	if img.Bounds().Dx() != int(h.Width) || img.Bounds().Dy() != int(h.Height) {
		t.Errorf("image size mismatch: got %dx%d, want %dx%d",
			img.Bounds().Dx(), img.Bounds().Dy(), h.Width, h.Height)
	}
}

func TestDecodeInterFrames(t *testing.T) {
	// Read the test video file.
	path := filepath.Join("testdata", "test_realtime.ivf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test data not found: %v", err)
	}

	r := bytes.NewReader(data)
	h, err := parseIVFHeader(r)
	if err != nil {
		t.Fatalf("parseIVFHeader: %v", err)
	}

	t.Logf("IVF: %dx%d, %d frames", h.Width, h.Height, h.NumFrames)

	d := NewDecoder()
	keyframeCount := 0
	interframeCount := 0

	for i := uint32(0); i < h.NumFrames; i++ {
		frameData, _, err := readIVFFrame(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("frame %d: readIVFFrame: %v", i, err)
		}

		d.Init(bytes.NewReader(frameData), len(frameData))

		fh, err := d.DecodeFrameHeader()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrameHeader: %v", i, err)
		}

		if fh.KeyFrame {
			keyframeCount++
		} else {
			interframeCount++
		}

		t.Logf("Frame %d: keyframe=%v, size=%d bytes", i, fh.KeyFrame, len(frameData))

		img, err := d.DecodeFrame()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrame: %v", i, err)
		}

		if img.Bounds().Dx() != int(h.Width) || img.Bounds().Dy() != int(h.Height) {
			t.Errorf("frame %d: image size mismatch: got %dx%d, want %dx%d",
				i, img.Bounds().Dx(), img.Bounds().Dy(), h.Width, h.Height)
		}
	}

	t.Logf("Total: %d keyframes, %d inter frames", keyframeCount, interframeCount)

	if keyframeCount == 0 {
		t.Error("expected at least one keyframe")
	}
	if interframeCount == 0 {
		t.Error("expected at least one inter frame")
	}
}

func TestDecodeMotionVideo(t *testing.T) {
	// Read the motion test video file.
	path := filepath.Join("testdata", "motion_video.ivf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test data not found: %v", err)
	}

	r := bytes.NewReader(data)
	h, err := parseIVFHeader(r)
	if err != nil {
		t.Fatalf("parseIVFHeader: %v", err)
	}

	t.Logf("IVF: %dx%d, %d frames", h.Width, h.Height, h.NumFrames)

	d := NewDecoder()

	for i := uint32(0); i < h.NumFrames; i++ {
		frameData, _, err := readIVFFrame(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("frame %d: readIVFFrame: %v", i, err)
		}

		d.Init(bytes.NewReader(frameData), len(frameData))

		fh, err := d.DecodeFrameHeader()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrameHeader: %v", i, err)
		}

		t.Logf("Frame %d: keyframe=%v, size=%d bytes", i, fh.KeyFrame, len(frameData))

		img, err := d.DecodeFrame()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrame: %v", i, err)
		}

		if img.Bounds().Dx() != int(h.Width) || img.Bounds().Dy() != int(h.Height) {
			t.Errorf("frame %d: image size mismatch: got %dx%d, want %dx%d",
				i, img.Bounds().Dx(), img.Bounds().Dy(), h.Width, h.Height)
		}
	}
}

// decodeVideoFile is a helper function to decode an entire video file.
func decodeVideoFile(t *testing.T, filename string, expectedWidth, expectedHeight int) {
	path := filepath.Join("testdata", filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test data not found: %v", err)
	}

	r := bytes.NewReader(data)
	h, err := parseIVFHeader(r)
	if err != nil {
		t.Fatalf("parseIVFHeader: %v", err)
	}

	if int(h.Width) != expectedWidth || int(h.Height) != expectedHeight {
		t.Errorf("IVF header size mismatch: got %dx%d, want %dx%d",
			h.Width, h.Height, expectedWidth, expectedHeight)
	}

	t.Logf("IVF: %dx%d, %d frames", h.Width, h.Height, h.NumFrames)

	d := NewDecoder()
	keyframeCount := 0
	interframeCount := 0

	for i := uint32(0); i < h.NumFrames; i++ {
		frameData, _, err := readIVFFrame(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("frame %d: readIVFFrame: %v", i, err)
		}

		d.Init(bytes.NewReader(frameData), len(frameData))

		fh, err := d.DecodeFrameHeader()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrameHeader: %v", i, err)
		}

		if fh.KeyFrame {
			keyframeCount++
		} else {
			interframeCount++
		}

		img, err := d.DecodeFrame()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrame: %v", i, err)
		}

		if img.Bounds().Dx() != expectedWidth || img.Bounds().Dy() != expectedHeight {
			t.Errorf("frame %d: image size mismatch: got %dx%d, want %dx%d",
				i, img.Bounds().Dx(), img.Bounds().Dy(), expectedWidth, expectedHeight)
		}
	}

	t.Logf("Total: %d keyframes, %d inter frames", keyframeCount, interframeCount)
}

func TestDecode720p30fps1s(t *testing.T) {
	decodeVideoFile(t, "720p_30fps_1s.ivf", 1280, 720)
}

func TestDecode720p60fps1s(t *testing.T) {
	decodeVideoFile(t, "720p_60fps_1s.ivf", 1280, 720)
}

func TestDecode1080p30fps1s(t *testing.T) {
	decodeVideoFile(t, "1080p_30fps_1s.ivf", 1920, 1080)
}

func TestDecode1080p60fps1s(t *testing.T) {
	decodeVideoFile(t, "1080p_60fps_1s.ivf", 1920, 1080)
}

func TestDecode720p30fps5s(t *testing.T) {
	decodeVideoFile(t, "720p_30fps_5s.ivf", 1280, 720)
}

func TestDecode1080p30fps5s(t *testing.T) {
	decodeVideoFile(t, "1080p_30fps_5s.ivf", 1920, 1080)
}

// TestDecodeQRCodeVideo tests that the VP8 decoder produces frames with sufficient
// quality to decode a QR code. This verifies the decoder meets minimum quality requirements.
func TestDecodeCompareWithFFmpeg(t *testing.T) {
	// Decode testsrc video and save frames for visual comparison.
	path := filepath.Join("testdata", "testsrc.ivf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test data not found: %v", err)
	}

	r := bytes.NewReader(data)
	h, err := parseIVFHeader(r)
	if err != nil {
		t.Fatalf("parseIVFHeader: %v", err)
	}

	t.Logf("IVF: %dx%d, %d frames", h.Width, h.Height, h.NumFrames)

	d := NewDecoder()

	// Decode and save frames 0, 1, 10, 20.
	framesToSave := map[uint32]bool{0: true, 1: true, 10: true, 20: true}

	for i := uint32(0); i < h.NumFrames; i++ {
		frameData, _, err := readIVFFrame(r)
		if err != nil {
			break
		}

		d.Init(bytes.NewReader(frameData), len(frameData))
		fh, err := d.DecodeFrameHeader()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrameHeader: %v", i, err)
		}

		img, err := d.DecodeFrame()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrame: %v", i, err)
		}

		if framesToSave[i] {
			outPath := fmt.Sprintf("/tmp/vp8_testsrc_%02d_key%v.png", i, fh.KeyFrame)
			f, _ := os.Create(outPath)
			bounds := img.Bounds()
			rgba := image.NewRGBA(bounds)
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					rgba.Set(x, y, img.At(x, y))
				}
			}
			png.Encode(f, rgba)
			f.Close()
			t.Logf("Saved frame %d (keyframe=%v) to %s", i, fh.KeyFrame, outPath)
			if !fh.KeyFrame {
				t.Logf("Frame %d MV modes: NEAREST=%d, NEAR=%d, ZERO=%d, NEW=%d, SPLIT=%d | Intra=%d, Inter=%d",
					i, d.MVModeCount[0], d.MVModeCount[1], d.MVModeCount[2], d.MVModeCount[3], d.MVModeCount[4],
					d.IntraMBCount, d.InterMBCount)
			}
		}
	}
}

func TestDecodeQRCodeVideo(t *testing.T) {
	const expectedQRContent = "VP8_DECODER_TEST_2025"

	path := filepath.Join("testdata", "qrcode_best_quality.ivf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("test data not found: %v", err)
	}

	r := bytes.NewReader(data)
	h, err := parseIVFHeader(r)
	if err != nil {
		t.Fatalf("parseIVFHeader: %v", err)
	}

	t.Logf("IVF: %dx%d, %d frames", h.Width, h.Height, h.NumFrames)

	d := NewDecoder()
	qrReader := qrcode.NewQRCodeReader()

	keyframeSuccess := 0
	keyframeTotal := 0
	interframeSuccess := 0
	interframeTotal := 0

	for i := uint32(0); i < h.NumFrames; i++ {
		frameData, _, err := readIVFFrame(r)
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("frame %d: readIVFFrame: %v", i, err)
		}

		d.Init(bytes.NewReader(frameData), len(frameData))

		fh, err := d.DecodeFrameHeader()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrameHeader: %v", i, err)
		}

		img, err := d.DecodeFrame()
		if err != nil {
			t.Fatalf("frame %d: DecodeFrame: %v", i, err)
		}

		isKeyframe := fh.KeyFrame

		// Convert YCbCr to grayscale for QR code reading.
		grayImg := ycbcrToGray(img)

		// Try to decode QR code from the frame.
		bmp, err := gozxing.NewBinaryBitmapFromImage(grayImg)
		if err != nil {
			if isKeyframe {
				keyframeTotal++
				t.Logf("frame %d (keyframe): failed to create bitmap: %v", i, err)
			} else {
				interframeTotal++
			}
			continue
		}

		result, err := qrReader.Decode(bmp, nil)
		if err != nil {
			if isKeyframe {
				keyframeTotal++
				t.Logf("frame %d (keyframe): failed to decode QR: %v", i, err)
			} else {
				interframeTotal++
				// Log failed interframes to identify patterns.
				t.Logf("frame %d (inter, %d from keyframe): failed to decode QR", i, int(i)%15)
			}
			continue
		}

		if result.GetText() == expectedQRContent {
			if isKeyframe {
				keyframeSuccess++
				keyframeTotal++
			} else {
				interframeSuccess++
				interframeTotal++
			}
		} else {
			if isKeyframe {
				keyframeTotal++
			} else {
				interframeTotal++
			}
			t.Logf("frame %d: QR decoded but content mismatch: got %q, want %q",
				i, result.GetText(), expectedQRContent)
		}
	}

	totalFrames := keyframeTotal + interframeTotal
	qrDecodedCount := keyframeSuccess + interframeSuccess

	t.Logf("Keyframe QR success: %d/%d (%.2f%%)", keyframeSuccess, keyframeTotal,
		float64(keyframeSuccess)/float64(keyframeTotal)*100)
	t.Logf("Interframe QR success: %d/%d (%.2f%%)", interframeSuccess, interframeTotal,
		float64(interframeSuccess)/float64(interframeTotal)*100)
	t.Logf("Total QR code decoded successfully in %d/%d frames", qrDecodedCount, totalFrames)

	// At least 80% of frames should have readable QR codes.
	minSuccessRate := 0.8
	successRate := float64(qrDecodedCount) / float64(totalFrames)
	if successRate < minSuccessRate {
		t.Errorf("QR code success rate too low: got %.2f%%, want at least %.2f%%",
			successRate*100, minSuccessRate*100)
	}
}

// ycbcrToGray converts a YCbCr image to grayscale for QR code reading.
func ycbcrToGray(img *image.YCbCr) *image.Gray {
	bounds := img.Bounds()
	gray := image.NewGray(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			yOffset := img.YOffset(x, y)
			gray.SetGray(x, y, color.Gray{Y: img.Y[yOffset]})
		}
	}
	return gray
}
