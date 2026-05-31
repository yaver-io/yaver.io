// Shim for `@react-native-community/netinfo`, imported by
// mobile/src/lib/quic.ts to gate direct-vs-relay on the link type.
// Headless always runs on a "real" network (a server/laptop), so we
// report a wired connection — quic.ts then prefers direct-first, which
// is correct for a Node/Bun host on a LAN.

export interface NetInfoState {
  type: string;
  isConnected: boolean | null;
  isInternetReachable: boolean | null;
}

type Listener = (state: NetInfoState) => void;

const NetInfo = {
  async fetch(): Promise<NetInfoState> {
    return { type: "ethernet", isConnected: true, isInternetReachable: true };
  },
  addEventListener(_cb: Listener): () => void {
    return () => {};
  },
};

export default NetInfo;
