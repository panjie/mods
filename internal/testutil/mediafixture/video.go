package mediafixture

import (
	"bytes"
	_ "embed"
)

// Generated non-private fixtures: video.mp4 contains rendered text and synthetic
// speech with different amounts; silent.mp4 is a blue frame without an audio
// track and stores moov after mdat. Neither requires external programs in tests.
//
//go:embed video.mp4
var video []byte

//go:embed silent.mp4
var silentVideo []byte

func Video() []byte       { return bytes.Clone(video) }
func SilentVideo() []byte { return bytes.Clone(silentVideo) }
