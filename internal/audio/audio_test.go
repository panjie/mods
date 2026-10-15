package audio

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/testutil/mediafixture"
	"github.com/stretchr/testify/require"
)

func TestPCMChunksAndMP3VBR(t *testing.T) {
	a, err := Parse(mediafixture.WAV(2))
	require.NoError(t, err)
	require.EqualValues(t, 2000, a.Audio.DurationMillis)
	require.Equal(t, 16000, a.Audio.SampleRate)
	a, err = Parse(mediafixture.MP3Frames())
	require.NoError(t, err)
	require.EqualValues(t, 79, a.Audio.DurationMillis)
	require.Equal(t, "mp3", a.Audio.Container)
	path := filepath.Join(t.TempDir(), "伪扩展名.png")
	require.NoError(t, os.WriteFile(path, mediafixture.WAV(1), 0600))
	a, err = Read(path)
	require.NoError(t, err)
	require.Equal(t, "audio/wav", a.MimeType)
}
func TestRejectInvalidAudio(t *testing.T) {
	wav := mediafixture.WAV(1)
	mp3 := mediafixture.MP3Frames()
	badRate := append([]byte(nil), wav...)
	binary.LittleEndian.PutUint32(badRate[38:], 0)
	for _, data := range [][]byte{nil, []byte("not audio"), wav[:len(wav)-1], mp3[:len(mp3)-1], badRate, make([]byte, MaxBytes+1), append(wav, 1)} {
		_, err := Parse(data)
		require.Error(t, err)
	}
	_, err := Read(t.TempDir())
	require.Error(t, err)
}
