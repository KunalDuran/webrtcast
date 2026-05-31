# webrtcast

A small Go module for streaming H.264 video to WebRTC viewers. A `VideoSource`
(e.g. FFmpeg) produces packets, and a `StreamHub` fans them out to every
connected viewer.

## Install

```sh
go get github.com/KunalDuran/webrtcast
```

> The module path is set in `go.mod`. If you host this somewhere else, change
> the `module` line in `go.mod` and the import in `cmd/webrtcast/main.go`.

## Use as a library

```go
import webrtcstream "github.com/KunalDuran/webrtcast"

source := webrtcstream.NewFFmpegSource() // or your own VideoSource
hub := webrtcstream.NewStreamHub(source)
go hub.Start()

// When an offer arrives over your signalling websocket:
err := webrtcstream.HandleOffer(conn, offerSDP, hub, nil)
```

Implement the `VideoSource` interface to plug in your own video source:

```go
type VideoSource interface {
	Start() error
	ReadPacket() ([]byte, error)
	Stop() error
}
```

## Run the example

The `cmd/webrtcast` program wires the library to a signalling server.
It needs `ffmpeg` on your `PATH`.

```sh
go run ./cmd/webrtcast -signal "ws://localhost:8080/ws?topic=signal"
```

## Layout

```
.                       library package "webrtcstream"
├── video_source.go     VideoSource interface
├── ffmpeg.go           FFmpegSource implementation
├── stream_hub.go       StreamHub: fans video out to viewers
├── streamer.go         single-track streaming helper
├── signaling.go        SignalMessage + HandleOffer
└── cmd/webrtcast/  runnable example program
```
