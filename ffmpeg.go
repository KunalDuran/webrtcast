package webrtcast

import "fmt"

// Default ffmpeg settings used when an option is left unset.
const (
	defaultFPS  = 30
	defaultSize = "640x480"
)

// defaultEncoderArgs encode H.264 tuned for low-latency live streaming.
//
// NOTE: libx264 is software encoding. It is fine on a desktop but will not keep
// up in real time on a Raspberry Pi Zero — use NewPiCameraSource (hardware
// encode) there instead.
var defaultEncoderArgs = []string{
	"-vcodec", "libx264",
	"-preset", "ultrafast",
	"-tune", "zerolatency",
}

// ffmpegConfig is assembled from FFmpegOption values into an ffmpeg argument
// list by NewFFmpegSource.
type ffmpegConfig struct {
	fps         int
	size        string
	inputArgs   []string
	encoderArgs []string
}

// FFmpegOption configures the ffmpeg command built by NewFFmpegSource. See the
// With* helpers.
type FFmpegOption func(*ffmpegConfig)

// WithFPS sets the frame rate. It controls both the default test-pattern rate
// and the per-frame Duration reported by ReadFrame, so set it to match a custom
// input source too. Values <= 0 are ignored.
func WithFPS(fps int) FFmpegOption {
	return func(c *ffmpegConfig) {
		if fps > 0 {
			c.fps = fps
		}
	}
}

// WithSize sets the WxH frame size (e.g. "1280x720") of the default test
// pattern. It has no effect when WithInputArgs is supplied.
func WithSize(size string) FFmpegOption {
	return func(c *ffmpegConfig) {
		if size != "" {
			c.size = size
		}
	}
}

// WithInputArgs replaces the ffmpeg arguments that select the video source,
// e.g. WithInputArgs("-i", "rtsp://camera/stream") or WithInputArgs("-f",
// "v4l2", "-i", "/dev/video0"). When unset, a generated test pattern derived
// from WithSize and WithFPS is used. The real-time pacing flag "-re" is always
// prepended, and the raw Annex-B output stage is always appended, so include
// neither here — only the input-selection arguments.
func WithInputArgs(args ...string) FFmpegOption {
	return func(c *ffmpegConfig) { c.inputArgs = args }
}

// WithEncoderArgs replaces the ffmpeg arguments controlling H.264 encoding
// (codec, preset, tune, bitrate, ...). The output is always raw Annex-B H.264
// on stdout, which ReadFrame depends on, so that part is not configurable.
func WithEncoderArgs(args ...string) FFmpegOption {
	return func(c *ffmpegConfig) { c.encoderArgs = args }
}

// NewFFmpegSource returns a source that streams a 640x480 test pattern at 30fps
// via ffmpeg. Pass options such as WithFPS or WithInputArgs to customise the
// command.
func NewFFmpegSource(opts ...FFmpegOption) *CommandSource {
	c := &ffmpegConfig{
		fps:         defaultFPS,
		size:        defaultSize,
		encoderArgs: defaultEncoderArgs,
	}
	for _, opt := range opts {
		opt(c)
	}

	args := []string{"-re"}
	if len(c.inputArgs) > 0 {
		args = append(args, c.inputArgs...)
	} else {
		args = append(args,
			"-f", "lavfi",
			"-i", fmt.Sprintf("testsrc=size=%s:rate=%d", c.size, c.fps),
		)
	}
	args = append(args, c.encoderArgs...)
	args = append(args,
		"-f", "h264", // raw Annex-B stream on stdout
		"-",
	)

	return NewCommandSource("ffmpeg", c.fps, args...)
}
