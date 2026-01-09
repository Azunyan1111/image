// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package vp8

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
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
