package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/pion/webrtc/v4"
)

const (
	remoteRuntimeTransportRTPH264 = "webrtc-rtp-h264-v1"
	remoteRuntimeTransportJPEGDC  = "webrtc-datachannel-jpeg-v1"
)

// remoteRuntimeStreamer is the facade between WebRTC signaling and the
// platform-specific capture implementations. Viewers always negotiate
// through the same HTTP/WebRTC surface; this interface decides whether
// the underlying stream is an RTP H.264 track, JPEG data-channel frames,
// or a future backend such as a Chrome/WebCodecs streamer.
type remoteRuntimeStreamer interface {
	Transport() string
	UsesRTP() bool
	ConfigurePeer(pc *webrtc.PeerConnection, live *remoteRuntimeLiveState, existingTrack *webrtc.TrackLocalStaticSample) (*webrtc.TrackLocalStaticSample, *webrtc.DataChannel, error)
	Start(ctx context.Context, live *remoteRuntimeLiveState, mgr *RemoteRuntimeManager)
}

func selectRemoteRuntimeStreamer(targetID, offerSDP string) remoteRuntimeStreamer {
	if offerWantsVideo(offerSDP) && agentCanEncodeRTPH264(targetID) {
		return rtpH264Streamer{}
	}
	return jpegDataChannelStreamer{}
}

func offerWantsVideo(sdp string) bool {
	return strings.Contains(sdp, "m=video")
}

type rtpH264Streamer struct{}

func (rtpH264Streamer) Transport() string { return remoteRuntimeTransportRTPH264 }
func (rtpH264Streamer) UsesRTP() bool     { return true }

func (rtpH264Streamer) ConfigurePeer(pc *webrtc.PeerConnection, live *remoteRuntimeLiveState, existingTrack *webrtc.TrackLocalStaticSample) (*webrtc.TrackLocalStaticSample, *webrtc.DataChannel, error) {
	videoTrack := existingTrack
	if videoTrack == nil {
		track, err := webrtc.NewTrackLocalStaticSample(
			webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000},
			"yaver-runtime", "yaver-stream",
		)
		if err != nil {
			return nil, nil, fmt.Errorf("create h264 track: %w", err)
		}
		videoTrack = track
	}
	if _, err := pc.AddTrack(videoTrack); err != nil {
		return nil, nil, fmt.Errorf("add track: %w", err)
	}
	return videoTrack, nil, nil
}

func (rtpH264Streamer) Start(ctx context.Context, live *remoteRuntimeLiveState, _ *RemoteRuntimeManager) {
	live.mu.Lock()
	track := live.videoTrack
	pumpRunning := live.videoPump != nil
	live.mu.Unlock()
	if track == nil || pumpRunning {
		return
	}
	pump := newVideoTrackPump(live.targetID, live.deviceID, track, func(ev map[string]any) {
		live.sendEventJSON(ev)
	})
	live.mu.Lock()
	if live.videoPump != nil {
		live.mu.Unlock()
		return
	}
	live.videoPump = pump
	live.mu.Unlock()
	pump.Start(ctx)
}

type jpegDataChannelStreamer struct{}

func (jpegDataChannelStreamer) Transport() string { return remoteRuntimeTransportJPEGDC }
func (jpegDataChannelStreamer) UsesRTP() bool     { return false }

func (jpegDataChannelStreamer) ConfigurePeer(pc *webrtc.PeerConnection, _ *remoteRuntimeLiveState, _ *webrtc.TrackLocalStaticSample) (*webrtc.TrackLocalStaticSample, *webrtc.DataChannel, error) {
	dc, err := pc.CreateDataChannel("frames", nil)
	if err != nil {
		return nil, nil, err
	}
	return nil, dc, nil
}

func (jpegDataChannelStreamer) Start(_ context.Context, live *remoteRuntimeLiveState, mgr *RemoteRuntimeManager) {
	live.startFramePump(mgr)
}
