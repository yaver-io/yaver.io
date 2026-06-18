import { describe, expect, it } from "bun:test";
import * as http from "node:http";
import { AddressInfo } from "node:net";
import { MobileClient } from "../src/mobile-client";

async function withMockConvex<T>(
  handler: (req: http.IncomingMessage, body: any) => { status: number; body: any },
  run: (baseUrl: string) => Promise<T>,
): Promise<T> {
  const server = http.createServer(async (req, res) => {
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
    const body = chunks.length ? JSON.parse(Buffer.concat(chunks).toString("utf8")) : null;
    const reply = handler(req, body);
    res.writeHead(reply.status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(reply.body));
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as AddressInfo).port;
  try {
    return await run(`http://127.0.0.1:${port}`);
  } finally {
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
}

describe("guest invitation flow", () => {
  it("sends scoped guest invite payloads for selected remote machines", async () => {
    let seenBody: any = null;
    let seenAuth = "";

    await withMockConvex(
      (req, body) => {
        expect(req.url).toBe("/guests/invite");
        seenBody = body;
        seenAuth = String(req.headers.authorization || "");
        return {
          status: 200,
          body: {
            inviteCode: "ABC123",
            guestRegistered: false,
            guestEmail: body.email,
            scope: body.scope,
          },
        };
      },
      async (convexUrl) => {
        const mobile = new MobileClient({ convexUrl, authToken: "owner-token" });
        const result = await mobile.guests.invite({
          email: "guest@example.com",
          deviceIds: ["hetzner"],
          scope: "full",
          allowedProjects: ["workspace"],
        });
        expect(result.inviteCode).toBe("ABC123");
      },
    );

    expect(seenAuth).toBe("Bearer owner-token");
    expect(seenBody).toEqual({
      email: "guest@example.com",
      deviceIds: ["hetzner"],
      scope: "full",
      allowedProjects: ["workspace"],
    });
  });
});
