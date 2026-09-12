// The blocked-site list for the Shadowrocket .conf comes from the public
// module kulinh/shadowrocket-vietnamese:sr_proxy_list_CN.module. The Worker
// pulls it at the Cloudflare edge (GitHub is reachable from there, not from
// China) and inlines the rules into the config, so the phone never has to
// reach GitHub and the user installs no module by hand. The module stays the
// single source of truth: edit it there, the next subscription refresh picks
// it up.

export const DEFAULT_CN_RULES_URL = "https://raw.githubusercontent.com/kulinh/shadowrocket-vietnamese/master/sr_proxy_list_CN.module";

// Rule types Shadowrocket accepts in a .conf [Rule] section that a module
// can also carry. Anything else (and module-only sections) is dropped.
const RULE_TYPES = new Set([
  "DOMAIN-SUFFIX",
  "DOMAIN",
  "DOMAIN-KEYWORD",
  "IP-CIDR",
  "IP-CIDR6",
  "IP-ASN",
  "GEOIP",
  "USER-AGENT",
  "URL-REGEX"
]);

// Turn module text into [Rule] lines that all target the config's PROXY
// group. The module's own policy field is ignored on purpose: whatever name
// its author used, inside our config the group is PROXY. Flags after the
// policy (no-resolve) are kept; duplicates are dropped, order is preserved.
export function parseModuleRules(text: string): string[] {
  const out: string[] = [];
  const seen = new Set<string>();
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (line === "" || line.startsWith("#") || line.startsWith("[") || line.startsWith(";")) continue;
    const parts = line.split(",").map((p) => p.trim());
    const type = parts[0].toUpperCase();
    const value = parts[1];
    if (!RULE_TYPES.has(type) || !value) continue;
    const flags = parts.slice(3).filter((f) => f.toLowerCase() === "no-resolve");
    const rule = [type, value, "PROXY", ...flags].join(",");
    const key = `${type},${value}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push(rule);
  }
  return out;
}

export interface CNRules {
  rules: string[];
  // One comment line describing where the rules came from, for the .conf.
  comment: string;
}

export type Fetcher = (input: string, init?: RequestInit) => Promise<Response>;

// Fetch + parse, or null when anything goes wrong (network, non-2xx, empty).
// The edge cache keeps GitHub out of the hot path: one origin hit per hour
// per colo. A failure must not break the subscription, the caller falls
// back to a RULE-SET line instead.
export async function fetchCNRules(url: string = DEFAULT_CN_RULES_URL, fetcher: Fetcher = fetch, timeoutMs = 5000): Promise<CNRules | null> {
  try {
    const res = await fetcher(url, {
      signal: AbortSignal.timeout(timeoutMs),
      headers: { "user-agent": "cfvpn-panel-api (+shadowrocket conf)" },
      cf: { cacheTtl: 3600, cacheEverything: true }
    } as RequestInit);
    if (!res.ok) return null;
    const rules = parseModuleRules(await res.text());
    if (rules.length === 0) return null;
    const etag = (res.headers.get("etag") ?? "").replace(/^W\//, "").replace(/"/g, "");
    const day = new Date().toISOString().slice(0, 10);
    return {
      rules,
      comment: `# sr_proxy_list_CN from ${url} (${rules.length} rules${etag ? `, etag ${etag.slice(0, 12)}` : ""}, fetched ${day})`
    };
  } catch {
    return null;
  }
}
