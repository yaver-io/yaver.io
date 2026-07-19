"use client";

// Retired with the flat product model.
// BillingView is the customer-facing surface for Free, Relay Pro, and Cloud
// Workspace. Keeping this component as a no-op avoids reintroducing the old
// wallet-backed à-la-carte managed capability cockpit while dashboard layout
// call sites are being simplified.
export function CapabilityShelf({ token: _token }: { token: string | null }) {
  return null;
}

export default CapabilityShelf;
