package main

import (
	"encoding/json"
	"flag"
	"log"

	"github.com/gorilla/websocket"

	webrtcstream "github.com/KunalDuran/webrtcast"
)

func main() {

	signalURL := flag.String(
		"signal",
		"ws://YOUR_SIGNAL_SERVER/ws?topic=signal",
		"signalling server websocket URL",
	)
	flag.Parse()

	// Build the video pipeline: an FFmpeg source feeding a hub that fans the
	// video out to every connected viewer.
	source := webrtcstream.NewFFmpegSource()
	hub := webrtcstream.NewStreamHub(source)
	go hub.Start()

	conn, _, err := websocket.DefaultDialer.Dial(*signalURL, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("connected to signalling server")

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println(err)
			continue
		}

		var signal webrtcstream.SignalMessage
		if err := json.Unmarshal(msg, &signal); err != nil {
			log.Println(err)
			continue
		}

		if signal.Type == "offer" {
			log.Println("received offer")
			go func(offerSDP string) {
				// nil ICE servers => library default (Google STUN).
				if err := webrtcstream.HandleOffer(conn, offerSDP, hub, nil); err != nil {
					log.Println("handle offer:", err)
				}
			}(signal.SDP)
		}
	}
}
