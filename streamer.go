package webrtcstream

import (
	"log"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

func StreamToWebRTC(
	source VideoSource,
	track *webrtc.TrackLocalStaticSample,
) {

	err := source.Start()
	if err != nil {
		panic(err)
	}

	defer source.Stop()

	log.Println("Video source started")

	for {

		packet, err := source.ReadPacket()
		if err != nil {
			log.Println(err)
			return
		}

		err = track.WriteSample(media.Sample{
			Data:     packet,
			Duration: time.Millisecond * 33,
		})

		if err != nil {
			log.Println(err)
		}
	}
}
