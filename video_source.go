package webrtcstream

// VideoSource is anything that can produce a stream of encoded video packets
// (for example an FFmpeg process or a camera). Implement this interface to feed
// your own source into a StreamHub.
type VideoSource interface {
	Start() error
	ReadPacket() ([]byte, error)
	Stop() error
}
