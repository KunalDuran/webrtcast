package webrtcast

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Broadcaster fans a single VideoSource out to many WebRTC viewers.
//
// The video source is started lazily when the first viewer connects and
// stopped again once the last viewer disconnects, so nothing is encoded while
// nobody is watching.
//
// Broadcaster is transport-agnostic: it only deals in SDP strings. How offers
// and answers travel between you and the viewer (websocket, HTTP, ...) is left
// entirely to the caller.
type Broadcaster struct {
	source     VideoSource
	iceServers []webrtc.ICEServer

	mu      sync.Mutex
	viewers map[string]*viewer
	running bool   // is the source started and being pumped?
	gen     uint64 // identifies the currently running pump goroutine
}

// viewer is one connected WebRTC peer and the track we write video into.
type viewer struct {
	pc    *webrtc.PeerConnection
	track *webrtc.TrackLocalStaticSample
}

// Option configures a Broadcaster.
type Option func(*Broadcaster)

// WithICEServers sets the ICE (STUN/TURN) servers offered to viewers.
// If unset, Google's public STUN server is used.
func WithICEServers(servers []webrtc.ICEServer) Option {
	return func(b *Broadcaster) { b.iceServers = servers }
}

// New creates a Broadcaster for the given video source.
func New(source VideoSource, opts ...Option) *Broadcaster {
	b := &Broadcaster{
		source:  source,
		viewers: make(map[string]*viewer),
		iceServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

var viewerCounter uint64

// Connect answers a viewer's SDP offer and returns the SDP answer.
//
// The viewer begins receiving video immediately and is removed automatically
// when its connection closes. The returned answer already contains its ICE
// candidates (non-trickle), so you can send it as-is over any transport.
func (b *Broadcaster) Connect(offerSDP string) (answerSDP string, err error) {
	id := fmt.Sprintf("viewer-%d", atomic.AddUint64(&viewerCounter, 1))

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: b.iceServers})
	if err != nil {
		return "", fmt.Errorf("create peer connection: %w", err)
	}
	// On any failure after this point, make sure we don't leak the connection.
	defer func() {
		if err != nil {
			pc.Close()
		}
	}()

	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "camera",
	)
	if err != nil {
		return "", fmt.Errorf("create video track: %w", err)
	}
	if _, err = pc.AddTrack(track); err != nil {
		return "", fmt.Errorf("add track: %w", err)
	}

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Println("viewer", id, "state:", state)
		switch state {
		case webrtc.PeerConnectionStateClosed,
			webrtc.PeerConnectionStateDisconnected,
			webrtc.PeerConnectionStateFailed:
			b.removeViewer(id)
		}
	})

	if err = pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerSDP,
	}); err != nil {
		return "", fmt.Errorf("set remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("create answer: %w", err)
	}

	// Wait for ICE gathering to finish so the answer is self-contained and can
	// be sent in a single message (non-trickle ICE).
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("set local description: %w", err)
	}
	<-gatherComplete

	if err = b.addViewer(id, &viewer{pc: pc, track: track}); err != nil {
		return "", fmt.Errorf("start source: %w", err)
	}

	log.Println("viewer", id, "connected")
	return pc.LocalDescription().SDP, nil
}

// Viewers returns the number of currently connected viewers.
func (b *Broadcaster) Viewers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.viewers)
}

// Close disconnects all viewers and stops the video source.
func (b *Broadcaster) Close() error {
	b.mu.Lock()
	pcs := make([]*webrtc.PeerConnection, 0, len(b.viewers))
	for id, v := range b.viewers {
		pcs = append(pcs, v.pc)
		delete(b.viewers, id)
	}
	if b.running {
		b.stopSourceLocked()
	}
	b.mu.Unlock()

	// Close peer connections outside the lock: closing fires the state-change
	// callback, which calls removeViewer and would otherwise deadlock.
	for _, pc := range pcs {
		pc.Close()
	}
	return nil
}

// addViewer registers a viewer, starting the source if it is the first one.
func (b *Broadcaster) addViewer(id string, v *viewer) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		if err := b.source.Start(); err != nil {
			return err
		}
		b.running = true
		b.gen++
		go b.pump(b.gen)
		log.Println("source started")
	}

	b.viewers[id] = v
	return nil
}

// removeViewer drops a viewer, stopping the source if it was the last one.
func (b *Broadcaster) removeViewer(id string) {
	b.mu.Lock()
	v, ok := b.viewers[id]
	if !ok {
		b.mu.Unlock()
		return
	}
	delete(b.viewers, id)
	if len(b.viewers) == 0 && b.running {
		b.stopSourceLocked()
	}
	b.mu.Unlock()

	v.pc.Close() // outside the lock — see Close.
	log.Println("viewer", id, "removed")
}

// stopSourceLocked stops the source and invalidates the running pump.
// The caller must hold b.mu.
func (b *Broadcaster) stopSourceLocked() {
	b.running = false
	b.gen++ // make the current pump exit quietly on its next loop
	if err := b.source.Stop(); err != nil {
		log.Println("stop source:", err)
	}
	log.Println("source stopped (no viewers)")
}

// pump reads frames from the source and writes them to every viewer until the
// source ends or this pump is superseded (gen no longer matches).
func (b *Broadcaster) pump(gen uint64) {
	for {
		frame, err := b.source.ReadFrame()
		if err != nil {
			b.mu.Lock()
			if gen == b.gen { // the source ended on its own (e.g. crashed)
				b.running = false
				log.Println("source ended:", err)
			}
			b.mu.Unlock()
			return
		}

		b.mu.Lock()
		if gen != b.gen { // superseded by stopSourceLocked
			b.mu.Unlock()
			return
		}
		for id, v := range b.viewers {
			if err := v.track.WriteSample(media.Sample{
				Data:     frame.Data,
				Duration: frame.Duration,
			}); err != nil {
				log.Println("write to", id, "failed:", err)
			}
		}
		b.mu.Unlock()
	}
}
