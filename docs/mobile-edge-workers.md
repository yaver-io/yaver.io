# Mobile Edge Workers

This document captures the useful version of "phones as infra" for Yaver.

## What is worth building

- A phone can be a `Yaver client`.
- Another phone can be an `edge-mobile` worker.
- The worker can run small local models, collect sensor/media input, and escalate heavier reasoning to central infra.

## What a phone farm is good for

- Speech transcription with tiny/small models.
- OCR and image labeling.
- Embeddings and reranking.
- Background media preprocessing.
- Privacy-sensitive local preprocessing before sending compact results upstream.

## What a phone farm is bad for

- Hosting large LLMs.
- Long-context reasoning.
- Multi-device tensor sharding for interactive latency-sensitive inference.

The limiting factors are RAM, sustained thermal performance, memory bandwidth, and coordination overhead.

## New backend support

`devices` now supports:

- `android` and `ios` platforms
- `deviceClass` to distinguish desktop vs edge-mobile vs server
- `edgeProfile` so Yaver can route work based on local inference support, thermals, battery, and preferred task classes

There is also a placement recommendation endpoint:

`POST /devices/placement/recommend`

Body:

```json
{
  "taskKind": "ocr"
}
```

Supported `taskKind` values:

- `speech-transcription`
- `ocr`
- `vision-labeling`
- `embedding`
- `rerank`
- `small-local-agent`
- `batch-preprocessing`
- `big-llm-chat`
- `long-context-reasoning`

The response tells the caller whether Yaver should prefer `edge`, `infra`, or `hybrid`, and whether farming is worthwhile for that class of work.
