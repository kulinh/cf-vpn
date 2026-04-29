// Mirror of internal/zones (Go). Keep PREFIXES + generateHost + pickZone
// behavior in lockstep with random.go.

export const PREFIXES = ["cdn", "static", "assets", "edge", "media"] as const;

export class NoEligibleZonesError extends Error {
  constructor() {
    super("no eligible zones");
    this.name = "NoEligibleZonesError";
  }
}

export type ZoneCandidate = { name: string; cf_zone_id?: string };
export type Rng = (n: number) => Uint8Array;

function toHex(buf: Uint8Array): string {
  return Array.from(buf, (b) => b.toString(16).padStart(2, "0")).join("");
}

export function generateHost(rng: Rng, zone: string): string {
  const buf = rng(5);
  const prefix = PREFIXES[buf[0] % PREFIXES.length];
  const body = toHex(buf.slice(1, 5));
  return `${prefix}-${body}.${zone}`;
}

export function pickZone<T extends ZoneCandidate>(rng: Rng, candidates: T[], excludeName: string): T {
  const eligible = candidates.filter((z) => z.name !== excludeName);
  if (eligible.length === 0) {
    throw new NoEligibleZonesError();
  }
  const idx = rng(1)[0] % eligible.length;
  return eligible[idx];
}

export const HY2_PREFIXES = ["quic", "udp", "hy"] as const;

export function generateHy2Host(rng: Rng, zone: string): string {
  const buf = rng(5);
  const prefix = HY2_PREFIXES[buf[0] % HY2_PREFIXES.length];
  const body = toHex(buf.slice(1, 5));
  return `${prefix}-${body}.${zone}`;
}
