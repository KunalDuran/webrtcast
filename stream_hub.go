package webrtcstream

import (
	"log"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

type StreamHub struct {
	source  VideoSource
	viewers map[string]*webrtc.TrackLocalStaticSample
	mu      sync.RWMutex
}

func NewStreamHub(source VideoSource) *StreamHub {
	return &StreamHub{
		source:  source,
		viewers: make(map[string]*webrtc.TrackLocalStaticSample),
	}
}

func (h *StreamHub) AddViewer(
	id string,
	track *webrtc.TrackLocalStaticSample,
) {

	h.mu.Lock()
	defer h.mu.Unlock()

	h.viewers[id] = track

	log.Println("viewer added:", id)
}

func (h *StreamHub) RemoveViewer(id string) {

	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.viewers, id)

	log.Println("viewer removed:", id)
}

func (h *StreamHub) Start() {

	err := h.source.Start()
	if err != nil {
		panic(err)
	}

	defer h.source.Stop()

	log.Println("stream hub started")

	for {

		packet, err := h.source.ReadPacket()
		if err != nil {
			log.Println(err)
			return
		}

		h.mu.RLock()

		for id, track := range h.viewers {

			err = track.WriteSample(media.Sample{
				Data:     packet,
				Duration: time.Millisecond * 33,
			})

			if err != nil {
				log.Println("write failed:", id, err)
			}
		}

		h.mu.RUnlock()
	}
}
