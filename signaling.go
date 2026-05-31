package webrtcstream

import (
	"encoding/json"
	"fmt"
	"log"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// SignalMessage is the JSON envelope exchanged with the signalling server.
type SignalMessage struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// viewerCounter gives every viewer a unique id.
var viewerCounter uint64

// HandleOffer answers a single WebRTC offer.
//
// It creates a peer connection, adds a video track, registers that track with
// the hub so the hub starts pushing video to it, then writes the SDP answer
// back over conn. When the connection drops the viewer is removed from the hub
// automatically.
//
// iceServers may be nil, in which case Google's public STUN server is used.
func HandleOffer(
	conn *websocket.Conn,
	offerSDP string,
	hub *StreamHub,
	iceServers []webrtc.ICEServer,
) error {

	viewerID := fmt.Sprintf("viewer-%d", atomic.AddUint64(&viewerCounter, 1))

	if iceServers == nil {
		iceServers = []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		}
	}

	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: iceServers,
	})
	if err != nil {
		return fmt.Errorf("create peer connection: %w", err)
	}

	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video",
		"camera",
	)
	if err != nil {
		return fmt.Errorf("create video track: %w", err)
	}

	if _, err := peerConnection.AddTrack(videoTrack); err != nil {
		return fmt.Errorf("add track: %w", err)
	}

	hub.AddViewer(viewerID, videoTrack)

	peerConnection.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {

		log.Println("state:", viewerID, state.String())

		if state == webrtc.PeerConnectionStateClosed ||
			state == webrtc.PeerConnectionStateDisconnected ||
			state == webrtc.PeerConnectionStateFailed {

			hub.RemoveViewer(viewerID)
			peerConnection.Close()
		}
	})

	err = peerConnection.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerSDP,
	})
	if err != nil {
		return fmt.Errorf("set remote description: %w", err)
	}

	answer, err := peerConnection.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("create answer: %w", err)
	}

	if err := peerConnection.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("set local description: %w", err)
	}

	data, err := json.Marshal(SignalMessage{Type: "answer", SDP: answer.SDP})
	if err != nil {
		return fmt.Errorf("marshal answer: %w", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return fmt.Errorf("send answer: %w", err)
	}

	log.Println("sent answer:", viewerID)
	return nil
}
