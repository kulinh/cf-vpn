import type { Env } from "../types";

export const ADMIN_HOST_ZONE = "rwl247.dev";

export function normalizeNodeIDForHost(id: string): string | null {
  const normalized = id.trim().toLowerCase();
  if (!/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(normalized)) {
    return null;
  }
  return normalized;
}

export function generateAdminHost(nodeID: string): string | null {
  const normalized = normalizeNodeIDForHost(nodeID);
  return normalized ? `${normalized}.${ADMIN_HOST_ZONE}` : null;
}

// A plain DNS name as it may appear in the authority of a client URI: at least
// two labels, [a-z0-9-] only, no leading/trailing hyphen, no trailing dot, no
// port/path/userinfo. Anything else reported by an agent or typed into the
// panel would be concatenated raw into subscription URIs and DNS calls.
export function isDnsHostname(value: unknown): value is string {
  if (typeof value !== "string" || value.length === 0 || value.length > 253) return false;
  const labels = value.split(".");
  if (labels.length < 2) return false;
  return labels.every((l) => /^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(l));
}

// A bare IPv6 literal: no brackets, no zone id, not IPv4-mapped/dotted.
export function isIPv6Literal(value: unknown): value is string {
  if (typeof value !== "string" || !value.includes(":") || /[^0-9A-Fa-f:]/.test(value)) return false;
  if (value.length > 39 || (value.match(/::/g) ?? []).length > 1 || /:::/.test(value)) return false;
  if ((value.startsWith(":") && !value.startsWith("::")) || (value.endsWith(":") && !value.endsWith("::"))) return false;
  const groups = value.split(":");
  if (groups.some((g) => g.length > 4)) return false;
  return value.includes("::") ? groups.length <= 8 : groups.length === 8;
}

export function isIPv4(hostname: unknown): boolean {
  if (typeof hostname !== "string") return false;
  if (!/^\d+\.\d+\.\d+\.\d+$/.test(hostname)) {
    return false;
  }
  const parts = hostname.split(".").map(Number);
  return parts.length === 4 && parts.every((part) => Number.isInteger(part) && part >= 0 && part <= 255);
}

function isIPLiteral(hostname: string): boolean {
  return isIPv4(hostname) || hostname.includes(":");
}

function allowedSuffixes(env: Env): string[] {
  return (env.ADMIN_HOST_ALLOWED_SUFFIXES || "")
    .split(",")
    .map((x) => x.trim().toLowerCase().replace(/^\.+/, ""))
    .filter(Boolean);
}

export function validateAdminHost(adminHost: string, env: Env): string | null {
  const raw = adminHost.trim().toLowerCase();
  if (!raw) {
    return "admin_host is required";
  }

  let hostname: string;
  try {
    const parsed = new URL(`https://${raw}`);
    if (parsed.hostname !== raw || parsed.pathname !== "/" || parsed.search || parsed.hash || parsed.port) {
      return "admin_host must be a plain hostname without path, query, hash, or port";
    }
    hostname = parsed.hostname;
  } catch {
    return "admin_host is not a valid hostname";
  }

  if (hostname === "localhost" || hostname.endsWith(".localhost")) {
    return "localhost admin_host is not allowed";
  }
  if (isIPLiteral(hostname)) {
    return "IP admin_host is not allowed";
  }

  const suffixes = allowedSuffixes(env);
  if (suffixes.length === 0) {
    return "ADMIN_HOST_ALLOWED_SUFFIXES is not configured";
  }

  const matched = suffixes.some((suffix) => hostname === suffix || hostname.endsWith(`.${suffix}`));
  if (!matched) {
    return "admin_host is outside configured allowlist";
  }

  return null;
}
