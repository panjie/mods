// Package video validates bounded local MP4 containers. It does not decode
// frames: a provider decode failure must still be reported as unprocessed.
package video

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	mp4 "github.com/abema/go-mp4"
	"github.com/panjie/mods/internal/proto"
)

const (
	MaxBytes                     = 6 * 1024 * 1024
	MaxDurationMillis      int64 = 120000
	MaxTotalDurationMillis int64 = 300000
	SamplingFPS                  = 2
	MaxSampledFrames       int64 = 600
	MaxTotalSampledFrames  int64 = 1200
)

// IsContainer recognizes ISO BMFF before routing to an audio parser. Actual
// tracks, brands and codecs are checked by Parse, not inferred from a suffix.
func IsContainer(head []byte) bool { return len(head) >= 8 && string(head[4:8]) == "ftyp" }

func Read(ctx context.Context, path string) (proto.MediaAttachment, error) {
	if err := ctx.Err(); err != nil {
		return proto.MediaAttachment{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return proto.MediaAttachment{}, err
	}
	if !info.Mode().IsRegular() {
		return proto.MediaAttachment{}, fmt.Errorf("video input must be a regular file")
	}
	if info.Size() > MaxBytes {
		return proto.MediaAttachment{}, fmt.Errorf("video exceeds 6 MiB inline limit")
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
		return proto.MediaAttachment{}, fmt.Errorf("video input must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return proto.MediaAttachment{}, err
	}
	a, err := Parse(ctx, data)
	a.Source = path
	return a, err
}

func EstimatedFrames(m proto.VideoMetadata) int64 {
	return (m.DurationMillis*int64(m.SamplingFPS) + 999) / 1000
}

type track struct {
	tkhd          *mp4.Tkhd
	mdhd          *mp4.Mdhd
	handler       string
	codec         string
	width, height int
	avcc, aac     bool
	offsets       []uint64
	sizes         *mp4.Stsz
	chunks        *mp4.Stsc
	times         *mp4.Stts
	edits         *mp4.Elst
	seen          map[string]bool
}
type span struct{ start, end uint64 }
type inspector struct {
	ctx        context.Context
	data       []byte
	boxes      int
	tracks     []*track
	mdats      []span
	movie      *mp4.Mvhd
	ftyp, moov bool
}

func Parse(ctx context.Context, data []byte) (proto.MediaAttachment, error) {
	a := proto.MediaAttachment{Kind: "video", Data: data, MimeType: "video/mp4"}
	if len(data) == 0 || len(data) > MaxBytes {
		return a, fmt.Errorf("video must contain 1 to 6 MiB of data")
	}
	if !IsContainer(data) {
		return a, fmt.Errorf("supported video input is an MP4 file starting with ftyp")
	}
	p := inspector{ctx: ctx, data: data}
	if err := p.walk(0, uint64(len(data)), "", nil, 0); err != nil {
		return a, fmt.Errorf("invalid or unsupported MP4: %w", err)
	}
	if !p.ftyp || !p.moov || p.movie == nil || len(p.mdats) == 0 {
		return a, fmt.Errorf("MP4 requires ftyp, moov, movie timing and nonempty mdat")
	}
	duration, err := durationMS(p.movie.GetDuration(), p.movie.Timescale)
	if err != nil {
		return a, err
	}
	m := proto.VideoMetadata{Container: "mp4", DurationMillis: duration, SamplingFPS: SamplingFPS}
	videoTracks, audioTracks := 0, 0
	ids := map[uint32]bool{}
	for _, t := range p.tracks {
		if t.tkhd == nil || t.mdhd == nil || t.tkhd.TrackID == 0 || t.tkhd.GetFlags()&1 == 0 || ids[t.tkhd.TrackID] {
			return a, fmt.Errorf("missing or duplicate MP4 track identity/timing")
		}
		ids[t.tkhd.TrackID] = true
		td, err := durationMS(t.tkhd.GetDuration(), p.movie.Timescale)
		if err != nil {
			return a, err
		}
		m.DurationMillis = max(m.DurationMillis, td)
		td, err = durationMS(t.mdhd.GetDuration(), t.mdhd.Timescale)
		if err != nil {
			return a, err
		}
		m.DurationMillis = max(m.DurationMillis, td)
		if t.edits != nil {
			var total uint64
			for i := range t.edits.Entries {
				segment := t.edits.GetSegmentDuration(i)
				mediaTime := t.edits.GetMediaTime(i)
				if segment == 0 || segment > uint64(p.movie.Timescale)*120 || mediaTime < 0 || uint64(mediaTime) >= t.mdhd.GetDuration() {
					return a, fmt.Errorf("invalid edit timeline")
				}
				total += segment
			}
			if total != t.tkhd.GetDuration() {
				return a, fmt.Errorf("edit timeline differs from track duration")
			}
		}
		if err := p.validateSamples(t); err != nil {
			return a, err
		}
		switch t.handler {
		case "vide":
			videoTracks++
			if t.codec != "avc1" || !t.avcc {
				return a, fmt.Errorf("video must be H.264 (avc1) with decoder configuration")
			}
			rotation, err := rotation(t.tkhd.Matrix)
			if err != nil {
				return a, err
			}
			w, h := int(t.tkhd.Width>>16), int(t.tkhd.Height>>16)
			if w != t.width || h != t.height || w <= 0 || h <= 0 || max(w, h) > 1920 || min(w, h) > 1080 {
				return a, fmt.Errorf("video must have unscaled dimensions at most 1080p")
			}
			if rotation == 90 || rotation == 270 {
				w, h = h, w
			}
			m.Width = w
			m.Height = h
			m.Rotation = rotation
			m.VideoCodec = "h264"
		case "soun":
			audioTracks++
			if t.codec != "mp4a" || !t.aac {
				return a, fmt.Errorf("MP4 audio must be AAC-LC")
			}
			m.HasAudio = true
			m.AudioCodec = "aac"
		default:
			return a, fmt.Errorf("unsupported MP4 track type %q", t.handler)
		}
	}
	if videoTracks != 1 || audioTracks > 1 {
		return a, fmt.Errorf("MP4 must contain exactly one video track and at most one audio track (audio-only MP4/M4A is unsupported)")
	}
	if m.DurationMillis > MaxDurationMillis || EstimatedFrames(m) > MaxSampledFrames {
		return a, fmt.Errorf("video exceeds 2 minute or sampling budget")
	}
	a.Video = m
	return a, nil
}

func durationMS(duration uint64, scale uint32) (int64, error) {
	if scale == 0 || duration == 0 || duration > uint64(scale)*120 {
		return 0, fmt.Errorf("video timing must be known and at most 2 minutes")
	}
	return int64((duration*1000 + uint64(scale) - 1) / uint64(scale)), nil
}
func rotation(m [9]int32) (int, error) {
	if m[2] != 0 || m[5] != 0 || m[8] != 1<<30 {
		return 0, fmt.Errorf("unsupported video perspective matrix")
	}
	switch [4]int32{m[0], m[1], m[3], m[4]} {
	case [4]int32{65536, 0, 0, 65536}:
		return 0, nil
	case [4]int32{0, 65536, -65536, 0}:
		return 90, nil
	case [4]int32{-65536, 0, 0, -65536}:
		return 180, nil
	case [4]int32{0, -65536, 65536, 0}:
		return 270, nil
	}
	return 0, fmt.Errorf("unsupported video scaling/reflection matrix")
}

// Walk only structural/codec boxes. The library handles MP4 box headers and
// payload layouts; explicit bounds and depth/count limits precede unmarshalling.
func (p *inspector) walk(start, end uint64, parent string, t *track, depth int) error {
	if depth > 12 {
		return fmt.Errorf("MP4 nesting exceeds limit")
	}
	r := bytes.NewReader(p.data)
	for off := start; off < end; {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		p.boxes++
		if p.boxes > 10000 {
			return fmt.Errorf("too many MP4 boxes")
		}
		if end-off < 8 {
			return fmt.Errorf("truncated MP4 box")
		}
		_, _ = r.Seek(int64(off), io.SeekStart)
		bi, err := mp4.ReadBoxInfo(r)
		if err != nil {
			return err
		}
		if bi.Size < bi.HeaderSize || bi.Size > end-off || bi.HeaderSize > bi.Size {
			return fmt.Errorf("MP4 box exceeds parent bounds")
		}
		name := bi.Type.String()
		body := p.data[off+bi.HeaderSize : off+bi.Size]
		next := off + bi.Size
		switch name {
		case "moof", "mvex", "sinf", "encv", "enca", "pssh":
			return fmt.Errorf("fragmented or encrypted MP4 is unsupported")
		}
		// Bound fixed/sample metadata before asking the library to allocate arrays.
		decode := func() (mp4.IBox, uint64, error) {
			if len(body) > 1024*1024 {
				return nil, 0, fmt.Errorf("oversized MP4 metadata")
			}
			return mp4.UnmarshalAny(bytes.NewReader(body), bi.Type, uint64(len(body)), mp4.Context{})
		}
		if parent == "stsd" && name != "avc1" && name != "mp4a" {
			return fmt.Errorf("unsupported codec %q", name)
		}
		if parent == "dref" && name != "url " {
			return fmt.Errorf("unsupported data reference %q", name)
		}
		switch name {
		case "ftyp":
			if parent != "" || p.ftyp || off != 0 || len(body) > 4096 {
				return fmt.Errorf("invalid ftyp")
			}
			b, _, err := decode()
			if err != nil {
				return err
			}
			f := b.(*mp4.Ftyp)
			allowed := false
			for _, brand := range []string{"isom", "iso2", "mp41", "mp42", "avc1"} {
				if string(f.MajorBrand[:]) == brand || f.HasCompatibleBrand([4]byte{brand[0], brand[1], brand[2], brand[3]}) {
					allowed = true
				}
			}
			if !allowed || string(f.MajorBrand[:]) == "qt  " || f.HasCompatibleBrand([4]byte{'q', 't', ' ', ' '}) {
				return fmt.Errorf("unsupported MP4 brand")
			}
			p.ftyp = true
		case "mdat":
			if parent != "" || len(body) == 0 {
				return fmt.Errorf("invalid mdat")
			}
			p.mdats = append(p.mdats, span{off + bi.HeaderSize, next})
		case "moov":
			if parent != "" || p.moov {
				return fmt.Errorf("duplicate/nested moov")
			}
			p.moov = true
			if err := p.walk(off+bi.HeaderSize, next, name, nil, depth+1); err != nil {
				return err
			}
		case "trak":
			if parent != "moov" || len(p.tracks) >= 2 {
				return fmt.Errorf("too many/nested tracks")
			}
			tr := &track{seen: map[string]bool{}}
			p.tracks = append(p.tracks, tr)
			if err := p.walk(off+bi.HeaderSize, next, name, tr, depth+1); err != nil {
				return err
			}
		case "mdia", "minf", "stbl", "dinf", "edts":
			expected := map[string]string{"mdia": "trak", "minf": "mdia", "stbl": "minf", "dinf": "minf", "edts": "trak"}
			if parent != expected[name] || t == nil || t.seen[name] {
				return fmt.Errorf("invalid/duplicate %s", name)
			}
			t.seen[name] = true
			if err := p.walk(off+bi.HeaderSize, next, name, t, depth+1); err != nil {
				return err
			}
		case "mvhd":
			if parent != "moov" || p.movie != nil {
				return fmt.Errorf("invalid movie timing")
			}
			b, _, err := decode()
			if err != nil {
				return err
			}
			p.movie = b.(*mp4.Mvhd)
		case "tkhd", "mdhd", "hdlr", "stsd", "avc1", "mp4a", "avcC", "esds", "stco", "co64", "stsz", "stsc", "stts", "dref", "url ", "elst":
			if t == nil || t.seen[name] {
				return fmt.Errorf("duplicate/misplaced %s", name)
			}
			t.seen[name] = true
			expected := map[string]string{"tkhd": "trak", "mdhd": "mdia", "hdlr": "mdia", "stsd": "stbl", "avc1": "stsd", "mp4a": "stsd", "avcC": "avc1", "esds": "mp4a", "stco": "stbl", "co64": "stbl", "stsz": "stbl", "stsc": "stbl", "stts": "stbl", "dref": "dinf", "url ": "dref", "elst": "edts"}
			if parent != expected[name] {
				return fmt.Errorf("misplaced %s", name)
			}
			b, n, err := decode()
			if err != nil {
				return err
			}
			switch v := b.(type) {
			case *mp4.Tkhd:
				t.tkhd = v
			case *mp4.Mdhd:
				t.mdhd = v
			case *mp4.Hdlr:
				t.handler = string(v.HandlerType[:])
			case *mp4.Stsd:
				if v.EntryCount != 1 {
					return fmt.Errorf("multiple sample descriptions unsupported")
				}
			case *mp4.VisualSampleEntry:
				if v.DataReferenceIndex != 1 {
					return fmt.Errorf("external video reference")
				}
				t.codec = name
				t.width = int(v.Width)
				t.height = int(v.Height)
			case *mp4.AudioSampleEntry:
				if v.DataReferenceIndex != 1 || v.EntryVersion != 0 {
					return fmt.Errorf("unsupported audio reference/version")
				}
				t.codec = name
			case *mp4.AVCDecoderConfiguration:
				if v.ConfigurationVersion != 1 || len(v.SequenceParameterSets) == 0 || len(v.PictureParameterSets) == 0 {
					return fmt.Errorf("missing AVC decoder configuration")
				}
				t.avcc = true
			case *mp4.Esds:
				aacConfig, lc := false, false
				for _, d := range v.Descriptors {
					if d.DecoderConfigDescriptor != nil {
						if d.DecoderConfigDescriptor.ObjectTypeIndication != 0x40 || d.DecoderConfigDescriptor.StreamType != 5 {
							return fmt.Errorf("unsupported audio codec")
						}
						aacConfig = true
					}
					if d.Tag == mp4.DecSpecificInfoTag && len(d.Data) >= 2 {
						lc = d.Data[0]>>3 == 2
					}
				}
				t.aac = aacConfig && lc
			case *mp4.Stco:
				for _, o := range v.ChunkOffset {
					t.offsets = append(t.offsets, uint64(o))
				}
			case *mp4.Co64:
				t.offsets = append(t.offsets, v.ChunkOffset...)
			case *mp4.Stsz:
				t.sizes = v
			case *mp4.Stsc:
				t.chunks = v
			case *mp4.Stts:
				t.times = v
			case *mp4.Dref:
				if v.EntryCount != 1 {
					return fmt.Errorf("unsupported data references")
				}
			case *mp4.Url:
				if v.GetFlags() != 1 {
					return fmt.Errorf("external media reference unsupported")
				}
			case *mp4.Elst:
				t.edits = v
				if len(v.Entries) > 16 {
					return fmt.Errorf("too many edit entries")
				}
				for i, e := range v.Entries {
					if e.MediaRateInteger != 1 || e.MediaRateFraction != 0 || v.GetMediaTime(i) < 0 {
						return fmt.Errorf("unsupported edit timeline")
					}
				}
			}
			switch name {
			case "stsd", "avc1", "mp4a", "dref":
				if err := p.walk(off+bi.HeaderSize+n, next, name, t, depth+1); err != nil {
					return err
				}
			default:
				if n != uint64(len(body)) {
					return fmt.Errorf("unparsed %s payload", name)
				}
			}
		}
		off = next
	}
	return nil
}

func (p *inspector) validateSamples(t *track) error {
	if t.sizes == nil || t.chunks == nil || t.times == nil || len(t.offsets) == 0 || t.seen["stco"] && t.seen["co64"] || !t.seen["url "] {
		return fmt.Errorf("missing or ambiguous sample tables/references")
	}
	count := t.sizes.SampleCount
	if count == 0 || count > 100000 {
		return fmt.Errorf("invalid sample count")
	}
	var total, ticks uint64
	for _, e := range t.times.Entries {
		if e.SampleCount == 0 || e.SampleDelta == 0 {
			return fmt.Errorf("invalid sample timing")
		}
		total += uint64(e.SampleCount)
		ticks += uint64(e.SampleCount) * uint64(e.SampleDelta)
	}
	if total != uint64(count) || ticks != t.mdhd.GetDuration() {
		return fmt.Errorf("inconsistent sample timing")
	}
	entries := t.chunks.Entries
	if len(entries) == 0 || entries[0].FirstChunk != 1 {
		return fmt.Errorf("invalid chunk table")
	}
	for i, e := range entries {
		if e.FirstChunk > uint32(len(t.offsets)) || e.SamplesPerChunk == 0 || e.SampleDescriptionIndex != 1 || i > 0 && e.FirstChunk <= entries[i-1].FirstChunk {
			return fmt.Errorf("invalid chunk mapping")
		}
	}
	sample, entry := uint32(0), 0
	for i, offset := range t.offsets {
		if err := p.ctx.Err(); err != nil {
			return err
		}
		if entry+1 < len(entries) && entries[entry+1].FirstChunk == uint32(i+1) {
			entry++
		}
		n := entries[entry].SamplesPerChunk
		if n > count-sample {
			return fmt.Errorf("chunk sample count exceeds table")
		}
		var size uint64
		for j := uint32(0); j < n; j++ {
			s := t.sizes.SampleSize
			if s == 0 {
				s = t.sizes.EntrySize[sample+j]
			}
			if s == 0 {
				return fmt.Errorf("empty media sample")
			}
			size += uint64(s)
		}
		fits := false
		for _, r := range p.mdats {
			if offset >= r.start && offset < r.end && size <= r.end-offset {
				fits = true
			}
		}
		if !fits {
			return fmt.Errorf("media chunk points outside mdat")
		}
		sample += n
	}
	if sample != count {
		return fmt.Errorf("incomplete media sample mapping")
	}
	return nil
}
