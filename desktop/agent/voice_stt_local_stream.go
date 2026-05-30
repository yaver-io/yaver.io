package main

// voice_stt_local_stream.go — realtime-ish streaming STT on top of the
// batch whisper.cpp CLI, so `yaver voice listen` (and `voice test`) feel
// live with the FREE/offline engine — no Deepgram key required.
//
// whisper.cpp's CLI is one-shot (feed a WAV, get a transcript), so true
// frame-by-frame streaming isn't available. We fake it convincingly:
//
//   - SendAudio appends mic PCM to a per-utterance rolling buffer and runs
//     a cheap RMS energy VAD on each chunk.
//   - A background ticker re-transcribes the in-progress utterance every
//     ~partialEvery and emits "partial" events (the dim, updating line).
//   - When trailing silence exceeds the hangover window, the utterance is
//     finalized: one more transcription → "final" + "eot", then the buffer
//     resets for the next turn. Multi-turn, hands-free, until Close.
//
// Re-transcribing the whole utterance each tick is wasteful in theory but
// cheap in practice: the tiny/base models decode a few seconds of audio in
// well under the tick interval on Apple silicon. Whisper runs are
// serialized through a single worker so we never spawn overlapping procs;
// partials are dropped when the worker is busy, finals always run.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	lwSampleRate = 16000
	lwBytesPerS  = lwSampleRate * 2 // mono signed-16-bit-LE

	lwSilenceRMS   = 450.0 // RMS below this = silence (s16 scale)
	lwHangoverMs   = 700   // trailing silence that ends a turn
	lwMinUttMs     = 350   // ignore sub-blips / stray clicks
	lwPartialEvery = 850 * time.Millisecond
	lwMaxUttSec    = 30 // force-finalize a runaway utterance
)

func lwMinUttBytes() int { return lwMinUttMs * lwBytesPerS / 1000 }

// localTranscribe is the transcription seam — the worker calls through it
// so tests can inject a fake (whisper.cpp need not be installed in CI).
var localTranscribe = transcribeLocalWhisper

// localStreamJob is one transcription request handed to the worker.
type localStreamJob struct {
	pcm   []byte
	final bool
	uttID int
}

// StreamingLocalWhisperSession implements sttSession with live partials
// and automatic end-of-turn detection, all offline via whisper.cpp.
type StreamingLocalWhisperSession struct {
	bin       string
	modelPath string
	autoEOT   bool // VAD auto-ends turns (hands-free); false = finalize only on Finalize (push-to-talk)
	events    chan DeepgramEvent
	jobs      chan localStreamJob
	quit      chan struct{}
	wg        sync.WaitGroup

	mu        sync.Mutex
	utt       []byte
	curUttID  int
	hadSpeech bool
	silenceMs int
	closed    bool
}

// OpenStreamingLocalWhisperSession starts the worker + partial ticker and
// returns a live session. Errors (with install hints) if the whisper.cpp
// CLI or a ggml model is missing — same contract as the batch session.
func OpenStreamingLocalWhisperSession(ctx context.Context) (*StreamingLocalWhisperSession, <-chan DeepgramEvent, error) {
	return OpenStreamingLocalWhisperSessionOpts(ctx, true)
}

// OpenStreamingLocalWhisperSessionOpts opens a streaming local session.
// autoEOT=true: VAD auto-detects end-of-turn (terminal hands-free loop).
// autoEOT=false: live partials only; the caller drives turn boundaries via
// Finalize (push-to-talk over /voice/stream, so a mid-sentence pause never
// prematurely cuts the utterance).
func OpenStreamingLocalWhisperSessionOpts(ctx context.Context, autoEOT bool) (*StreamingLocalWhisperSession, <-chan DeepgramEvent, error) {
	bin := localWhisperBin()
	if bin == "" {
		return nil, nil, fmt.Errorf("no whisper.cpp CLI found (install: brew install whisper-cpp)")
	}
	model := localWhisperModel()
	if model == "" {
		return nil, nil, fmt.Errorf("no whisper ggml model found (set YAVER_WHISPER_MODEL or place one in ~/.yaver/models/)")
	}
	s, ev := startStreamingLocal(ctx, bin, model, autoEOT)
	return s, ev, nil
}

// startStreamingLocal builds a session over a given CLI + model and starts
// its goroutines. Split out from Open so tests can drive it with dummy
// paths and a stubbed localTranscribe (no whisper.cpp dependency).
func startStreamingLocal(ctx context.Context, bin, model string, autoEOT bool) (*StreamingLocalWhisperSession, <-chan DeepgramEvent) {
	s := &StreamingLocalWhisperSession{
		bin:       bin,
		modelPath: model,
		autoEOT:   autoEOT,
		events:    make(chan DeepgramEvent, 8),
		jobs:      make(chan localStreamJob, 4),
		quit:      make(chan struct{}),
	}
	s.wg.Add(1)
	go s.partialLoop(ctx)
	go s.worker() // closes s.events when s.jobs drains after close
	return s, s.events
}

