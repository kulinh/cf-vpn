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

function isIPv4(hostname: string): boolean {
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
