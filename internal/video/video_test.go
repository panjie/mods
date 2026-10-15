package video

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/testutil/mediafixture"
	"github.com/stretchr/testify/require"
)

func TestMP4AudioVideo(t *testing.T) {
	data := mediafixture.Video()
	a, err := Parse(context.Background(), data)
	require.NoError(t, err)
	require.True(t, a.Video.HasAudio)
	require.Equal(t, "h264", a.Video.VideoCodec)
	require.Equal(t, 480, a.Video.Width)
}

func TestSilentVideoAndLateMoov(t *testing.T) {
	data := mediafixture.SilentVideo()
	require.Greater(t, bytes.Index(data, []byte("moov")), bytes.Index(data, []byte("mdat")))
	a, err := Parse(context.Background(), data)
	require.NoError(t, err)
	require.False(t, a.Video.HasAudio)
	require.EqualValues(t, 2000, a.Video.DurationMillis)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Parse(ctx, data)
	require.ErrorIs(t, err, context.Canceled)
	path := filepath.Join(t.TempDir(), "视频 空格.bin")
	require.NoError(t, os.WriteFile(path, data, 0600))
	a, err = Read(context.Background(), path)
	require.NoError(t, err)
	require.Equal(t, "video/mp4", a.MimeType)
	_, err = Read(context.Background(), t.TempDir())
	require.Error(t, err)
}

func TestRejectMalformedOrUnsupportedMP4(t *testing.T) {
	original := mediafixture.Video()
	mutate := func(typ string, offset int, value uint32) []byte {
		b := bytes.Clone(original)
		i := bytes.Index(b, []byte(typ))
		require.Positive(t, i)
		binary.BigEndian.PutUint32(b[i+offset:], value)
		return b
	}
	cases := map[string][]byte{
		"empty": nil, "junk": []byte("not MP4"), "oversize": make([]byte, MaxBytes+1), "truncated": original[:len(original)-30],
		"box overflow": mutate("moov", -4, 0xffffffff), "zero timescale": mutate("mdhd", 16, 0),
		"unknown duration": mutate("mdhd", 20, 0xffffffff), "outside chunk": mutate("stco", 12, 0xffffffff),
		"huge sample count": mutate("stsz", 12, 0xffffffff), "no sample description": mutate("stsd", 8, 0),
		"encrypted":  bytes.Replace(original, []byte("avc1"), []byte("encv"), -1),
		"fragmented": bytes.Replace(original, []byte("moov"), []byte("moof"), 1),
		"audio only": bytes.Replace(original, []byte("vide"), []byte("soun"), 1),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) { _, err := Parse(context.Background(), b); require.Error(t, err) })
	}
}

func TestVideoRotation(t *testing.T) {
	data := mediafixture.Video()
	i := bytes.Index(data, []byte("tkhd"))
	require.Positive(t, i)
	// Version-zero tkhd matrix starts 44 bytes after the type field.
	for j, v := range []uint32{0, 65536, 0, 0xffff0000, 0, 0, 0, 0, 0x40000000} {
		binary.BigEndian.PutUint32(data[i+44+j*4:], v)
	}
	a, err := Parse(context.Background(), data)
	require.NoError(t, err)
	require.Equal(t, 90, a.Video.Rotation)
	require.Equal(t, 80, a.Video.Width)
	require.Equal(t, 480, a.Video.Height)
}

func FuzzParseMP4(f *testing.F) {
	f.Add(mediafixture.Video())
	f.Add(mediafixture.SilentVideo())
	f.Add([]byte("invalid"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxBytes {
			return
		}
		_, _ = Parse(context.Background(), data)
	})
}