// SendAudio appends a mic chunk, runs VAD, and triggers a final flush when
// a turn ends. Called from the single mic-pump goroutine (never concurrent
// with Finalize), so only partialLoop is the other writer to s.jobs.
func (s *StreamingLocalWhisperSession) SendAudio(pcm []byte) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("session closed")
	}
	rms := pcmRMS(pcm)
	chunkMs := len(pcm) * 1000 / lwBytesPerS
	s.utt = append(s.utt, pcm...)

	var final *localStreamJob
	switch {
	case rms >= lwSilenceRMS:
		if !s.hadSpeech {
			s.curUttID++ // a new utterance begins
		}
		s.hadSpeech = true
		s.silenceMs = 0
	case s.hadSpeech:
		s.silenceMs += chunkMs
		if s.autoEOT && s.silenceMs >= lwHangoverMs && len(s.utt) >= lwMinUttBytes() {
			final = s.cutUtteranceLocked()
		}
	}
	// Guard against an utterance that never goes silent (e.g. background
	// noise): force a final once it gets too long. Applies even in
	// push-to-talk mode so the buffer can't grow without bound.
	if final == nil && s.hadSpeech && len(s.utt) >= lwMaxUttSec*lwBytesPerS {
		final = s.cutUtteranceLocked()
	}
	s.mu.Unlock()

	if final != nil {
		s.submit(*final, true) // finals must run
	}
	return nil
}

// cutUtteranceLocked snapshots the current utterance as a final job and
// resets the buffer. Caller must hold s.mu.
func (s *StreamingLocalWhisperSession) cutUtteranceLocked() *localStreamJob {
	j := &localStreamJob{
		pcm:   append([]byte(nil), s.utt...),
		final: true,
		uttID: s.curUttID,
	}
	s.utt = s.utt[:0]
	s.hadSpeech = false
	s.silenceMs = 0
	return j
}

// partialLoop periodically emits a partial transcript of the in-progress
// utterance so the user sees text appear while still speaking.
func (s *StreamingLocalWhisperSession) partialLoop(ctx context.Context) {
	defer s.wg.Done()
	t := time.NewTicker(lwPartialEvery)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			var job *localStreamJob
			if s.hadSpeech && len(s.utt) >= lwMinUttBytes() {
				job = &localStreamJob{
					pcm:   append([]byte(nil), s.utt...),
					uttID: s.curUttID,
				}
			}
			s.mu.Unlock()
			if job != nil {
				s.submit(*job, false) // drop if worker busy
			}
		}
	}
}

// submit enqueues a job. Finals block (until accepted or shutdown);
// partials are best-effort and dropped when the worker is backed up.
func (s *StreamingLocalWhisperSession) submit(j localStreamJob, block bool) {
	if block {
		select {
		case s.jobs <- j:
		case <-s.quit:
		}
		return
	}
	select {
	case s.jobs <- j:
	case <-s.quit:
	default:
	}
}

// worker serializes whisper.cpp invocations and emits transcript events.
// Stale partials (from a turn already finalized) are skipped so old text
// can't overwrite a fresh line.
func (s *StreamingLocalWhisperSession) worker() {
	defer close(s.events)
	lastFinal := 0
	for job := range s.jobs {
		if !job.final && job.uttID <= lastFinal {
			continue
		}
		text, err := localTranscribe(s.bin, s.modelPath, job.pcm)
		if err != nil {
			if job.final {
				lastFinal = job.uttID
				s.events <- DeepgramEvent{Kind: "error", Error: err.Error()}
			}
			continue
		}
		text = strings.TrimSpace(text)
		if job.final {
			lastFinal = job.uttID
			if text != "" {
				s.events <- DeepgramEvent{Kind: "final", Text: text}
				s.events <- DeepgramEvent{Kind: "eot"}
			}
		} else if text != "" {
			s.events <- DeepgramEvent{Kind: "partial", Text: text}
		}
	}
}

// Finalize flushes any in-progress utterance as a last final, then shuts
// the session down. Called by the mic pump on Ctrl-C / mic EOF.
func (s *StreamingLocalWhisperSession) Finalize() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	var flush *localStreamJob
	if len(s.utt) >= lwMinUttBytes() {
		flush = &localStreamJob{pcm: append([]byte(nil), s.utt...), final: true, uttID: s.curUttID}
	}
	s.utt = nil
	s.mu.Unlock()

	close(s.quit) // stop partialLoop from any further sends
	s.wg.Wait()   // ensure no in-flight partial send races the close below

	if flush != nil {
		s.jobs <- *flush // safe: only this goroutine sends now
	}
	close(s.jobs) // worker drains remaining jobs, then closes s.events
	return nil
}

// Close shuts down without a final flush (abort path).
func (s *StreamingLocalWhisperSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.utt = nil
	s.mu.Unlock()

	close(s.quit)
	s.wg.Wait()
	close(s.jobs)
	return nil
}

// pcmRMS computes the root-mean-square amplitude of signed-16-bit-LE PCM,
// the cheap energy proxy our VAD thresholds on.
func pcmRMS(pcm []byte) float64 {
	n := len(pcm) / 2
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i+1 < len(pcm); i += 2 {
		sample := int16(uint16(pcm[i]) | uint16(pcm[i+1])<<8)
		f := float64(sample)
		sum += f * f
	}
	return math.Sqrt(sum / float64(n))
}
