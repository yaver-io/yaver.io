export type EVProviderId =
  | "esarj"
  | "zes"
  | "trugo"
  | "enyakit"
  | "voltrun"
  | "sharz"
  | "sarjtr"
  | "unknown";

export type EVRouteKind =
  | "provider_deeplink"
  | "remote_android"
  | "manual_assist";

export type EVChargingState =
  | "idle"
  | "scanning"
  | "qr_captured"
  | "provider_identified"
  | "route_selected"
  | "auth_check"
  | "awaiting_user_confirmation"
  | "starting"
  | "charging"
  | "stopping"
  | "complete"
  | "provider_unknown"
  | "qr_unreadable"
  | "connector_unknown"
  | "auth_required"
  | "otp_required"
  | "payment_required"
  | "proximity_required"
  | "provider_app_required"
  | "remote_android_unavailable"
  | "blocked_by_provider"
  | "manual_only"
  | "cancelled"
  | "failed";

export type EVConfidence = "high" | "medium" | "low";

export interface EVParsedQR {
  provider: EVProviderId;
  normalizedUrl?: string;
  stationId?: string;
  connectorId?: string;
  chargerId?: string;
  socketLabel?: string;
  confidence: EVConfidence;
  notes: string[];
}

export interface EVApproval {
  id: string;
  at: number;
  kind: "login" | "otp" | "payment" | "start" | "stop";
  label: string;
  approved: boolean;
}

export interface EVEvent {
  at: number;
  type: string;
  message: string;
  data?: Record<string, unknown>;
}

export interface EVChargingIntent {
  id: string;
  createdAt: number;
  updatedAt: number;
  state: EVChargingState;
  provider: EVProviderId;
  route?: EVRouteKind;
  rawQr?: string;
  normalizedUrl?: string;
  stationId?: string;
  connectorId?: string;
  chargerId?: string;
  socketLabel?: string;
  approvals: EVApproval[];
  events: EVEvent[];
}

export interface EVRouteEnv {
  hasActiveYaverDevice: boolean;
  hasRemoteAndroid: boolean;
  hasProviderUrl: boolean;
}

export interface EVRouteOption {
  kind: EVRouteKind;
  label: string;
  description: string;
  risk: "low" | "medium" | "high";
  requiresUserPresent: boolean;
  requiresApproval: boolean;
  available: boolean;
  unavailableReason?: string;
}

export interface EVProviderAdapter {
  id: EVProviderId;
  label: string;
  domains: string[];
  androidPackageHints: string[];
  parseQr(raw: string): EVParsedQR | null;
  buildRoutes(intent: EVChargingIntent, env: EVRouteEnv): EVRouteOption[];
}
