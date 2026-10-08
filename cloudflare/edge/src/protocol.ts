export interface RegisterMessage {
  type: "register";
  deviceId: string;
  token: string;
  password?: string;
}

export interface TunnelRequest {
  id: string;
  method: string;
  path: string;
  query: string;
  headers: Record<string, string>;
  body: string;
  targetPort?: number;
}

export interface TunnelResponse {
  id: string;
  statusCode: number;
  headers: Record<string, string>;
  body: string;
}

export interface TunnelFrame {
  type: "register" | "registered" | "request" | "response" | "error" | "ping" | "pong" |
    "stream_request" | "stream_start" | "stream_data" | "stream_end" | "stream_error" |
    "ws_open" | "ws_opened" | "ws_data" | "ws_close" | "ws_error";
    // Keep public client endpoints unchanged while multiplexing upgraded
    // WebSockets through the agent's one outbound edge connection.
  id?: string;
  register?: RegisterMessage;
  ok?: boolean;
  message?: string;
  request?: TunnelRequest;
  response?: TunnelResponse;
  statusCode?: number;
  headers?: Record<string, string>;
  data?: string;
  messageType?: number;
}
