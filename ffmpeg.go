package webrtcstream

import (
	"bufio"
	"os/exec"
)

type FFmpegSource struct {
	cmd    *exec.Cmd
	reader *bufio.Reader
}

func NewFFmpegSource() *FFmpegSource {
	return &FFmpegSource{}
}

func (f *FFmpegSource) Start() error {

	f.cmd = exec.Command(
		"ffmpeg",
		"-re",
		"-f", "lavfi",
		"-i", "testsrc=size=640x480:rate=30",
		"-vcodec", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-f", "h264",
		"-",
	)

	stdout, err := f.cmd.StdoutPipe()
	if err != nil {
		return err
	}

	f.reader = bufio.NewReader(stdout)

	return f.cmd.Start()
}

func (f *FFmpegSource) ReadPacket() ([]byte, error) {

	buffer := make([]byte, 1400)

	n, err := f.reader.Read(buffer)
	if err != nil {
		return nil, err
	}

	return buffer[:n], nil
}

func (f *FFmpegSource) Stop() error {

	if f.cmd != nil && f.cmd.Process != nil {
		return f.cmd.Process.Kill()
	}

	return nil
}
