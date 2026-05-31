package webrtcast

import "time"

// VideoSource is anything that can produce a stream of encoded video frames
// (for example an FFmpeg process or a camera). Implement this interface to feed
// your own source into a Broadcaster.
type VideoSource interface {
	Start() error
	ReadFrame() (Frame, error)
	Stop() error
}

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
