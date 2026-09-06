# Silent Input / visual speech recognition

Status: experimental iOS → user-machine vertical slice. Explicit Send is
mandatory. Android edge, mobile inference, hybrid AV, and cloud are interfaces
only and are not advertised as available.

## Runtime boundary

```text
iOS front camera (audio=false)
  -> temporary app-cache MP4 (8 s hard cap)
  -> iOS Vision outer-lip landmarks
  -> stabilized 96x96 gray8 mouth crops at 25 fps
  -> delete temporary MP4
  -> existing authenticated Yaver peer/relay transport, batches of 8
  -> in-memory agent session (250-frame cap, 2-minute TTL, four sessions max)
  -> YAVER_VSR_COMMAND local subprocess
  -> conservative context-term correction
  -> task composer draft
  -> user reviews and explicitly taps Send
```

The agent rejects JPEG/WebP, other dimensions, out-of-order batches, duplicate
timestamps, oversized batches, and more than ten seconds of frames. A stopped
session is removed from memory before inference begins. Neither frames nor
transcripts go through Convex or analytics. Latency counters may be logged on
the phone without pixels or transcript text.

The temporary iOS recording is a practical MVP compromise: the full frame is
written only to the app's temporary cache, never transported, and deleted in a
`finally` path after cropping. A future native streaming source should feed the
same `MouthFrame` interface directly and remove that temporary file entirely.

## Interfaces and future backends

`mobile/src/lib/silentInput/types.ts` owns `VisualSpeechSource`,
`VisualSpeechRecognizer`, `MouthFrame`, and backend-neutral result types.
`user-machine` is the only enabled recognizer. `mobile` and `cloud` remain
explicit enum values so Core ML, ONNX Runtime Mobile, and an opt-in fallback can
be added without changing the composer or frame contract. Cloud stays disabled
and no cloud endpoint exists.

Android should use CameraX plus a maintained landmark implementation, producing
the same gray8 frame contract. It must not fall back to uploading a full video
when landmarks are unavailable.

## Model and licensing audit

Auto-AVSR is a useful experimental baseline and Chaplin demonstrates a local,
push-to-record interaction, but neither is coupled into Yaver. The adapter
expects a user-installed checkout and checkpoint. It never downloads weights.

The Auto-AVSR repository describes its code as Apache-2.0 but explicitly warns
that pretrained models can have separate terms inherited from training data.
Common LRS3-derived weights are non-commercial or unclear. Do not bundle or
commercially redistribute any checkpoint until counsel has verified the exact
code, checkpoint, and training-data chain. Chaplin's MIT license covers its
code, not upstream weights or datasets.

## Evaluation gate

Before widening the flag, record a consented, local-only benchmark for 20–50
developer commands across speakers, lighting, pose, glasses, facial hair, and
distance. Report command top-1/top-3 accuracy in addition to WER. Keep clips out
of analytics and source control. The current 8-second interaction and explicit
Send remain required until false-action rates are measured.
