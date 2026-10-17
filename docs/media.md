# Media Details

Mods reads images, audio, and video natively and sends the original files to the
selected model. No OCR installation, ASR service, ffmpeg, frame extraction, or
nested `mods` process is required.

Discovery and reading use the ordinary filesystem tools:

- `fs_find_images` and `fs_find_media` enumerate candidates with pagination and
  recursive scanning.
- `fs_read_images` and `fs_read_media` return ordered, labelled attachments
  directly to the current model.

Filesystem tools must be enabled for the reading tools. `auto` (the default)
recognizes media requests; `builtin-tools.filesystem: false` disables directory
discovery and reading tools. Discovery includes hidden files and does not follow
directory symlinks.

Adapters expose media inputs and reading tools according to their implemented
protocol, without a model-name capability list or per-model media settings.
Choose a model that understands the inputs you send; if it does not, the service
may reject the request. Mods preserves that error rather than silently removing
attachments. File validation and cumulative budgets still apply.

## Images

With a vision-capable model and tool calls, describe the search in natural
language. `fs_find_images` enumerates PNG, JPEG, GIF, and WebP candidates;
`fs_read_images` returns labelled image attachments to the current model. No
OCR installation or nested Mods process is required.

```sh
mods -a deepseek -m deepseek-flash "Find images containing 'invoice' in this directory and its subdirectories. Return matching paths and the text you read."
mods --image assets/mods-product.png "suggest alt text for this image"
mods -I "describe the image on my clipboard"
```

Discovery supports recursive pagination. The reader checks file content,
preserves original pixels, and accepts up to eight images per call. Animated
image interpretation depends on the provider. Unsupported formats require
conversion before reading.

Each conversation request is bounded to 64 images and 20 MiB of raw image data,
including saved history and earlier tool rounds; image request bodies are also
bounded to 40 MiB before provider-specific overrides. Splitting reads into
batches does not remove cumulative context costs.

Failed reads and unfinished scans are reported as incomplete, not as
non-matches. For large collections, use smaller scopes in fresh conversations.
Continuing a saved conversation preserves tool images and source labels;
discovery cursors expire with the request, so resume by enumerating again.
Visual recognition can be uncertain, especially for small or blurred text; ask
for supporting text snippets and uncertain cases.

## Audio

`--audio` is repeatable and accepts PCM WAV and MP3. Audio is sent directly to
Qwen as native model input:

```sh
mods -a qwen -m qwen3.8-omni-flash "Find recordings mentioning refunds in this directory and its subdirectories. Return paths and supporting quotes."

mods -a qwen -m qwen3.8-omni-flash --audio meeting.wav --image slide.png "Compare the amounts in the recording and slide."
```

Both Chat Completions (the default for Qwen) and an explicit
`endpoint: responses` use the existing `qwen` protocol profile. The built-in
Qwen provider selects it automatically, independently of the model name. For a
custom OpenAI-compatible gateway using this dialect, set
`provider-profile: qwen` at provider or model level. This is also required for
older gateway configurations that relied on the model name to select Qwen media
encoding. An explicit different profile is never overridden by the model name.
Output is text; microphone recording and speech generation are not included.
Adapters without an implemented audio dialect reject audio before sending it.
The service decides whether a model accepts input in an implemented dialect.

`fs_find_media` discovers image/audio/video candidates with pagination and
recursive scanning; `fs_read_media` returns ordered, labelled attachments.
Existing image tools use the same scanning, reading, authorization, and budget
engine. Automatic filesystem activation recognizes audio requests. Discovery
includes common unsupported audio extensions (M4A/AAC/FLAC/OGG/Opus) so they can
be reported as unread instead of silently omitted. File contents determine the
actual supported format.

Each recording is limited to 6 MiB and 10 minutes; a tool read accepts at most
four recordings (prefer one or two), within eight total media files. All input
and saved history share 20 MiB of raw media, 15 minutes of audio, and a 40 MiB
serialized-request budget. Image count and pixel limits still apply. The file
size limit often applies before the duration limit. Long-audio slicing and
automatic context compaction are not implemented: use smaller scopes in fresh
sessions.

Original audio bytes and channels are preserved, and saved sessions retain
attachments even if the source file changes. Logs and transcripts show source
and audio metadata, never Base64. In JSON mode, incomplete-scan diagnostics go
to stderr separately from the model answer. Recognition and timestamps may be
uncertain; silence or failed reads are not evidence of a non-match.

## Video

`--video` is repeatable and can be combined with `--audio` and `--image`. Video
uses the same directory tools, source labels, authorization, attachment storage,
and budgets as images and audio:

```sh
mods -a qwen -m qwen3.8-omni-flash "Find videos mentioning refunds in this directory and its subdirectories. Check both speech and visible text; return paths and evidence."

mods -a qwen -m qwen3.8-omni-flash --video meeting.mp4 --image slide.png "Compare the spoken and visible amounts with the slide."
```

Chat Completions and Responses send the original local MP4 as native video,
including its audio track. No runtime ffmpeg, frame extraction, ASR service, or
recursive model invocation is required. As with audio, the `qwen` profile
selects this encoding for any model name; the service decides model support.
Adapters without an implemented video dialect reject video before sending it.

The first version accepts non-fragmented MP4 with one H.264 (`avc1`) video track
and optionally one AAC-LC audio track. Each file must be at most **6 MiB,
2 minutes, and 1080p**. Pure-Go container checks validate timing, dimensions,
rotation, sample tables, and local data references; they do not fully decode
compressed media. Encrypted files, complex edit timelines, other codecs, and
containers are rejected. Discovery includes MOV/MKV/WebM/AVI/M4V candidates so
unsupported files can be reported as unread. No automatic transcoding occurs.

A tool read attaches at most one video. The full conversation may contain up to
5 minutes of video, with a requested sampling rate of 2 fps and a conservative
1200-frame budget. The shared 20 MiB raw and 40 MiB request limits still apply;
video with sound also consumes the 15-minute audio budget, without counting its
bytes twice. Saved sessions keep the original video, so continuation does not
need the source file. Transcripts display metadata instead of encoded bytes.

Recognition remains model-dependent, especially across multiple videos or mixed
inputs. A successful read proves delivery, not correct recognition. Check quoted
evidence for important searches. Sampling can miss brief events; timestamps are
approximate and results are not an exhaustive frame-by-frame inspection.
Long-video splitting and automatic context compaction are not implemented.

## Shared Limits

| Resource | Limit |
|----------|-------|
| Media files per tool read | 8 |
| Images per conversation request | 64 |
| Raw media per conversation request | 20 MiB |
| Serialized request body | 40 MiB |
| Audio per recording | 6 MiB / 10 minutes |
| Recordings per tool read | 4 (prefer 1–2) |
| Audio per conversation | 15 minutes |
| Video per file | 6 MiB / 2 minutes / 1080p |
| Videos per tool read | 1 |
| Video per conversation | 5 minutes, sampled at 2 fps (1200-frame budget) |

These are cumulative across input, saved history, and earlier tool rounds.
Splitting work into batches does not remove context costs. Failed or unread
files are reported as incomplete, not as non-matches.
