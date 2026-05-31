// Shim for `react-native-udp` backed by Node's built-in `dgram`.
//
// The mobile lib's beacon listener (mobile/src/lib/beacon.ts) uses
// this to receive the agent's UDP broadcasts on :19837. Shipping
// dgram-backed here means `mobile-headless` genuinely discovers
// live agents on the LAN — no mocking.

import * as dgram from "node:dgram";

type MessageCb = (data: Buffer, rinfo: dgram.RemoteInfo) => void;
type ErrorCb = (err: Error) => void;
type ListeningCb = () => void;

class UdpSocket {
  private sock: dgram.Socket;
  constructor(type: "udp4" | "udp6" = "udp4") {
    this.sock = dgram.createSocket({ type, reuseAddr: true });
  }

  once(event: "listening", cb: ListeningCb): this;
  once(event: "error", cb: ErrorCb): this;
  once(event: "message", cb: MessageCb): this;
  once(event: string, cb: (...args: any[]) => void): this {
    this.sock.once(event as any, cb);
    return this;
  }

  on(event: "listening", cb: ListeningCb): this;
  on(event: "error", cb: ErrorCb): this;
  on(event: "message", cb: MessageCb): this;
  on(event: string, cb: (...args: any[]) => void): this {
    this.sock.on(event as any, cb);
    return this;
  }

  bind(port: number, cb?: () => void): this;
  bind(port: number, address: string, cb?: () => void): this;
  bind(port: number, addressOrCb?: string | (() => void), maybeCb?: () => void): this {
    if (typeof addressOrCb === "string") {
      this.sock.bind(port, addressOrCb, maybeCb);
    } else {
      this.sock.bind(port, addressOrCb);
    }
    return this;
  }

  send(
    buf: Buffer | string,
    offset: number,
    length: number,
    port: number,
    address: string,
    cb?: (err: Error | null) => void,
  ) {
    this.sock.send(buf, offset, length, port, address, cb);
  }

  setBroadcast(flag: boolean) {
    this.sock.setBroadcast(flag);
  }

  close(cb?: () => void) {
    this.sock.close(cb);
  }
}

export function createSocket(
  typeOrOpts: string | { type: "udp4" | "udp6"; reusePort?: boolean; reuseAddr?: boolean },
) {
  const type = typeof typeOrOpts === "string"
    ? (typeOrOpts as "udp4" | "udp6")
    : typeOrOpts.type;
  return new UdpSocket(type);
}

export default { createSocket };
