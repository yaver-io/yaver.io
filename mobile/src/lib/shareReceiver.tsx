// shareReceiver.tsx — bridges the OS share sheet into Yaver.
//
// When the user screenshots a third-party app and taps Share → Yaver
// (the iOS Share Extension / Android ACTION_SEND intent the
// expo-share-intent config plugin wires up), this component decodes the
// shared image(s) to base64 ImageAttachments and hands them to
// shareIntentEmitter. ShareComposeModal then opens the WhatsApp-style
// "add a comment + pick machines" sheet.
//
// Mounted once at app root (app/_layout.tsx). Single instance — the
// useShareIntent hook reads the native module state on mount and
// foreground; mounting it twice would double-fire.
import { useEffect, useRef } from "react";
// expo-file-system v19 moved the string API (readAsStringAsync,
// EncodingType) to the `legacy` submodule — match designMode.ts /
// speech.ts which already import it this way.
import * as FileSystem from "expo-file-system/legacy";
import { useShareIntent } from "expo-share-intent";
import { shareIntentEmitter } from "./shareIntent";
import type { ImageAttachment } from "./quic";

function guessMime(name: string, fallback: string): string {
  const ext = (name.split(".").pop() || "").toLowerCase();
  if (ext === "png") return "image/png";
  if (ext === "jpg" || ext === "jpeg") return "image/jpeg";
  if (ext === "heic" || ext === "heif") return "image/heic";
  if (ext === "webp") return "image/webp";
  if (ext === "gif") return "image/gif";
  return fallback || "image/jpeg";
}

export function ShareIntentReceiver() {
  // resetOnBackground:false — we reset explicitly after decoding so a
  // backgrounding mid-compose doesn't drop the user's screenshot.
  const { hasShareIntent, shareIntent, resetShareIntent } = useShareIntent({
    resetOnBackground: false,
  });
  // De-dupe: useShareIntent re-renders on every reset; without this we
  // could decode + emit the same payload twice.
  const handledRef = useRef(false);

  useEffect(() => {
    if (!hasShareIntent) {
      handledRef.current = false;
      return;
    }
    if (handledRef.current) return;
    handledRef.current = true;

    let cancelled = false;
    (async () => {
      const files = shareIntent.files || [];
      const images: ImageAttachment[] = [];
      for (const f of files.slice(0, 5)) {
        const mime = f.mimeType || guessMime(f.fileName || "", "image/jpeg");
        if (!mime.startsWith("image/")) continue;
        if (!f.path) continue;
        try {
          const base64 = await FileSystem.readAsStringAsync(f.path, {
            encoding: FileSystem.EncodingType.Base64,
          });
          if (!base64) continue;
          images.push({
            base64,
            mimeType: mime,
            filename: f.fileName || `shared_${Date.now()}.jpg`,
          });
        } catch {
          // Unreadable share (revoked content URI, sandbox miss) —
          // skip that file rather than abort the whole share.
        }
      }
      if (cancelled) return;
      const text = (shareIntent.text || "").trim() || undefined;
      // Reset native state first so the next share is clean; the bus
      // hand-off is in-memory and survives the reset.
      resetShareIntent();
      if (images.length > 0 || text) {
        shareIntentEmitter.emit(images, text);
      }
    })();

    return () => {
      cancelled = true;
    };
    // shareIntent is a fresh object each render; gate on hasShareIntent.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hasShareIntent]);

  return null;
}
