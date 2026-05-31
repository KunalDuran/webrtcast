# webrtcast

A small Go module for broadcasting H.264 video to many WebRTC viewers. A
`VideoSource` (e.g. FFmpeg) produces packets, and a `Broadcaster` fans them out
to every connected viewer.

- **Lazy** — the source starts on the first viewer and stops when the last one
  leaves, so nothing is encoded while nobody is watching.
- **Transport-agnostic** — the library only deals in SDP strings. Signalling
  (websocket, HTTP, MQTT, ...) is entirely up to you.

## Install

```sh
go get github.com/KunalDuran/webrtcast
```

> The module path is set in `go.mod`. If you host this somewhere else, change
> the `module` line in `go.mod` and the import in `cmd/webrtcast/main.go`.

## Use as a library

```go
import "github.com/KunalDuran/webrtcast"

source := webrtcast.NewFFmpegSource()  // or your own VideoSource
caster := webrtcast.New(source)
defer caster.Close()

// When an offer arrives over your own signalling channel, answer it.
// The returned SDP answer is self-contained (ICE already gathered) —
// send it back however you like.
answer, err := caster.Connect(offerSDP)
```

Custom STUN/TURN servers:

```go
caster := webrtcast.New(source, webrtcast.WithICEServers([]webrtc.ICEServer{
	{URLs: []string{"stun:stun.l.google.com:19302"}},
	{URLs: []string{"turn:turn.example.com"}, Username: "u", Credential: "p"},
}))
```

Plug in your own video source by implementing `VideoSource`:

```go
type VideoSource interface {
	Start() error
	ReadFrame() (Frame, error) // one complete H.264 access unit + its duration
	Stop() error
}
```

## Run the example

The `cmd/webrtcast` program wires the library to a websocket signalling server.
It needs `ffmpeg` on your `PATH`.

```sh
go run ./cmd/webrtcast -signal "ws://localhost:8080/ws?topic=signal"
```

## Layout

```
.                       library package "webrtcast" (no transport deps)
├── video_source.go     VideoSource interface
├── ffmpeg.go           FFmpegSource implementation
├── broadcaster.go      Broadcaster: lazy source + fan-out + SDP answering
└── cmd/webrtcast/      runnable websocket example
```
