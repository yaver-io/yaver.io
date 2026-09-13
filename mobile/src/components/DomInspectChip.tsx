// DomInspectChip — the visible, switchable half of Yaver's DOM MODE (element
// inspect) on the phone: a Browse|Inspect radio plus the attached-element chip.
//
// ── The rule this exists to satisfy ───────────────────────────────────────
//
// SILENT PROMPT MUTATION IS A DEFECT. The agent prepends a block describing
// the element the user clicked (dom_inspect_turn.go, every turn including
// follow-ups). If the user cannot see that happening and cannot stop it, we
// have built exactly the kind of hidden behaviour this repo treats as a bug.
//
// So the chip states the element BY NAME ("div.card > button.submit — İleri
// →"), expands to the literal facts being sent, and switching back to Browse
// DELETES what was already reported (DELETE /dom-inspect), so "off" means the
// agent is not holding your element rather than holding it and promising not
// to look.
//
// ── Why it renders on the preview surface ─────────────────────────────────
//
// The selection and its disclosure live together in the DevPreview render /
// reload surface, behind that header's ellipsis. Ordinary Tasks must not show
// browser-only controls. The bridge (domInspectBridge.ts) still carries the
// selected element to the next Vibing turn.
//
// Browse|Inspect is a radio, not a toggle: while Inspect is on, clicks in the
// preview are intercepted (the real app cannot be used), so the exclusivity
// must be visible. DOM mode is opt-in — never default — and the probe
// auto-offs after a selection.

import React, { useCallback, useEffect, useState } from "react";
import { Pressable, Text, View } from "react-native";

import { useColors } from "../context/ThemeContext";
import {
  type ObservedDomElement,
  domInspectPrefReady,
  getObservedDomElement,
  setDomModeEnabled,
  subscribeDomInspect,
} from "../lib/domInspectBridge";
import { domInspectDetail, domInspectSummary, isDomInspectEnabled } from "../lib/domInspect";
import { getActivePreviewLane, subscribeActivePreviewLane } from "../lib/feedbackTrigger";
import { monoFamily } from "../theme/tokens";

