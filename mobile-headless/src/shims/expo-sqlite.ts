// Shim for `expo-sqlite`, imported by mobile/src/lib/phoneSandboxLocal.ts.
//
// The phone-sandbox SQLite path is a device-only feature (the on-phone
// mini-backend). mobile-headless pulls the file into its type graph via
// quic.ts but never runs it, so this provides a type-complete surface
// that throws if actually invoked — making misuse obvious rather than
// silently returning wrong data.

export interface SQLiteDatabase {
  execAsync(sql: string): Promise<void>;
  runAsync(sql: string, ...params: any[]): Promise<{ lastInsertRowId: number; changes: number }>;
  getAllAsync<T = any>(sql: string, ...params: any[]): Promise<T[]>;
  getFirstAsync<T = any>(sql: string, ...params: any[]): Promise<T | null>;
  closeAsync(): Promise<void>;
}

export async function openDatabaseAsync(_name: string): Promise<SQLiteDatabase> {
  throw new Error("expo-sqlite is not available in the headless (Node/Bun) runtime");
}

export default { openDatabaseAsync };
