// Package media holds budgets shared by CLI inputs, tool reads and history.
package media

import (
	"context"
	"encoding/base64"
	"fmt"

	audioutil "github.com/panjie/mods/internal/audio"
	imageutil "github.com/panjie/mods/internal/image"
	"github.com/panjie/mods/internal/proto"
	videoutil "github.com/panjie/mods/internal/video"
)

type Usage struct {
	Bytes, Images                         int
	AudioMillis, VideoMillis, VideoFrames int64
}

func (u *Usage) Add(a proto.MediaAttachment) {
	u.Bytes += len(a.Data)
	switch a.Kind {
	case "image":
		u.Images++
	case "audio":
		u.AudioMillis += a.Audio.DurationMillis
	case "video":
		u.VideoMillis += a.Video.DurationMillis
		u.VideoFrames += videoutil.EstimatedFrames(a.Video)
		if a.Video.HasAudio {
			u.AudioMillis += a.Video.DurationMillis
		}
	}
}
func (u Usage) Validate() error {
	if u.AudioMillis > audioutil.MaxTotalDurationMillis {
		return fmt.Errorf("audio context exceeds 15 minutes (including video audio); start a smaller batch")
	}
	if u.VideoMillis > videoutil.MaxTotalDurationMillis || u.VideoFrames > videoutil.MaxTotalSampledFrames {
		return fmt.Errorf("video context exceeds 5 minute or 1200 estimated frame budget")
	}
	if u.Images > imageutil.MaxImageCount || u.Bytes > imageutil.MaxTotalImageBytes {
		return fmt.Errorf("media context budget exhausted (%d images, %d bytes; maximum 64 images and 20 MiB); start a smaller batch", u.Images, u.Bytes)
	}
	return nil
}
func Validate(messages []proto.Message) error {
	var usage Usage
	for _, m := range messages {
		for _, a := range m.Attachments() {
			switch a.Kind {
			case "image":
			case "audio":
				parsed, err := audioutil.Parse(a.Data)
				if err != nil {
					return err
				}
				if parsed.Audio != a.Audio || parsed.MimeType != a.MimeType {
					return fmt.Errorf("audio metadata does not match saved bytes")
				}
			case "video":
				parsed, err := videoutil.Parse(context.Background(), a.Data)
				if err != nil {
					return err
				}
				if parsed.Video != a.Video || parsed.MimeType != a.MimeType {
					return fmt.Errorf("video metadata does not match saved bytes")
				}
			default:
				return fmt.Errorf("unsupported media kind %q", a.Kind)
			}
			if a.Kind == "audio" || a.Kind == "video" {
				if base64.StdEncoding.EncodedLen(len(a.Data))+len("data:"+a.MimeType+";base64,") >= 10000000 {
					return fmt.Errorf("%s exceeds provider Base64 limit", a.Kind)
				}
			}
			usage.Add(a)
		}
	}
	return usage.Validate()
}
