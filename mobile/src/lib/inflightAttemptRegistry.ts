// inflightAttemptRegistry.ts — ownership-safe dedupe for asynchronous attempts.
//
// Deleting a client does not cancel its Promise. A late `.finally()` from that
// retired attempt must therefore never delete a newer attempt registered under
// the same device id. This tiny registry makes both invalidation and ownership
// explicit and keeps that race independently testable.

export class InflightAttemptRegistry<T extends object> {
  private readonly entries = new Map<string, T>();

  get(key: string): T | undefined {
    return this.entries.get(key);
  }

  set(key: string, attempt: T): void {
    this.entries.set(key, attempt);
  }

  /** Remove only when `attempt` still owns this key. */
  release(key: string, attempt: T): boolean {
    if (this.entries.get(key) !== attempt) return false;
    this.entries.delete(key);
    return true;
  }

  /** Abandon the current owner so a retry may start immediately. */
  invalidate(key: string): void {
    this.entries.delete(key);
  }

  clear(): void {
    this.entries.clear();
  }
}
