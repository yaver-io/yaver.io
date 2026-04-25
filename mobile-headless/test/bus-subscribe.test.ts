// Hermetic test for `MobileClient.subscribeBusEvents` — spins up a
// tiny in-process SSE server that emits the same `data: <json>\n\n`
// frame format the real agent uses (see desktop/agent/bus_http.go),
// then drives the mobile client against it. Locks in:
//   - prefix=peer is forwarded as a query param
//   - SSE frames are decoded into BusEvent objects and surfaced via onEvent
//   - the unsubscribe function aborts the stream cleanly
//
// This is the regression net for the mobile DeviceContext bus
// subscription wiring (mobile/src/context/DeviceContext.tsx). When
// the agent's bus topic shape or SSE framing changes, this test
// fails before the RN app does.

import { describe, it, expect } from "bun:test";
import * as http from "node:http";
import { AddressInfo } from "node:net";
import { MobileClient } from "../src/mobile-client";

interface BusFrame {
  id: string;
  topic: string;
  publisher: string;
  publishedAt: number;
  qos: 0 | 1;
  payload?: unknown;
}

async function startBusServer(token: string): Promise<{
  baseUrl: string;
  push: (frame: BusFrame) => void;
  close: () => Promise<void>;
  lastQuery: () => string | undefined;
}> {
  const subs = new Set<http.ServerResponse>();
  let lastQuery: string | undefined;

  const server = http.createServer((req, res) => {
    const url = new URL(req.url!, "http://localhost");
    if (url.pathname !== "/bus/events") {
      res.writeHead(404);
      res.end();
      return;
    }
    if (req.headers.authorization !== `Bearer ${token}`) {
      res.writeHead(401, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "unauthorized" }));
      return;
    }
    lastQuery = url.search.slice(1);
    res.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
    });
    res.write(": ready\n\n");
    subs.add(res);
    req.on("close", () => subs.delete(res));
  });

  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as AddressInfo).port;

  return {
    baseUrl: `http://127.0.0.1:${port}`,
    push: (frame) => {
      const wire = `data: ${JSON.stringify(frame)}\n\n`;
      for (const res of subs) res.write(wire);
    },
    close: async () => {
      for (const res of subs) res.end();
      subs.clear();
      await new Promise<void>((resolve, reject) =>
        server.close((err) => (err ? reject(err) : resolve())),
      );
    },
    lastQuery: () => lastQuery,
  };
}

describe("MobileClient.subscribeBusEvents", () => {
  it("decodes SSE frames into BusEvent objects, forwards prefix, unsubscribes cleanly", async () => {
    const srv = await startBusServer("mock-token");
    try {
      const client = new MobileClient({
        agentBaseUrl: srv.baseUrl,
        authToken: "mock-token",
      });

      const events: any[] = [];
      const errors: Error[] = [];
      const unsub = client.subscribeBusEvents({
        prefix: "peer",
        onEvent: (evt) => events.push(evt),
        onError: (err) => errors.push(err),
      });

      // Wait for the SSE handshake — server has to accept the
      // connection before we push frames or the writes go nowhere.
      await Bun.sleep(100);
      expect(srv.lastQuery()).toBe("prefix=peer");

      const peerId = "abc12345-de56-7890-abcd-ef0123456789";
      srv.push({
        id: "evt-1",
        topic: `peer/${peerId}/online`,
        publisher: peerId,
        publishedAt: Date.now(),
        qos: 1,
        payload: { hostname: "kivancs-mac", platform: "darwin" },
      });
      srv.push({
        id: "evt-2",
        topic: `peer/${peerId}/ping`,
        publisher: peerId,
        publishedAt: Date.now(),
        qos: 0,
      });

      // SSE delivery is async — give the reader loop a tick.
      await Bun.sleep(150);

      expect(events.length).toBe(2);
      expect(events[0].topic).toBe(`peer/${peerId}/online`);
      expect(events[0].publisher).toBe(peerId);
      expect((events[0].payload as any).hostname).toBe("kivancs-mac");
      expect(events[1].topic).toBe(`peer/${peerId}/ping`);
      expect(errors.length).toBe(0);

      unsub();
      // After unsub, late frames must not surface.
      srv.push({
        id: "evt-3",
        topic: `peer/${peerId}/online`,
        publisher: peerId,
        publishedAt: Date.now(),
        qos: 1,
      });
      await Bun.sleep(50);
      expect(events.length).toBe(2);
    } finally {
      await srv.close();
    }
  });

  it("surfaces non-2xx responses through onError without throwing", async () => {
    const srv = await startBusServer("right-token");
    try {
      const client = new MobileClient({
        agentBaseUrl: srv.baseUrl,
        authToken: "wrong-token",
      });
      let err: Error | undefined;
      const unsub = client.subscribeBusEvents({
        onEvent: () => {},
        onError: (e) => {
          err = e;
        },
      });
      await Bun.sleep(100);
      expect(err).toBeDefined();
      expect(err!.message).toMatch(/HTTP 401/);
      unsub();
    } finally {
      await srv.close();
    }
  });
});
