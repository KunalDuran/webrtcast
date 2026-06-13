# webrtcast

A small Go module for broadcasting H.264 video to many WebRTC viewers. A
`VideoSource` (e.g. FFmpeg) produces packets, and a `Broadcaster` fans them out
to every connected viewer.

- **Lazy** — the source starts on the first viewer and stops when the last one
  leaves, so nothing is encoded while nobody is watching.
- **Self-healing** — if the source crashes while viewers are still connected,
  it is rebuilt and restarted with exponential backoff; register
  `OnSourceError` for telemetry.
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

// New takes a factory: a fresh source is built for each run (first viewer, or
// auto-restart after a crash), so every run starts from clean state.
caster := webrtcast.New(func() webrtcast.VideoSource {
	return webrtcast.NewFFmpegSource() // or your own VideoSource
})
defer caster.Close()

// When an offer arrives over your own signalling channel, answer it. The ctx
// bounds ICE gathering so a flaky/unreachable STUN server can't hang you. The
// returned SDP answer is self-contained (ICE already gathered) — send it back
// however you like.
answer, err := caster.Connect(ctx, offerSDP)
```

Custom STUN/TURN servers, a logger, and a source-error hook are set via
options:

```go
caster := webrtcast.New(newSource,
	webrtcast.WithICEServers([]webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		{URLs: []string{"turn:turn.example.com"}, Username: "u", Credential: "p"},
	}),
	webrtcast.WithLogger(slog.Default()),
	webrtcast.OnSourceError(func(err error) { /* alert/telemetry */ }),
)
```

Plug in your own video source by implementing `VideoSource`. It is single-use —
the broadcaster builds a fresh one per run, so it need not be reusable across
`Start`/`Stop`:

```go
type VideoSource interface {
	Start(ctx context.Context) error // cancelling ctx must stop it
	ReadFrame() (Frame, error)       // one complete H.264 access unit + its duration
	Stop() error                     // release/reap resources
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
├── video_source.go     VideoSource interface + SourceFunc factory + Frame
├── command_source.go   CommandSource: run an encoder, parse Annex-B into AUs
├── ffmpeg.go           NewFFmpegSource preset
├── picamera.go         NewPiCameraSource preset (rpicam-vid hardware encode)
├── broadcaster.go      Broadcaster: lazy run + fan-out + SDP answering + restart
└── cmd/webrtcast/      runnable websocket example
```
