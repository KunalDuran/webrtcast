package webrtcast

import (
	"context"
	"time"
)

// VideoSource is anything that can produce a stream of encoded video frames
// (for example an FFmpeg process or a camera). Implement this interface to feed
// your own source into a Broadcaster.
//
// A VideoSource is single-use: the Broadcaster constructs a fresh one (via its
// SourceFunc) for each run, Starts it, drains it with ReadFrame, and Stops it.
// Implementations need not be reusable across Start/Stop cycles.
type VideoSource interface {
	// Start begins producing frames. Cancelling ctx must stop the source and
	// cause any blocked ReadFrame to return an error.
	Start(ctx context.Context) error
	// ReadFrame returns the next complete encoded access unit, or an error
	// (io.EOF when the source ends cleanly).
	ReadFrame() (Frame, error)
	// Stop releases the source's resources. The Broadcaster calls it once
	// reading has stopped, so implementations can safely block to reap.
	Stop() error
}

// SourceFunc builds a fresh VideoSource for a single run. The Broadcaster calls
// it each time the source needs to (re)start — when the first viewer connects
// and on auto-restart after a crash — so every run gets clean state with no
// leftovers from a previous run.
type SourceFunc func() VideoSource

// Frame is one complete encoded video access unit together with how long it
// should be displayed.
//
// Data holds a single H.264 access unit in Annex-B form — every NAL unit it
// contains is prefixed with a 0x00000001 start code — which is exactly what a
// WebRTC track expects as one sample.
type Frame struct {
	Data     []byte
	Duration time.Duration
}
