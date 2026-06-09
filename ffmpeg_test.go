package webrtcast

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/pion/webrtc/v4/pkg/media/h264reader"
)

// nal builds a NAL unit: a header byte (which encodes the type in its low 5
// bits) followed by an arbitrary payload.
func nal(header byte, payload ...byte) []byte {
	return append([]byte{header}, payload...)
}

// annexB joins NAL units into an Annex-B stream with 4-byte start codes.
func annexB(nals ...[]byte) []byte {
	var buf bytes.Buffer
	for _, n := range nals {
		buf.Write([]byte{0x00, 0x00, 0x00, 0x01})
		buf.Write(n)
	}
	return buf.Bytes()
}

// newTestSource wires a CommandSource to read from an in-memory stream instead
// of a real encoder process.
func newTestSource(t *testing.T, stream []byte) *CommandSource {
	t.Helper()
	r, err := h264reader.NewReader(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("new reader: %v", err)
	}
	return &CommandSource{fps: 30, reader: r}
}

func TestReadFrameGroupsAccessUnits(t *testing.T) {
	// Header bytes: SPS=0x67, PPS=0x68, IDR slice=0x65, non-IDR slice=0x41.
	stream := annexB(
		nal(0x67, 'S'), // AU1: SPS
		nal(0x68, 'P'), //      PPS
		nal(0x65, 'I'), //      IDR slice
		nal(0x41, 'a'), // AU2: non-IDR slice
		nal(0x41, 'b'), // AU3: non-IDR slice (lost at EOF, by design)
	)
	src := newTestSource(t, stream)

	// First frame: the parameter sets must travel with the IDR they precede,
	// each NAL carrying its own start code.
	first, err := src.ReadFrame()
	if err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	want := annexB(nal(0x67, 'S'), nal(0x68, 'P'), nal(0x65, 'I'))
	if !bytes.Equal(first.Data, want) {
		t.Errorf("frame 1 data = %v, want %v", first.Data, want)
	}
	if first.Duration != time.Second/30 {
		t.Errorf("frame 1 duration = %v, want %v", first.Duration, time.Second/30)
	}

	// Second frame: just the AU2 slice on its own.
	second, err := src.ReadFrame()
	if err != nil {
		t.Fatalf("frame 2: %v", err)
	}
	if want := annexB(nal(0x41, 'a')); !bytes.Equal(second.Data, want) {
		t.Errorf("frame 2 data = %v, want %v", second.Data, want)
	}

	// The trailing access unit has no following picture to flush it, so the
	// stream ends.
	if _, err := src.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Errorf("frame 3 err = %v, want EOF", err)
	}
}
