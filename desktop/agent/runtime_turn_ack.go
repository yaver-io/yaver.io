package main

// Device-side acknowledgement for runtime-turn reloads.
//
// This closes the last step of the inventory-vs-operation chain:
//
//	task finished        -> the CODE changed                    (`unverified`)
//	broadcast accepted   -> a live phone took the command       (`delivered`)
//	device reports back  -> the bundle actually loaded          (`verified`)
//
// Everything before the last line is the agent talking about its own side of
// the wire. Only the device can say whether the app really came back up, so
// `verified` is set from an event the DEVICE emitted, never inferred here.
//
// The transport is deliberately the existing black-box event stream rather
// than a new endpoint: the phone already POSTs `preview_worker_bundle_loaded`
// / `preview_worker_bundle_load_failed` after a reload. All this adds is a
// correlation id so the agent can tell WHICH turn an outcome belongs to.

import "strings"

const (
	// runtimeTurnAckMetaKey is the correlation id carried out on the reload
	// command and echoed back in the device's event metadata.
	runtimeTurnAckMetaKey = "turnId"

	runtimeTurnAckLoadedMessage = "preview_worker_bundle_loaded"
	runtimeTurnAckFailedPrefix  = "preview_worker_bundle_load_failed"
	// A build with no native bundle loader can never reload. That is a
	// verification FAILURE, not silence — otherwise the turn sits on
	// `delivered` forever and the user waits for something that cannot happen.
	runtimeTurnAckUnsupportedMessage = "preview_worker_bundle_load_unsupported_platform"
)

// runtimeTurnObserveDeviceEvent inspects one inbound black-box event and, if it
// carries a runtime-turn correlation id, records the REAL reload outcome.
//
// Unrelated events are the overwhelming majority, so this returns fast and
// never blocks the event-ingest path.
func runtimeTurnObserveDeviceEvent(deviceID string, e BlackBoxEvent) {
	turnID := runtimeTurnIDFromMetadata(e.Metadata)
	if turnID == "" {
		return
	}
	state, detail, ok := runtimeTurnAckOutcome(e)
	if !ok {
		return
	}
	runtimeQueue.updateAny(turnID, func(i *RuntimeTurnQueueItem) {
		// Never downgrade a verified turn because a later unrelated reload
		// failed on some other device.
		if i.TestTarget != nil && i.TestTarget.State == "verified" && state != "verified" {
			return
		}
		if i.TestTarget == nil {
			i.TestTarget = &RuntimeTurnTestTarget{Kind: "yaver-mobile-container"}
		}
		i.TestTarget.State = state
		i.TestTarget.Detail = detail
		if deviceID != "" && deviceID != "unknown" {
			i.TestTarget.DeviceID = deviceID
		}
		if state == "verified" {
			i.Spoken = "It's live on your phone."
		} else {
			i.Spoken = "The reload failed on the device."
		}
	})
}

// runtimeTurnAckOutcome maps a device event to a test-target state. The bool
// reports whether this event says anything about a reload at all.
func runtimeTurnAckOutcome(e BlackBoxEvent) (state, detail string, ok bool) {
	msg := strings.TrimSpace(e.Message)
	switch {
	case msg == runtimeTurnAckLoadedMessage:
		return "verified", "the device loaded the bundle and reported back", true
	case strings.HasPrefix(msg, runtimeTurnAckFailedPrefix):
		reason := strings.TrimSpace(strings.TrimPrefix(msg, runtimeTurnAckFailedPrefix))
		reason = strings.TrimSpace(strings.TrimPrefix(reason, ":"))
		if reason == "" {
			reason = "the device could not load the bundle"
		}
		return "failed", reason, true
	case msg == runtimeTurnAckUnsupportedMessage:
		return "failed", "this build of Yaver has no native bundle loader; update the app", true
	default:
		return "", "", false
	}
}

func runtimeTurnIDFromMetadata(meta map[string]interface{}) string {
	if meta == nil {
		return ""
	}
	raw, ok := meta[runtimeTurnAckMetaKey]
	if !ok {
		return ""
	}
	s, ok := raw.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}
