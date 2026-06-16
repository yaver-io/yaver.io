package main

// stream_webrtc_audio.go — WebRTC audio (the Opus codec, NOT the AI model). Adds
// a second track to the live WebRTC stream so a viewer HEARS the source, not
// just sees it. ffmpeg reads the capture card's ALSA audio → encodes Opus →
// Ogg on stdout → pion's oggreader → WriteSample into a TrackLocalStaticSample.
//
// Shared per ALSA device (audio is independent of the video tier): one Opus
// encode fans out to every viewer of that device, refcounted like the video
// fan-out. ALSA is live, so reads are naturally real-time-paced — no ticker.
// Linux-only (ALSA); elsewhere audio is skipped and the stream stays video-only.

import (
	"context"
	"os/exec"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
)

type sharedAudio struct {
	device string
	track  *webrtc.TrackLocalStaticSample
	cancel context.CancelFunc
	pcs    map[*webrtc.PeerConnection]bool
}

var (
	audioMu sync.Mutex
	audios  = map[string]*sharedAudio{}
)

// getOrCreateAudio returns the shared Opus encode for an ALSA device, starting
// the ffmpeg→Opus pump on first use.
func getOrCreateAudio(device string) (*sharedAudio, error) {
	audioMu.Lock()
	defer audioMu.Unlock()
	if sa, ok := audios[device]; ok {
		return sa, nil
	}
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2},
		"audio", "yaver-audio")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	sa := &sharedAudio{device: device, track: track, cancel: cancel, pcs: map[*webrtc.PeerConnection]bool{}}
	audios[device] = sa
	go audioPump(ctx, device, track)
	return sa, nil
}

func (sa *sharedAudio) addPC(pc *webrtc.PeerConnection) {
	audioMu.Lock()
	sa.pcs[pc] = true
	audioMu.Unlock()
}

func (sa *sharedAudio) removePC(pc *webrtc.PeerConnection) {
	audioMu.Lock()
	delete(sa.pcs, pc)
	empty := len(sa.pcs) == 0
	if empty {
		delete(audios, sa.device)
	}
	audioMu.Unlock()
	if empty {
		sa.cancel() // stops the ffmpeg pump (CommandContext)
	}
}

// audioPump: ffmpeg ALSA → libopus → Ogg/Opus on stdout → oggreader pages →
// WriteSample with a granule-derived duration (correct regardless of how ffmpeg
// packs frames per page).
func audioPump(ctx context.Context, device string, track *webrtc.TrackLocalStaticSample) {
	ff := ffmpegPath()
	if ff == "" {
		return
	}
	cmd := exec.CommandContext(ctx, ff,
		"-f", "alsa", "-i", device,
		"-c:a", "libopus", "-b:a", "64k", "-ar", "48000", "-ac", "2",
		"-page_duration", "20000", // flush Ogg pages every ~20ms (low latency)
		"-f", "ogg", "pipe:1",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	defer func() { _ = cmd.Wait() }()
	ogg, _, err := oggreader.NewWith(stdout)
	if err != nil {
		return
	}
	var lastGranule uint64
	for {
		if ctx.Err() != nil {
			return
		}
		page, hdr, err := ogg.ParseNextPage()
		if err != nil {
			return // EOF / ffmpeg died / ctx cancelled killed it
		}
		sampleCount := float64(hdr.GranulePosition - lastGranule)
		lastGranule = hdr.GranulePosition
		dur := time.Duration((sampleCount/48000)*1000) * time.Millisecond
		if dur <= 0 {
			dur = 20 * time.Millisecond
		}
		if err := track.WriteSample(media.Sample{Data: page, Duration: dur}); err != nil {
			return
		}
	}
}
