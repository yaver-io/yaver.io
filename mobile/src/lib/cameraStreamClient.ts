// cameraStreamClient — push THIS phone's camera frames to a box's stream plane
// (M10). The box buffers them and serves them via stream_list / stream_snapshot,
// so the phone camera becomes a shareable source (own account or a guest watch
// link) with no inbound connection to the phone. Posts to the agent's
// /stream/push over the existing mesh transport (LAN-direct or relay).
import { quicClient } from "./quic";

export async function pushCameraFrame(deviceId: string, name: string, jpegB64: string): Promise<boolean> {
  try {
    const res = await quicClient.agentRequest(
      deviceId,
      `/stream/push?name=${encodeURIComponent(name)}`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ jpegB64 }),
      },
      8000,
    );
    return res.ok;
  } catch {
    return false;
  }
}
