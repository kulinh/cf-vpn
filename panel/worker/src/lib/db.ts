export function nowTs(): number {
  return Date.now();
}

export async function all<T>(stmt: D1PreparedStatement): Promise<T[]> {
  const out = await stmt.all<T>();
  return out.results ?? [];
}

export async function one<T>(stmt: D1PreparedStatement): Promise<T | null> {
  return (await stmt.first<T>()) ?? null;
}

// Telegram callback_data is capped at 64 bytes and the longest wrapper the bot
// builds is "u:del:" + id + ":yes" (10 bytes), so an id longer than this can
// never be put on a button — reject it at creation instead of discovering it
// when /users silently stops answering.
export const MAX_ENTITY_ID_LEN = 54;

export function userIDFromName(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, MAX_ENTITY_ID_LEN)
    // A cut can land on a separator; don't leave a trailing dash behind.
    .replace(/-+$/g, "");
}

export function randomHex(bytes = 4): string {
  const buf = new Uint8Array(bytes);
  crypto.getRandomValues(buf);
  return Array.from(buf, (b) => b.toString(16).padStart(2, "0")).join("");
}
