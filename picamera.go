package webrtcast

import "strconv"

// Pi camera defaults tuned for the Raspberry Pi's VideoCore hardware H.264
// encoder — light enough for a Pi Zero.
const (
	// defaultCameraBinary is the libcamera capture tool on current Raspberry Pi
	// OS. On Bullseye it is named "libcamera-vid"; on the legacy stack use
	// raspivid via NewCommandSource instead (its flags differ).
	defaultCameraBinary  = "rpicam-vid"
	defaultCameraWidth   = 640
	defaultCameraHeight  = 480
	defaultCameraFPS     = 30
	defaultCameraBitrate = 800_000 // bits per second
	defaultCameraProfile = "baseline"
)

// piCameraConfig is assembled from PiCameraOption values into a rpicam-vid
// argument list by NewPiCameraSource.
type piCameraConfig struct {
	binary  string
	width   int
	height  int
	fps     int
	bitrate int
	profile string
}

// PiCameraOption configures the camera command built by NewPiCameraSource. See
// the With* helpers.
type PiCameraOption func(*piCameraConfig)

// WithCameraBinary overrides the capture binary (default "rpicam-vid"). Use
// "libcamera-vid" on Raspberry Pi OS Bullseye, where the tool has the same
// flags under the older name.
func WithCameraBinary(name string) PiCameraOption {
	return func(c *piCameraConfig) {
		if name != "" {
			c.binary = name
		}
	}
}

// WithCameraResolution sets the capture resolution. On a Pi Zero keep this
// modest (640x480) — the WiFi uplink, not the encoder, is the bottleneck.
func WithCameraResolution(width, height int) PiCameraOption {
	return func(c *piCameraConfig) {
		if width > 0 && height > 0 {
			c.width, c.height = width, height
		}
	}
}

// WithCameraFPS sets the capture frame rate. It also sets the per-frame
// Duration reported by ReadFrame. Values <= 0 are ignored.
func WithCameraFPS(fps int) PiCameraOption {
	return func(c *piCameraConfig) {
		if fps > 0 {
			c.fps = fps
		}
	}
}

// WithCameraBitrate sets the target H.264 bitrate in bits per second.
func WithCameraBitrate(bitsPerSecond int) PiCameraOption {
	return func(c *piCameraConfig) {
		if bitsPerSecond > 0 {
			c.bitrate = bitsPerSecond
		}
	}
}

// WithCameraProfile sets the H.264 profile ("baseline", "main", "high").
// Baseline is the default: no B-frames, lowest latency, widest WebRTC
// compatibility.
func WithCameraProfile(profile string) PiCameraOption {
	return func(c *piCameraConfig) {
		if profile != "" {
			c.profile = profile
		}
	}
}

// NewPiCameraSource returns a source that captures from the Raspberry Pi camera
// and encodes H.264 on the VideoCore hardware encoder — the optimal path on a
// Pi Zero, where software encoding cannot keep up.
//
// The "--inline" flag is always set so SPS/PPS parameter sets are repeated
// before every keyframe; this lets WebRTC viewers that join mid-stream start
// decoding without waiting for the next stream restart.
func NewPiCameraSource(opts ...PiCameraOption) *CommandSource {
	c := &piCameraConfig{
		binary:  defaultCameraBinary,
		width:   defaultCameraWidth,
		height:  defaultCameraHeight,
		fps:     defaultCameraFPS,
		bitrate: defaultCameraBitrate,
		profile: defaultCameraProfile,
	}
	for _, opt := range opts {
		opt(c)
	}

	args := []string{
		"-t", "0", // run until the process is stopped
		"--codec", "h264",
		"--width", strconv.Itoa(c.width),
		"--height", strconv.Itoa(c.height),
		"--framerate", strconv.Itoa(c.fps),
		"--bitrate", strconv.Itoa(c.bitrate),
		"--profile", c.profile,
		"--inline",    // repeat SPS/PPS before every keyframe — required for mid-stream WebRTC joins
		"--nopreview", // headless: no display output
		"-o", "-",     // raw Annex-B H.264 to stdout
	}

	return NewCommandSource(c.binary, c.fps, args...)
}
