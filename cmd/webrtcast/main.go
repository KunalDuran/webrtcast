// Command webrtcast is an example program showing how to use the webrtcast
// library: it streams an FFmpeg test pattern to any WebRTC viewer that connects
// through a signalling server.
//
// The signalling here uses a websocket, but that is purely the example's
// choice — the library only deals in SDP strings, so you can wire it to HTTP,
// gRPC, MQTT, or anything else.
package main

import (
	"encoding/json"
	"flag"
	"log"

	"github.com/gorilla/websocket"

	"github.com/KunalDuran/webrtcast"
)

// signalMessage is the JSON envelope this example exchanges with the signalling
// server. Your own protocol can look however you like.
type signalMessage struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

func main() {
	signalURL := flag.String(
		"signal",
		"ws://YOUR_SIGNAL_SERVER/ws?topic=signal",
		"signalling server websocket URL",
	)
	flag.Parse()

	// The source is started lazily by the broadcaster on the first viewer, so
	// nothing is encoded until someone actually connects.
	source := webrtcast.NewFFmpegSource()
	caster := webrtcast.New(source)
	defer caster.Close()

	conn, _, err := websocket.DefaultDialer.Dial(*signalURL, nil)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("connected to signalling server")

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Fatal(err)
		}

		var sig signalMessage
		if err := json.Unmarshal(msg, &sig); err != nil {
			log.Println(err)
			continue
		}
		if sig.Type != "offer" {
			continue
		}
		log.Println("received offer")

		// Hand the offer to the library and get back an answer to send on.
		answer, err := caster.Connect(sig.SDP)
		if err != nil {
			log.Println("connect:", err)
			continue
		}

		reply, _ := json.Marshal(signalMessage{Type: "answer", SDP: answer})
		if err := conn.WriteMessage(websocket.TextMessage, reply); err != nil {
			log.Println("send answer:", err)
		}
	}
}
