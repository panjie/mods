// Package audio validates bounded native audio inputs without external programs.
package audio

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/panjie/mods/internal/proto"
)

const MaxBytes = 6 * 1024 * 1024
const MaxDurationMillis int64 = 10 * 60 * 1000
const MaxTotalDurationMillis int64 = 15 * 60 * 1000

func Read(path string) (proto.MediaAttachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		return proto.MediaAttachment{}, err
	}
	if !info.Mode().IsRegular() {
		return proto.MediaAttachment{}, fmt.Errorf("audio input must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return proto.MediaAttachment{}, err
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return proto.MediaAttachment{}, err
	}
	if !info.Mode().IsRegular() {
		return proto.MediaAttachment{}, fmt.Errorf("audio input must be a regular file")
	}
	if info.Size() > MaxBytes {
		return proto.MediaAttachment{}, fmt.Errorf("audio exceeds 6 MiB inline limit")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return proto.MediaAttachment{}, err
	}
	a, err := Parse(data)
	a.Source = path
	return a, err
}

func Parse(data []byte) (proto.MediaAttachment, error) {
	a := proto.MediaAttachment{Kind: "audio", Data: data}
	if len(data) == 0 || len(data) > MaxBytes {
		return a, fmt.Errorf("audio must contain 1 to 6 MiB of data")
	}
	var err error
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WAVE" {
		a.Audio, err = parseWAV(data)
		a.MimeType = "audio/wav"
	} else {
		a.Audio, err = parseMP3(data)
		a.MimeType = "audio/mpeg"
	}
	if err != nil {
		return a, err
	}
	if a.Audio.DurationMillis <= 0 || a.Audio.DurationMillis > MaxDurationMillis {
		return a, fmt.Errorf("audio duration must be known and at most 10 minutes")
	}
	return a, nil
}

func parseWAV(b []byte) (proto.AudioMetadata, error) {
	m := proto.AudioMetadata{Container: "wav", Codec: "pcm"}
	bad := func() (proto.AudioMetadata, error) { return m, fmt.Errorf("invalid or unsupported PCM WAV") }
	end := int64(binary.LittleEndian.Uint32(b[4:8])) + 8
	if end != int64(len(b)) {
		return bad()
	}
	align, rate, size := 0, 0, 0
	foundFmt, foundData := false, false
	for p := 12; p < len(b); {
		if p+8 > len(b) {
			return bad()
		}
		n := int64(binary.LittleEndian.Uint32(b[p+4 : p+8]))
		start := p + 8
		if n > int64(len(b)-start) {
			return bad()
		}
		chunk := b[start : start+int(n)]
		switch string(b[p : p+4]) {
		case "fmt ":
			if foundFmt || len(chunk) < 16 || binary.LittleEndian.Uint16(chunk[:2]) != 1 {
				return bad()
			}
			foundFmt = true
			m.Channels = int(binary.LittleEndian.Uint16(chunk[2:4]))
			m.SampleRate = int(binary.LittleEndian.Uint32(chunk[4:8]))
			rate = int(binary.LittleEndian.Uint32(chunk[8:12]))
			align = int(binary.LittleEndian.Uint16(chunk[12:14]))
			bits := int(binary.LittleEndian.Uint16(chunk[14:16]))
			if m.Channels < 1 || m.Channels > 8 || m.SampleRate < 8000 || m.SampleRate > 192000 || (bits != 8 && bits != 16 && bits != 24 && bits != 32) || align != m.Channels*bits/8 || rate != align*m.SampleRate {
				return bad()
			}
		case "data":
			if foundData {
				return bad()
			}
			foundData = true
			size = len(chunk)
		}
		p = start + int(n) + int(n%2)
		if p > len(b) {
			return bad()
		}
	}
	if !foundFmt || !foundData || size == 0 || size%align != 0 {
		return bad()
	}
	m.DurationMillis = (int64(size)*1000 + int64(rate) - 1) / int64(rate)
	return m, nil
}

// Walk every MPEG Layer III frame, including VBR streams. No duration is
// inferred from file size or a single bitrate. Reject junk/truncated frames.
func parseMP3(b []byte) (proto.AudioMetadata, error) {
	m := proto.AudioMetadata{Container: "mp3", Codec: "mp3"}
	bad := func() (proto.AudioMetadata, error) {
		return m, fmt.Errorf("invalid audio: supported inputs are PCM WAV and MPEG Layer III MP3")
	}
	p := 0
	if len(b) >= 10 && string(b[:3]) == "ID3" {
		if b[3] < 2 || b[3] > 4 {
			return bad()
		}
		n := 0
		for _, v := range b[6:10] {
			if v&128 != 0 {
				return bad()
			}
			n = n<<7 | int(v)
		}
		p = 10 + n
		if b[3] == 4 && b[5]&16 != 0 {
			p += 10
		}
		if p > len(b) {
			return bad()
		}
	}
	var samples int64
	frames := 0
	for p < len(b) {
		if len(b)-p == 128 && string(b[p:p+3]) == "TAG" {
			p = len(b)
			break
		}
		if len(b)-p < 4 {
			return bad()
		}
		h := binary.BigEndian.Uint32(b[p : p+4])
		ver := (h >> 19) & 3
		layer := (h >> 17) & 3
		bi := (h >> 12) & 15
		si := (h >> 10) & 3
		if h>>21 != 0x7ff || ver == 1 || layer != 1 || bi == 0 || bi == 15 || si == 3 {
			return bad()
		}
		sr := []int{44100, 48000, 32000}[si]
		br := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}[bi]
		coef, ns := 144, 1152
		if ver != 3 {
			sr /= 2
			br = []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}[bi]
			coef = 72
			ns = 576
		}
		if ver == 0 {
			sr /= 2
		}
		channels := 2
		if (h>>6)&3 == 3 {
			channels = 1
		}
		if frames > 0 && (sr != m.SampleRate || channels != m.Channels) {
			return bad()
		}
		m.SampleRate = sr
		m.Channels = channels
		length := coef*br*1000/sr + int((h>>9)&1)
		if length < 4 || length > len(b)-p {
			return bad()
		}
		p += length
		samples += int64(ns)
		frames++
	}
	if frames < 2 {
		return bad()
	}
	m.DurationMillis = (samples*1000 + int64(m.SampleRate) - 1) / int64(m.SampleRate)
	return m, nil
}
