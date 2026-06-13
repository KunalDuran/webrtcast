package webrtcast

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"sync"
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
//
// A CommandSource is single-use: Start runs the process, ReadFrame drains it,
// and Stop reaps it. Build a fresh one for each run (the Broadcaster does this
// via its SourceFunc); do not Start a source that has already been Stopped.
type CommandSource struct {
	name string
	args []string
	fps  int

	cmd    *exec.Cmd
	reader *h264reader.H264Reader
	stderr *ringBuffer

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

// Start launches the encoder process. Cancelling ctx kills the process, which
// unblocks any in-flight ReadFrame with an error; the owner then calls Stop to
// reap it.
func (s *CommandSource) Start(ctx context.Context) error {
	s.cmd = exec.CommandContext(ctx, s.name, s.args...)

	// Keep the tail of stderr so a process that dies (bad device, bad args)
	// surfaces its own error message instead of a bare EOF from ReadFrame.
	s.stderr = newRingBuffer(4096)
	s.cmd.Stderr = s.stderr

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
		s.cmd.Wait()
		return err
	}
	s.reader = reader
	return nil
}

// ReadFrame returns the next complete access unit.
//
// It reads NAL units until it sees the start of the next access unit, then
// emits everything gathered so far as one frame. Grouping NALs this way (rather
// than returning raw byte chunks) keeps each WebRTC sample aligned to a frame
// boundary, so the RTP marker bit and timestamps are correct.
//
// Limitation: a multi-slice picture would be flushed per slice, since each VCL
// NAL is treated as starting a new access unit. This is fine for encoders that
// emit one slice per frame (x264 and rpicam-vid do so by default).
func (s *CommandSource) ReadFrame() (Frame, error) {
	for {
		nal, err := s.reader.NextNAL()
		if err != nil {
			return Frame{}, s.wrapErr(err)
		}

		// VCL NAL types (1-5) carry the coded picture itself.
		isVCL := nal.UnitType >= h264reader.NalUnitTypeCodedSliceNonIdr &&
			nal.UnitType <= h264reader.NalUnitTypeCodedSliceIdr

		// Per the H.264 spec, an access unit ends when the next one begins, and a
		// new access unit starts at the first VCL NAL of a new picture OR at any
		// AUD/SPS/PPS/SEI that precedes it. Flushing only on VCL→VCL would glue
		// inline parameter sets (rpicam-vid's --inline emits SPS/PPS before every
		// IDR) onto the previous frame instead of the keyframe they belong to.
		startsNewAU := isVCL ||
			nal.UnitType == h264reader.NalUnitTypeSPS ||
			nal.UnitType == h264reader.NalUnitTypePPS ||
			nal.UnitType == h264reader.NalUnitTypeSEI ||
			nal.UnitType == h264reader.NalUnitTypeAUD

		if s.haveVCL && startsNewAU {
			frame := s.flush()
			s.appendNAL(nal.Data)
			s.haveVCL = isVCL
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

// wrapErr annotates a read error with the tail of the process's stderr, turning
// the bare EOF a dead encoder produces into something diagnosable.
func (s *CommandSource) wrapErr(err error) error {
	if s.stderr != nil {
		if tail := s.stderr.String(); tail != "" {
			return fmt.Errorf("%w; encoder stderr: %s", err, tail)
		}
	}
	return err
}

// Stop kills the process (if still running) and waits for it, reaping the OS
// process so repeated start/stop cycles do not accumulate zombies. It is safe
// to call on a source that already exited on its own.
//
// There is a documented race between killing the process and the stdout pipe
// being fully drained; Stop is meant to be called once reading has stopped
// (i.e. after ReadFrame has returned an error), which is the standard
// kill-then-wait teardown.
func (s *CommandSource) Stop() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return nil
	}
	s.cmd.Process.Kill()
	s.cmd.Wait() // reap; the error is just the kill signal / prior exit status
	s.cmd = nil
	return nil
}

// ringBuffer is an io.Writer that retains only the last size bytes written to
// it — enough to capture an encoder's final error message without growing
// unbounded over a long run.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []byte
	size int
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{size: size}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.size {
		r.buf = r.buf[len(r.buf)-r.size:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}
