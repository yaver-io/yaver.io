#!/usr/bin/env node
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { chromium } = require("playwright");

const agentBase = process.env.AGENT_BASE || "http://127.0.0.1:18080";
const token = process.env.AGENT_TOKEN || "";
const remotePageUrl = process.env.REMOTE_PAGE_URL || "";

if (!token || !remotePageUrl) {
  console.error("AGENT_TOKEN and REMOTE_PAGE_URL are required");
  process.exit(2);
}

const browser = await chromium.launch({
  headless: true,
});
const page = await browser.newPage();

try {
  const result = await page.goto(`${agentBase}/health`).then(() =>
    page.evaluate(
      async ({ agentBase, token, remotePageUrl }) => {
        const headers = { Authorization: `Bearer ${token}` };
        const jsonFetch = async (path, init = {}) => {
          const res = await fetch(`${agentBase}${path}`, {
            ...init,
            headers: { ...headers, ...(init.headers || {}) },
            cache: "no-store",
          });
          const data = await res.json().catch(() => ({}));
          if (!res.ok) throw new Error(data.error || `${path} HTTP ${res.status}`);
          return data;
        };
        const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
        const waitForIce = (pc) =>
          new Promise((resolve) => {
            if (pc.iceGatheringState === "complete") return resolve();
            const done = () => {
              if (pc.iceGatheringState === "complete") {
                pc.removeEventListener("icegatheringstatechange", done);
                resolve();
              }
            };
            pc.addEventListener("icegatheringstatechange", done);
            setTimeout(resolve, 2000);
          });

        const avgRGB = async (data) => {
          const blob = data instanceof Blob ? data : new Blob([data], { type: "image/jpeg" });
          const bitmap = await createImageBitmap(blob);
          const canvas = document.createElement("canvas");
          const cropW = Math.min(240, bitmap.width);
          const cropH = Math.min(240, bitmap.height);
          canvas.width = cropW;
          canvas.height = cropH;
          const ctx = canvas.getContext("2d");
          ctx.drawImage(
            bitmap,
            Math.max(0, Math.floor(bitmap.width / 2 - cropW / 2)),
            Math.max(0, Math.floor(bitmap.height / 2 - cropH / 2)),
            cropW,
            cropH,
            0,
            0,
            cropW,
            cropH,
          );
          const pixels = ctx.getImageData(0, 0, cropW, cropH).data;
          let r = 0;
          let g = 0;
          let b = 0;
          const count = pixels.length / 4;
          for (let i = 0; i < pixels.length; i += 4) {
            r += pixels[i];
            g += pixels[i + 1];
            b += pixels[i + 2];
          }
          return {
            r: Math.round(r / count),
            g: Math.round(g / count),
            b: Math.round(b / count),
          };
        };
        const isRed = ({ r, g, b }) => r >= 150 && g <= 120 && b <= 120 && r > g + 40;
        const isGreen = ({ r, g, b }) => g >= 130 && r <= 140 && b <= 140 && g > r + 40;
        const waitForFrame = (predicate, label, timeoutMs = 15000) =>
          new Promise((resolve, reject) => {
            const started = performance.now();
            const timer = setInterval(async () => {
              if (performance.now() - started > timeoutMs) {
                clearInterval(timer);
                reject(new Error(`timed out waiting for ${label}`));
                return;
              }
              const item = frameQueue.shift();
              if (!item) return;
              try {
                const rgb = await avgRGB(item);
                if (predicate(rgb)) {
                  clearInterval(timer);
                  resolve({ rgb, elapsedMs: Math.round(performance.now() - started) });
                }
              } catch (error) {
                clearInterval(timer);
                reject(error);
              }
            }, 120);
          });

        const caps = await jsonFetch("/remote-runtime/capabilities?framework=browser&workDir=/tmp");
        const browserTarget = (caps.targets || []).find((target) => target.id === "browser-window");
        if (!browserTarget?.enabled) throw new Error(`browser-window disabled: ${browserTarget?.reason || "missing"}`);

        const session = await jsonFetch("/remote-runtime/sessions", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            workDir: "/tmp",
            framework: "browser",
            targetId: "browser-window",
            transportMode: "direct-webrtc",
          }),
        });
        let sessionDeleted = false;
        const cleanup = async () => {
          if (sessionDeleted) return;
          sessionDeleted = true;
          await fetch(`${agentBase}/remote-runtime/sessions/${encodeURIComponent(session.id)}`, {
            method: "DELETE",
            headers,
          }).catch(() => {});
        };

        const frameQueue = [];
        const timings = { createdAt: performance.now() };
        try {
          await jsonFetch(`/remote-runtime/sessions/${encodeURIComponent(session.id)}/control`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              action: "navigate",
              url: remotePageUrl,
              clientId: "ci-browser-webrtc-color",
              clientLabel: "CI browser WebRTC color smoke",
            }),
          });
          await wait(300);

          let iceServers = [{ urls: "stun:stun.l.google.com:19302" }];
          try {
            const ice = await jsonFetch("/stream/webrtc/ice");
            if (Array.isArray(ice.iceServers) && ice.iceServers.length > 0) iceServers = ice.iceServers;
          } catch {}

          const pc = new RTCPeerConnection({ iceServers });
          pc.createDataChannel("primer");
          pc.addTransceiver("video", { direction: "recvonly" });
          pc.ondatachannel = (event) => {
            if (event.channel.label !== "frames") return;
            event.channel.binaryType = "arraybuffer";
            event.channel.onmessage = (message) => frameQueue.push(message.data);
          };
          pc.ontrack = (event) => {
            const video = document.createElement("video");
            video.muted = true;
            video.autoplay = true;
            video.playsInline = true;
            video.srcObject = event.streams[0];
            document.body.append(video);
            video.play().catch(() => {});
          };
          const offer = await pc.createOffer();
          await pc.setLocalDescription(offer);
          await waitForIce(pc);
          const local = pc.localDescription || offer;
          const answer = await jsonFetch(`/remote-runtime/sessions/${encodeURIComponent(session.id)}/webrtc/offer`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ type: local.type, sdp: local.sdp }),
          });
          await pc.setRemoteDescription({
            type: answer.answer?.type || "answer",
            sdp: answer.answer?.sdp || "",
          });

          const before = await waitForFrame(isRed, "red WebRTC frame");
          await jsonFetch(`/remote-runtime/sessions/${encodeURIComponent(session.id)}/control`, {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              action: "tap",
              x: 640,
              y: 400,
              clientId: "ci-browser-webrtc-color",
              clientLabel: "CI browser WebRTC color smoke",
            }),
          });
          const after = await waitForFrame(isGreen, "green WebRTC frame");
          pc.close();
          await cleanup();
          return {
            ok: true,
            sessionId: session.id,
            negotiatedTransport: answer.transport || answer.session?.frameTransport || "",
            before,
            after,
            totalMs: Math.round(performance.now() - timings.createdAt),
          };
        } catch (error) {
          await cleanup();
          throw error;
        }
      },
      { agentBase, token, remotePageUrl },
    )
  );
  console.log(JSON.stringify(result, null, 2));
} finally {
  await browser.close().catch(() => {});
}
