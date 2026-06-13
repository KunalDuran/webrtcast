package webrtcast

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// fakeSource is a controllable VideoSource for exercising the Broadcaster's run
// lifecycle without a real encoder process.
type fakeSource struct {
	started    chan struct{}
	stopCalled chan struct{}
	frames     chan Frame
	crash      chan error
	stopOnce   sync.Once
	ctx        context.Context
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		started:    make(chan struct{}),
		stopCalled: make(chan struct{}),
		frames:     make(chan Frame),
		crash:      make(chan error, 1),
	}
}

func (f *fakeSource) Start(ctx context.Context) error {
	f.ctx = ctx
	close(f.started)
	return nil
}

func (f *fakeSource) ReadFrame() (Frame, error) {
	select {
	case fr := <-f.frames:
		return fr, nil
	case err := <-f.crash:
		return Frame{}, err
	case <-f.ctx.Done():
		return Frame{}, f.ctx.Err()
	}
}

func (f *fakeSource) Stop() error {
	f.stopOnce.Do(func() { close(f.stopCalled) })
	return nil
}

// newViewer builds a viewer backed by a real (offline) peer connection, so
// removeViewer's pc.Close() has something to close.
func newViewer(t *testing.T) *viewer {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("new peer connection: %v", err)
	}
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "camera",
	)
	if err != nil {
		t.Fatalf("new track: %v", err)
	}
	return &viewer{pc: pc, track: track}
}

func waitClosed(t *testing.T, ch chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

// TestBroadcasterStartStopRestart covers the lifecycle that produced the
// original races: lazy start on the first viewer, reap on the last viewer's
// departure, and auto-restart (a fresh source) after a mid-run crash.
func TestBroadcasterStartStopRestart(t *testing.T) {
	sources := make(chan *fakeSource, 8)
	b := New(func() VideoSource {
		s := newFakeSource()
		sources <- s
		return s
	}, WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))

	// Nothing runs until a viewer arrives.
	if got := b.Viewers(); got != 0 {
		t.Fatalf("viewers before connect = %d, want 0", got)
	}

	// First viewer starts the source.
	if err := b.addViewer("v1", newViewer(t)); err != nil {
		t.Fatalf("addViewer: %v", err)
	}
	s1 := <-sources
	waitClosed(t, s1.started, "source 1 start")

	// Last viewer leaving cancels the run and the pump reaps the source.
	b.removeViewer("v1")
	waitClosed(t, s1.stopCalled, "source 1 stop/reap")

	// A new viewer builds a brand-new source (single-use per run).
	if err := b.addViewer("v2", newViewer(t)); err != nil {
		t.Fatalf("addViewer after restart: %v", err)
	}
	s2 := <-sources
	if s2 == s1 {
		t.Fatal("expected a fresh source instance for the new run")
	}
	waitClosed(t, s2.started, "source 2 start")

	// A mid-run crash, with the viewer still connected, auto-restarts: the old
	// source is reaped and a fresh one is built and started.
	s2.crash <- errors.New("boom")
	waitClosed(t, s2.stopCalled, "source 2 reap after crash")

	s3 := <-sources
	waitClosed(t, s3.started, "source 3 start after restart")

	// Close stops the active run and reaps the current source.
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitClosed(t, s3.stopCalled, "source 3 stop on Close")
}
