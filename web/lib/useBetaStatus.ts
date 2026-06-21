import { useEffect, useState } from "react";
import { getManagedSubscription, type BetaStatus } from "@/lib/subscription";

// useBetaStatus — reads the beta entitlement from GET /subscription so the
// dashboard can render the focused Beta workspace (project + vibe box)
// instead of the full shell. Cosmetic only; real access is server-enforced
// (gateway caps + hidden infra grant). null while loading or for non-beta.
export function useBetaStatus(token: string | null | undefined): {
  beta: BetaStatus | null;
  loading: boolean;
} {
  const [beta, setBeta] = useState<BetaStatus | null>(null);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let cancelled = false;
    if (!token) {
      setBeta(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    getManagedSubscription(token).then((s) => {
      if (cancelled) return;
      setBeta(s?.beta ?? null);
      setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [token]);
  return { beta, loading };
}
