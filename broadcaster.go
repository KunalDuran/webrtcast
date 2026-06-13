package webrtcast

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// Restart backoff bounds used when a source crashes while viewers remain.
const (
	initialRestartBackoff = 500 * time.Millisecond
	maxRestartBackoff     = 30 * time.Second
)

// Broadcaster fans a single video source out to many WebRTC viewers.
//
// The source is built and started lazily when the first viewer connects (via
// the SourceFunc passed to New) and stopped again once the last viewer
// disconnects, so nothing is encoded while nobody is watching. If the source
// crashes while viewers are still connected, it is rebuilt and restarted with
// exponential backoff.
//
// Broadcaster is transport-agnostic: it only deals in SDP strings. How offers
// and answers travel between you and the viewer (websocket, HTTP, ...) is left
// entirely to the caller.
type Broadcaster struct {
	// viewerSeq is accessed with atomic.AddUint64 and MUST stay the first field
	// of the struct: on 32-bit platforms (e.g. ARM) Go only guarantees 64-bit
	// alignment for the first word of an allocated struct, and an unaligned
	// 64-bit atomic op panics. Keep it at the top.
	viewerSeq uint64

	newSource     SourceFunc
	iceServers    []webrtc.ICEServer
	logger        *slog.Logger
	onSourceError func(error)

	mu      sync.Mutex
	viewers map[string]*viewer
	running bool               // is a source run active?
	gen     uint64             // identifies the currently running pump (mutex-guarded, not atomic)
	cancel  context.CancelFunc // cancels the active run's source context
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

// WithLogger sets the logger. If unset, slog.Default() is used. Pass a logger
// at a higher level (or a discarding handler) to silence the per-viewer
// state-change chatter.
func WithLogger(logger *slog.Logger) Option {
	return func(b *Broadcaster) {
		if logger != nil {
			b.logger = logger
		}
	}
}

// OnSourceError registers a callback invoked whenever the source fails — either
// crashing mid-run or failing to restart. The Broadcaster keeps trying to
// restart regardless; this hook is for telemetry/alerting. The callback runs on
// the run goroutine, so it must not block.
func OnSourceError(fn func(error)) Option {
	return func(b *Broadcaster) { b.onSourceError = fn }
}

// New creates a Broadcaster that builds a fresh video source, via newSource,
// each time a run starts (first viewer, or restart after a crash).
func New(newSource SourceFunc, opts ...Option) *Broadcaster {
	b := &Broadcaster{
		newSource: newSource,
		viewers:   make(map[string]*viewer),
		logger:    slog.Default(),
		iceServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Connect answers a viewer's SDP offer and returns the SDP answer.
//
// The viewer begins receiving video immediately and is removed automatically
// when its connection closes. The returned answer already contains its ICE
// candidates (non-trickle), so you can send it as-is over any transport.
//
// ctx bounds ICE gathering: if it expires (e.g. an unreachable STUN server on a
// flaky link) Connect returns rather than hanging the caller's signalling loop.
func (b *Broadcaster) Connect(ctx context.Context, offerSDP string) (answerSDP string, err error) {
	id := fmt.Sprintf("viewer-%d", atomic.AddUint64(&b.viewerSeq, 1))

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
		b.logger.Info("viewer state changed", "viewer", id, "state", state.String())
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
	// be sent in a single message (non-trickle ICE), but don't wait forever.
	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err = pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("set local description: %w", err)
	}
	select {
	case <-gatherComplete:
	case <-ctx.Done():
		return "", fmt.Errorf("ice gathering: %w", ctx.Err())
	}

	if err = b.addViewer(id, &viewer{pc: pc, track: track}); err != nil {
		return "", fmt.Errorf("start source: %w", err)
	}

	// The connection may have failed or closed during gathering/setup, before
	// the state-change callback could find this viewer in the map. If so its
	// removeViewer call no-op'd; drop the now-orphaned viewer ourselves so the
	// source does not run on for a peer that is already gone.
	if state := pc.ConnectionState(); state == webrtc.PeerConnectionStateFailed ||
		state == webrtc.PeerConnectionStateClosed ||
		state == webrtc.PeerConnectionStateDisconnected {
		b.removeViewer(id)
		err = fmt.Errorf("connection %s before setup completed", state)
		return "", err
	}

	b.logger.Info("viewer connected", "viewer", id)
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
		b.stopRunLocked()
	}
	b.mu.Unlock()

	// Close peer connections outside the lock: closing fires the state-change
	// callback, which calls removeViewer and would otherwise deadlock.
	for _, pc := range pcs {
		pc.Close()
	}
	return nil
}

// addViewer registers a viewer, starting a source run if it is the first one.
func (b *Broadcaster) addViewer(id string, v *viewer) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running {
		if err := b.startRunLocked(); err != nil {
			return err
		}
	}
	b.viewers[id] = v
	return nil
}

