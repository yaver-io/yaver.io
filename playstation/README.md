# Yaver PlayStation adapter boundary

This directory is the public, SDK-free part of the proposed PlayStation client.
The executable contract treats tvOS as the interaction reference while keeping
Sony SDK code out of the public repository.

The PlayStation client is a viewer/controller. A render machine may provide a
browser viewport or a native React Native/Hermes runtime, but the console does
not execute an arbitrary Hermes bundle and does not embed a third-party app.
WebRTC H.264 is preferred; an authenticated frame stream is the fallback, and
bounded controller input stays on the authenticated single-writer control
plane.

Voice is push-to-talk. Platform STT/TTS is preferred; any cloud speech path
requires explicit consent and a visible transcript before sending. Text and
visual replies remain available when speech is unavailable.

Run `node playstation/tests/readiness-contract.test.mjs`. A non-zero result from
`node playstation/readiness.mjs` is expected until Sony approval, SDK/title IDs,
official-hardware validation, privacy review, and certification are complete.
