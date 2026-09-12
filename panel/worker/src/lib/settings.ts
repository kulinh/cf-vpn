import type { Env } from "../types";
import { one } from "./db";

// Fleet-wide key/value settings (migration 0022). Readers must survive a
// missing table or a D1 hiccup: a setting is a default, never a hard
// dependency of the request that reads it.
export const RULES_MODE_KEY = "rules_mode";

export async function getSetting(env: Env, key: string): Promise<string | null> {
  try {
    const row = await one<{ value: string }>(env.DB.prepare("SELECT value FROM settings WHERE key=?").bind(key));
    return row?.value ?? null;
  } catch {
    return null;
  }
}
