/**
 * Event bus for OS-share-sheet payloads. The ShareIntentReceiver
 * (mounted at app root) decodes images shared into Yaver from any app's
 * share sheet and emits them here; ShareComposeModal subscribes and
 * opens the WhatsApp-style "add a comment + pick machines" sheet.
 *
 * `text` carries any caption/text the user shared alongside the image
 * (or shared text on its own) so the compose box can pre-fill it.
 */
import type { ImageAttachment } from "./quic";

type Listener = (images: ImageAttachment[], text?: string) => void;

class ShareIntentEmitter {
  private listeners: Listener[] = [];

  on(listener: Listener): () => void {
    this.listeners.push(listener);
    return () => {
      this.listeners = this.listeners.filter((l) => l !== listener);
    };
  }

  emit(images: ImageAttachment[], text?: string) {
    for (const listener of this.listeners) {
      listener(images, text);
    }
  }
}

export const shareIntentEmitter = new ShareIntentEmitter();
