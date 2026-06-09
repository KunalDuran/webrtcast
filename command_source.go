package webrtcast

import (
	"bufio"
	"os/exec"
	"time"

	"github.com/pion/webrtc/v4/pkg/media/h264reader"
)

// annexBStartCode prefixes every NAL unit in an Annex-B stream.
var annexBStartCode = []byte{0x00, 0x00, 0x00, 0x01}

// CommandSource produces H.264 video by running an external command and parsing
// its Annex-B output on stdout into whole frames (access units).
//
// Any encoder works as long as it writes a raw Annex-B H.264 stream to stdout —
// ffmpeg, rpicam-vid and raspivid all do. Use NewFFmpegSource or
// NewPiCameraSource for ready-made presets, or NewCommandSource to drive an
// arbitrary command.
type CommandSource struct {
	name string
	args []string
	fps  int

	cmd    *exec.Cmd
	reader *h264reader.H264Reader

	// pending accumulates the NAL units of the access unit currently being
	// assembled; haveVCL records whether it already holds a coded picture.
	pending []byte
	haveVCL bool
}

// NewCommandSource returns a source that runs name with args and reads raw
// Annex-B H.264 from its stdout. fps sets the per-frame Duration reported by
// ReadFrame, so make it match the stream's actual frame rate.
func NewCommandSource(name string, fps int, args ...string) *CommandSource {
	return &CommandSource{name: name, fps: fps, args: args}
}

func (s *CommandSource) Start() error {
	s.cmd = exec.Command(s.name, s.args...)

	stdout, err := s.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := s.cmd.Start(); err != nil {
		return err
	}

	reader, err := h264reader.NewReader(bufio.NewReader(stdout))
	if err != nil {
		s.cmd.Process.Kill()
		return err
	}
	s.reader = reader
	return nil
}

// ReadFrame returns the next complete access unit.
//
// It reads NAL units until it sees the start of the next picture, then emits
// everything gathered so far as one frame. Grouping NALs this way (rather than
// returning raw byte chunks) keeps each WebRTC sample aligned to a frame
// boundary, so the RTP marker bit and timestamps are correct.
func (s *CommandSource) ReadFrame() (Frame, error) {
	for {
		nal, err := s.reader.NextNAL()
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
		if isVCL && s.haveVCL {
			frame := s.flush()
			s.appendNAL(nal.Data)
			s.haveVCL = true
			return frame, nil
		}

		s.appendNAL(nal.Data)
		if isVCL {
			s.haveVCL = true
		}
	}
}

func (s *CommandSource) appendNAL(data []byte) {
	s.pending = append(s.pending, annexBStartCode...)
	s.pending = append(s.pending, data...)
}

func (s *CommandSource) flush() Frame {
	frame := Frame{
		Data:     s.pending,
		Duration: time.Second / time.Duration(s.fps),
	}
	s.pending = nil // start a fresh buffer; the flushed slice is untouched
	s.haveVCL = false
	return frame
}

func (s *CommandSource) Stop() error {
	if s.cmd != nil && s.cmd.Process != nil {
		return s.cmd.Process.Kill()
	}
	return nil
}
