package proto

// ProjectToolImages retains the legacy projection representation for image-only
// adapters while using the shared ordered media grouping algorithm.
func ProjectToolImages(input []Message) []Message {
	out := ProjectToolMedia(input)
	for i := range out {
		out[i].Images = out[i].ImageAttachments()
		var rest []MediaAttachment
		for _, a := range out[i].Media {
			if a.Kind != "image" {
				rest = append(rest, a)
			}
		}
		out[i].Media = rest
	}
	return out
}
