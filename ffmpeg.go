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

// Default ffmpeg settings used when an option is left unset.
const (
	defaultFPS  = 30
	defaultSize = "640x480"
)

// defaultEncoderArgs encode H.264 tuned for low-latency live streaming.
var defaultEncoderArgs = []string{
	"-vcodec", "libx264",
	"-preset", "ultrafast",
	"-tune", "zerolatency",
}

// FFmpegSource produces H.264 video by running ffmpeg and parsing its Annex-B
// output into whole frames (access units).
type FFmpegSource struct {
	fps         int
	size        string
	inputArgs   []string
	encoderArgs []string

	cmd    *exec.Cmd
	reader *h264reader.H264Reader

	// pending accumulates the NAL units of the access unit currently being
	// assembled; haveVCL records whether it already holds a coded picture.
	pending []byte
	haveVCL bool
}

// FFmpegOption configures a FFmpegSource. See the With* helpers.
type FFmpegOption func(*FFmpegSource)

// WithFPS sets the frame rate. It controls both the default test-pattern rate
// and the per-frame Duration reported by ReadFrame, so set it to match a custom
// input source too. Values <= 0 are ignored.
func WithFPS(fps int) FFmpegOption {
	return func(f *FFmpegSource) {
		if fps > 0 {
			f.fps = fps
		}
	}
}

// WithSize sets the WxH frame size (e.g. "1280x720") of the default test
// pattern. It has no effect when WithInputArgs is supplied.
func WithSize(size string) FFmpegOption {
	return func(f *FFmpegSource) {
		if size != "" {
			f.size = size
		}
	}
}

// WithInputArgs replaces the ffmpeg arguments that select the video source,
// e.g. WithInputArgs("-i", "rtsp://camera/stream") or WithInputArgs("-f",
// "v4l2", "-i", "/dev/video0"). When unset, a generated test pattern derived
// from WithSize and WithFPS is used. The real-time pacing flag "-re" is always
// prepended.
func WithInputArgs(args ...string) FFmpegOption {
	return func(f *FFmpegSource) { f.inputArgs = args }
}

// WithEncoderArgs replaces the ffmpeg arguments controlling H.264 encoding
// (codec, preset, tune, bitrate, ...). The output is always raw Annex-B H.264
// on stdout, which ReadFrame depends on, so that part is not configurable.
func WithEncoderArgs(args ...string) FFmpegOption {
	return func(f *FFmpegSource) { f.encoderArgs = args }
}

// NewFFmpegSource returns a source that streams a 640x480 test pattern at 30fps.
// Pass options such as WithFPS or WithInputArgs to customise the ffmpeg command.
func NewFFmpegSource(opts ...FFmpegOption) *FFmpegSource {
	f := &FFmpegSource{
		fps:         defaultFPS,
		size:        defaultSize,
		encoderArgs: defaultEncoderArgs,
	}
	for _, opt := range opts {
		opt(f)
	}
	return f
}

func (f *FFmpegSource) Start() error {
	args := []string{"-re"}
	if len(f.inputArgs) > 0 {
		args = append(args, f.inputArgs...)
	} else {
		args = append(args,
			"-f", "lavfi",
			"-i", fmt.Sprintf("testsrc=size=%s:rate=%d", f.size, f.fps),
		)
	}
	args = append(args, f.encoderArgs...)
	args = append(args,
		"-f", "h264", // raw Annex-B stream on stdout
		"-",
	)
	f.cmd = exec.Command("ffmpeg", args...)

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