export function DomInspectChip({
  /** The project this composer will send work to. When set, an element
   *  selected in a DIFFERENT project is not shown: the agent keys the element
   *  by workDir, so claiming attachment across projects would be a chip that
   *  lies. */
  workDir,
  style,
}: {
  workDir?: string | null;
  style?: any;
}) {
  const c = useColors();
  const [el, setEl] = useState<ObservedDomElement | null>(() => getObservedDomElement());
  const [inspect, setInspect] = useState(() => isDomInspectEnabled());
  const [expanded, setExpanded] = useState(false);
  /** True while a DOM-capable (browser/WebView/iframe) preview is active. The
   *  Hermes/native preview lane has NO DOM — the probe lives in pages the
   *  agent serves, and a native RN/Expo preview is views, not HTML. Offering
   *  the Inspect toggle there would be a false green: the user flips it, the
   *  probe never exists, and "click an element in the preview" is a lie. */
  const [domAvailable, setDomAvailable] = useState(
    () => getActivePreviewLane() === "browser",
  );

  useEffect(() => {
    let alive = true;
    const unsub = subscribeDomInspect(setEl);
    // The mode pref hydrates from AsyncStorage asynchronously. Re-read once it
    // has, rather than after a guessed delay: a chip mounted at cold start must
    // not render a state that lies about its own position.
    void domInspectPrefReady.then(() => {
      if (alive) setInspect(isDomInspectEnabled());
    });
    // React to the preview lane: the Inspect toggle is only honest while a
    // DOM-capable preview exists. If the lane becomes unavailable while Inspect
    // is on, drop the mode so the chip does not claim an attachment channel
    // that no longer exists (the attached element itself stays — it was
    // captured from a preview that existed). isDomInspectEnabled() is the
    // module-level truth, so the closure is never stale on the mode.
    const unsubLane = subscribeActivePreviewLane((lane) => {
      if (!alive) return;
      const avail = lane === "browser";
      setDomAvailable(avail);
      if (!avail && isDomInspectEnabled()) {
        setInspect(false);
        setDomModeEnabled(false, workDir);
      }
    });
    return () => {
      alive = false;
      unsub();
      unsubLane();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workDir]);

  const selectInspect = useCallback(
    (on: boolean) => {
      if (on && !domAvailable) return; // gate: no DOM-capable preview to click
      setInspect(on);
      setDomModeEnabled(on, on ? undefined : workDir);
      // The probe itself lives in the PREVIEW WebView, which this component
      // does not own (different tab). The preview screens subscribe to the
      // mode via domInspectBridge and inject the enable command into the page
      // when it flips on — see the wiring in apps.tsx / DevPreview.tsx.
    },
    [workDir, domAvailable],
  );

  // Nothing observed yet: render just the Browse|Inspect radio. A chip
  // reading "element: —" would assert a capability that is not currently
  // doing anything.
  const summary = el ? domInspectSummary(el.el) : "";
  const detail = el ? domInspectDetail(el.el) : [];
  const project = el?.workDir ? el.workDir.split("/").filter(Boolean).pop() || el.workDir : "";
  const unattachable = !el?.workDir;

  const activeTint = inspect ? c.brandPrimary : c.textMuted;

  return (
    <View
      style={[
        {
          borderWidth: 1,
          borderColor: c.border,
          backgroundColor: c.bgCardElevated,
          borderRadius: 10,
          paddingHorizontal: 10,
          paddingVertical: 6,
          gap: 6,
        },
        style,
      ]}
    >
      {/* Browse | Inspect radio */}
      <View
        style={{
          flexDirection: "row",
          alignItems: "center",
          gap: 6,
        }}
        accessibilityRole="radiogroup"
      >
        <Pressable
          onPress={() => selectInspect(false)}
          hitSlop={{ top: 8, bottom: 8, left: 4, right: 4 }}
          accessibilityRole="radio"
          accessibilityState={{ checked: !inspect }}
          aria-checked={!inspect}
          style={{
            borderRadius: 6,
            paddingHorizontal: 10,
            paddingVertical: 3,
            backgroundColor: !inspect ? c.brandPrimarySoft : "transparent",
          }}
        >
          <Text style={{ color: !inspect ? c.brandPrimary : c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 0.6 }}>
            BROWSE
          </Text>
        </Pressable>
        <Pressable
          onPress={() => selectInspect(true)}
          disabled={!domAvailable}
          hitSlop={{ top: 8, bottom: 8, left: 4, right: 4 }}
          accessibilityRole="radio"
          accessibilityState={{ checked: inspect, disabled: !domAvailable }}
          aria-checked={inspect}
          style={{
            borderRadius: 6,
            paddingHorizontal: 10,
            paddingVertical: 3,
            backgroundColor: inspect ? c.brandPrimarySoft : "transparent",
            opacity: domAvailable ? 1 : 0.45,
          }}
        >
          <Text style={{ color: inspect ? c.brandPrimary : c.textMuted, fontSize: 10, fontWeight: "700", letterSpacing: 0.6 }}>
            INSPECT
          </Text>
        </Pressable>
        {inspect ? (
          <Text style={{ color: c.brandPrimary, fontSize: 10, flexShrink: 1 }} numberOfLines={1}>
            click an element in the preview · Esc cancels
          </Text>
        ) : !domAvailable ? (
          <Text style={{ color: c.textMuted, fontSize: 10, flexShrink: 1 }} numberOfLines={2}>
            element inspect needs the web preview (not the native app preview)
          </Text>
        ) : null}
      </View>

      {/* Attached element */}
      {el && summary ? (
        <View
          style={{
            borderWidth: 1,
            borderColor: c.brandPrimarySoft,
            backgroundColor: c.brandPrimarySoft,
            borderRadius: 8,
            paddingHorizontal: 8,
            paddingVertical: 5,
            gap: 4,
          }}
        >
          <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
            <Pressable
              onPress={() => setExpanded((v) => !v)}
              hitSlop={{ top: 8, bottom: 8, left: 8, right: 4 }}
              style={{ flexDirection: "row", alignItems: "center", flexShrink: 1, flexGrow: 1, gap: 6 }}
              accessibilityRole="button"
              accessibilityLabel={`Element attached: ${summary}. Tap to see exactly what is sent with your prompt.`}
              accessibilityState={{ expanded }}
            >
              <Text style={{ color: c.brandPrimary, fontSize: 11, opacity: 0.8 }}>⤷</Text>
              <Text
                numberOfLines={1}
                style={{ color: c.brandPrimary, fontSize: 11, flexShrink: 1 }}
              >
                <Text style={{ opacity: 0.7 }}>element: </Text>
                {summary}
                {project ? <Text style={{ opacity: 0.7 }}> · {project}</Text> : null}
              </Text>
              <Text style={{ color: c.brandPrimary, fontSize: 9, opacity: 0.6 }}>{expanded ? "▾" : "▸"}</Text>
            </Pressable>
            <Pressable
              onPress={() => selectInspect(false)}
              hitSlop={{ top: 8, bottom: 8, left: 8, right: 8 }}
              accessibilityRole="button"
              accessibilityLabel="Stop attaching this element and delete it from the agent"
              style={{ borderWidth: 1, borderColor: c.brandPrimary, borderRadius: 6, paddingHorizontal: 6, paddingVertical: 2 }}
            >
              <Text style={{ color: c.brandPrimary, fontSize: 9, fontWeight: "700", letterSpacing: 0.6 }}>OFF</Text>
            </Pressable>
          </View>

          {expanded ? (
            <View style={{ borderTopWidth: 1, borderTopColor: c.brandPrimarySoft, paddingTop: 4, gap: 2 }}>
              {unattachable ? (
                <Text style={{ color: c.textSecondary, fontSize: 10 }}>
                  Selected in the preview, but Yaver doesn&apos;t know which project it belongs to yet, so nothing was
                  attached. Start the dev server from a project so the preview reports a working directory.
                </Text>
              ) : (
                <>
                  <Text style={{ color: c.textSecondary, fontSize: 10, opacity: 0.8 }}>
                    Sent with your prompt, so the agent audits the right element:
                  </Text>
                  {detail.map((line) => (
                    <Text key={line} style={{ color: c.textSecondary, fontSize: 10, fontFamily: monoFamily }}>
                      {line}
                    </Text>
                  ))}
                  {el.el.shot ? (
                    <Text style={{ color: c.textMuted, fontSize: 10 }}>screenshot: attached</Text>
                  ) : null}
                  <Text style={{ color: c.textMuted, fontSize: 10 }}>
                    Markup, styles and screenshot only — never what you type into a field. Stays on your machine; never
                    synced.
                  </Text>
                </>
              )}
            </View>
          ) : null}
        </View>
      ) : null}
    </View>
  );
}
