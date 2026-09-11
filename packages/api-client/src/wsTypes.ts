// WebSocket JSON 协议信封（docs/04 §7，与 packages/contract/wsproto 对齐）。

export type ClientMsg =
  | { type: "sub"; symbols: string[] }
  | { type: "unsub"; symbols: string[] }
  | { type: "ping" }
  | { type: "ack"; deliveryId: string }
  | { type: "sub"; channels: string[] };

export interface ServerMsg {
  type: "pong" | "quote" | "kline" | "alert" | "sys";
  data?: unknown;
}

export interface SysData {
  code: string; // "drain" 等
}