// removeViewer drops a viewer, stopping the source run if it was the last one.
func (b *Broadcaster) removeViewer(id string) {
	b.mu.Lock()
	v, ok := b.viewers[id]
	if !ok {
		b.mu.Unlock()
		return
	}
	delete(b.viewers, id)
	if len(b.viewers) == 0 && b.running {
		b.stopRunLocked()
	}
	b.mu.Unlock()

	v.pc.Close() // outside the lock — see Close.
	b.logger.Info("viewer removed", "viewer", id)
}

// startRunLocked builds a fresh source, starts it, and launches the run
// goroutine that pumps it. The caller must hold b.mu. The first start is
// synchronous so Connect surfaces an immediate failure (e.g. ffmpeg not on
// PATH); later restarts happen inside the run goroutine.
func (b *Broadcaster) startRunLocked() error {
	ctx, cancel := context.WithCancel(context.Background())
	src := b.newSource()
	if err := src.Start(ctx); err != nil {
		cancel()
		return err
	}
	b.running = true
	b.gen++
	b.cancel = cancel
	go b.run(ctx, src, b.gen)
	b.logger.Info("source started")
	return nil
}

// stopRunLocked signals the active run to stop. The caller must hold b.mu.
//
// It only cancels the run's context — which kills the source process and
// unblocks ReadFrame — and bumps gen so any in-flight pump write is discarded.
// The run goroutine owns the actual teardown (reaping the process via
// src.Stop), so there is never concurrent teardown of the same source.
func (b *Broadcaster) stopRunLocked() {
	b.running = false
	b.gen++ // make the current pump discard any write it is mid-way through
	if b.cancel != nil {
		b.cancel()
		b.cancel = nil
	}
	b.logger.Info("source stopping (no viewers)")
}

// run owns one source's lifetime: it pumps frames until the source ends, reaps
// it, and — unless the run was stopped on purpose — rebuilds and restarts it
// with exponential backoff. gen ties this run's writes to the Broadcaster; once
// superseded, its writes are dropped.
func (b *Broadcaster) run(ctx context.Context, src VideoSource, gen uint64) {
	backoff := initialRestartBackoff
	for {
		err := b.pump(ctx, src, gen)
		src.Stop() // reap the process now that reading has stopped

		// pump returns nil only when this run was superseded (intentional stop);
		// a cancelled context is likewise an intentional stop.
		if err == nil || ctx.Err() != nil {
			b.logger.Info("source stopped")
			return
		}

		// The source ended on its own — treat as a crash and restart.
		b.logger.Error("source ended unexpectedly; restarting", "err", err)
		b.notifySourceError(err)

		// Back off, then build and start a fresh source. Retry the start itself
		// with backoff too, since the failure may be transient (camera busy).
		for {
			select {
			case <-ctx.Done():
				b.logger.Info("source stopped")
				return
			case <-time.After(backoff):
			}
			backoff = nextBackoff(backoff)

			src = b.newSource()
			if startErr := src.Start(ctx); startErr != nil {
				b.logger.Error("source restart failed", "err", startErr)
				b.notifySourceError(startErr)
				continue
			}
			break
		}
		backoff = initialRestartBackoff // reset once a restart succeeds
	}
}

// pump reads frames from src and writes them to every viewer until src ends
// (returns the error) or this run is superseded (returns nil).
//
// Frames are written outside b.mu: it snapshots the current tracks under the
// lock, releases it, then packetizes and writes. Holding the lock across N
// WriteSample calls every frame would block Connect and removeViewer for the
// duration of every frame's fan-out.
func (b *Broadcaster) pump(ctx context.Context, src VideoSource, gen uint64) error {
	for {
		frame, err := src.ReadFrame()
		if err != nil {
			return err
		}

		b.mu.Lock()
		if gen != b.gen { // superseded by stopRunLocked / a newer run
			b.mu.Unlock()
			return nil
		}
		tracks := make([]*webrtc.TrackLocalStaticSample, 0, len(b.viewers))
		ids := make([]string, 0, len(b.viewers))
		for id, v := range b.viewers {
			tracks = append(tracks, v.track)
			ids = append(ids, id)
		}
		b.mu.Unlock()

		sample := media.Sample{Data: frame.Data, Duration: frame.Duration}
		for i, t := range tracks {
			if err := t.WriteSample(sample); err != nil {
				// A track whose pc just closed errors here; removeViewer will
				// clear it shortly. Log and keep serving everyone else.
				b.logger.Warn("write to viewer failed", "viewer", ids[i], "err", err)
			}
		}
	}
}

// notifySourceError invokes the OnSourceError hook if one was registered.
func (b *Broadcaster) notifySourceError(err error) {
	if b.onSourceError != nil {
		b.onSourceError(err)
	}
}

// nextBackoff doubles d, capped at maxRestartBackoff.
func nextBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > maxRestartBackoff {
		return maxRestartBackoff
	}
	return d
}
