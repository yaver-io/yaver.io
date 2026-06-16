package main

// stream_webrtc_fanout.go — M15 Q4: tiered simulcast fan-out. Instead of a fresh
// ffmpeg encode per WebRTC viewer (CPU death on a Pi), share ONE encode per
// (source, tier): a single videoTrackPump feeds a single TrackLocalStaticSample,
// and Pion fans its RTP out to every PeerConnection the track is attached to.
// Viewers at the same quality tier piggy-back on one encode; the box runs at
// most one ffmpeg per tier per source regardless of viewer count (§H.6).
//
// Lifecycle is refcounted under one mutex so get-or-create and last-viewer
// teardown are atomic (no "attach to a dying encode" race).

import (
	"context"
	"sync"

	"github.com/pion/webrtc/v4"
)

type sharedEncode struct {
	key   string // "source:tier"
	track *webrtc.TrackLocalStaticSample
	pump  *videoTrackPump
	pcs   map[*webrtc.PeerConnection]bool
}

var (
	encodesMu sync.Mutex
	encodes   = map[string]*sharedEncode{}
)

// getOrCreateEncode returns the shared encode for (source, profile.tier),
// creating + starting its pump on first use. The pump's deviceID is the full
// "source:tier" key so SpawnCapture applies THIS tier's profile while pulling
// frames from the bare source.
func getOrCreateEncode(source string, prof StreamProfile) (*sharedEncode, error) {
	key := source + ":" + prof.Name
	encodesMu.Lock()
	defer encodesMu.Unlock()
	if se, ok := encodes[key]; ok {
		return se, nil
	}
	track, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264}, "video", "yaver-"+key)
	if err != nil {
		return nil, err
	}
	se := &sharedEncode{key: key, track: track, pcs: map[*webrtc.PeerConnection]bool{}}
	se.pump = newVideoTrackPump("stream-"+source, key, track, nil)
	encodes[key] = se
	setActiveEncodeProfile(key, prof)
	se.pump.Start(context.Background())
	return se, nil
}

func (se *sharedEncode) addPC(pc *webrtc.PeerConnection) {
	encodesMu.Lock()
	se.pcs[pc] = true
	encodesMu.Unlock()
}

// removePC drops a viewer; when the last one leaves, the encoder stops and the
// entry is removed — atomic with getOrCreateEncode so a concurrent new viewer
// either reuses this encode (before delete) or makes a fresh one (after).
func (se *sharedEncode) removePC(pc *webrtc.PeerConnection) {
	encodesMu.Lock()
	delete(se.pcs, pc)
	empty := len(se.pcs) == 0
	if empty {
		delete(encodes, se.key)
	}
	encodesMu.Unlock()
	if empty {
		se.pump.Stop()
		activeEncodeProfile.Delete(se.key)
	}
}

// sharedEncodeStatus reports the live tiered encodes (for stream_quality_get).
func sharedEncodeStatus() []map[string]interface{} {
	encodesMu.Lock()
	defer encodesMu.Unlock()
	out := []map[string]interface{}{}
	for key, se := range encodes {
		out = append(out, map[string]interface{}{"tier": key, "viewers": len(se.pcs)})
	}
	return out
}
