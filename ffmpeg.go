package webrtcast

import (
	"bufio"
	"fmt"
	"os/exec"
	"time"

	"github.com/pion/webrtc/v4/pkg/media/h264reader"
)

// annexBStartCode prefixes every NAL unit in an Annex-B stream.
var annexBStartCode = []byte{0x00, 0x00, 0x00, 0x01}

// FFmpegSource produces H.264 video by running ffmpeg and parsing its Annex-B
// output into whole frames (access units).
type FFmpegSource struct {
	fps    int
	cmd    *exec.Cmd
	reader *h264reader.H264Reader

	// pending accumulates the NAL units of the access unit currently being
	// assembled; haveVCL records whether it already holds a coded picture.
	pending []byte
	haveVCL bool
}

// NewFFmpegSource returns a source that streams a 640x480 test pattern at 30fps.
func NewFFmpegSource() *FFmpegSource {
	return &FFmpegSource{fps: 30}
}

func (f *FFmpegSource) Start() error {
	f.cmd = exec.Command(
		"ffmpeg",
		"-re",
		"-f", "lavfi",
		"-i", fmt.Sprintf("testsrc=size=640x480:rate=%d", f.fps),
		"-vcodec", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-f", "h264", // raw Annex-B stream on stdout
		"-",
	)

	stdout, err := f.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := f.cmd.Start(); err != nil {
		return err
	}

	reader, err := h264reader.NewReader(bufio.NewReader(stdout))
	if err != nil {
		f.cmd.Process.Kill()
		return err
	}
	f.reader = reader
	return nil
}

// ReadFrame returns the next complete access unit.
//
// It reads NAL units until it sees the start of the next picture, then emits
// everything gathered so far as one frame. Grouping NALs this way (rather than
// returning raw byte chunks) keeps each WebRTC sample aligned to a frame
// boundary, so the RTP marker bit and timestamps are correct.
func (f *FFmpegSource) ReadFrame() (Frame, error) {
	for {
		nal, err := f.reader.NextNAL()
		if err != nil {
			return Frame{}, err
		}

		// VCL NAL types (1-5) carry the coded picture itself; everything else
		// (SPS, PPS, SEI, ...) is metadata that belongs with the picture that
		// follows it.
		isVCL := nal.UnitType >= h264reader.NalUnitTypeCodedSliceNonIdr &&
			nal.UnitType <= h264reader.NalUnitTypeCodedSliceIdr

		// A VCL NAL arriving when we already have one means the previous access
		// unit is complete: flush it and begin the new one with this NAL.
		if isVCL && f.haveVCL {
			frame := f.flush()
			f.appendNAL(nal.Data)
			f.haveVCL = true
			return frame, nil
		}

		f.appendNAL(nal.Data)
		if isVCL {
			f.haveVCL = true
		}
	}
}

func (f *FFmpegSource) appendNAL(data []byte) {
	f.pending = append(f.pending, annexBStartCode...)
	f.pending = append(f.pending, data...)
}

func (f *FFmpegSource) flush() Frame {
	frame := Frame{
		Data:     f.pending,
		Duration: time.Second / time.Duration(f.fps),
	}
	f.pending = nil // start a fresh buffer; the flushed slice is untouched
	f.haveVCL = false
	return frame
}

func (f *FFmpegSource) Stop() error {
	if f.cmd != nil && f.cmd.Process != nil {
		return f.cmd.Process.Kill()
	}
	return nil
}
