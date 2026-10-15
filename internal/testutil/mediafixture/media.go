// Package mediafixture supplies synthetic, non-private encoded test inputs.
package mediafixture

import "encoding/binary"

// WAV makes PCM silence with an odd-length JUNK chunk to exercise RIFF padding.
func WAV(seconds int) []byte {
	b := make([]byte, 54+seconds*16000*2)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEJUNK")
	binary.LittleEndian.PutUint32(b[16:], 1)
	copy(b[22:], "fmt ")
	binary.LittleEndian.PutUint32(b[26:], 16)
	binary.LittleEndian.PutUint16(b[30:], 1)
	binary.LittleEndian.PutUint16(b[32:], 1)
	binary.LittleEndian.PutUint32(b[34:], 16000)
	binary.LittleEndian.PutUint32(b[38:], 32000)
	binary.LittleEndian.PutUint16(b[42:], 2)
	binary.LittleEndian.PutUint16(b[44:], 16)
	copy(b[46:], "data")
	binary.LittleEndian.PutUint32(b[50:], uint32(len(b)-54))
	return b
}

// MP3Frames constructs header-valid VBR frames for parser boundary tests only;
// these synthetic frames are not used for recognition/integration tests.
func MP3Frames() []byte {
	b := []byte{'I', 'D', '3', 4, 0, 0, 0, 0, 0, 0}
	for _, bitrate := range []int{128, 192, 128} {
		frame := make([]byte, 144*bitrate*1000/44100)
		h := uint32(0xfffb9000)
		if bitrate == 192 {
			h = 0xfffbb000
		}
		binary.BigEndian.PutUint32(frame, h)
		b = append(b, frame...)
	}
	return b
}
