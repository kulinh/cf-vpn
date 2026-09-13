export function buildPublicSubscriptionUrl(origin: string, token: string): string {
  return `${origin}/sub/${token}`
}

export function buildShadowrocketDeepLink(subUrl: string, remark = 'RWL'): string {
  return `shadowrocket://add/sub://${btoa(subUrl)}?remark=${encodeURIComponent(remark)}`
}

// Hiddify takes everything after `hiddify://import/` as the subscription URL,
// so the plain form needs no encoding. There is deliberately no #name: Hiddify
// reads the fragment still percent-encoded and lets it override the
// `profile-title` header the subscription already sends. A URL with its own
// query string would have that query attached to the link instead, so it goes
// through the `?url=` form. Newer Hiddify builds only auto-import when the
// link host is `import`, which both forms satisfy.
export function buildHiddifyDeepLink(subUrl: string): string {
  return subUrl.includes('?')
    ? `hiddify://import/?url=${encodeURIComponent(subUrl)}`
    : `hiddify://import/${subUrl}`
}

// One profile per trip: the blocked-site list is chosen by the link.
export type RuleSet = 'cn' | 'uae'
export const RULE_SETS: ReadonlyArray<{ key: RuleSet; label: string; hint: string }> = [
  { key: 'cn', label: 'CN', hint: 'China: sites behind the GFW go through the proxy, the rest direct' },
  { key: 'uae', label: 'UAE', hint: 'UAE: OTT calls (WhatsApp, FaceTime…) and TDRA-blocked sites go through the proxy' },
]
export function profileName(rules: RuleSet): string {
  return `RWL-${rules.toUpperCase()}`
}

// The Shadowrocket .conf (policy groups + rules) is a separate remote config
// next to the node subscription; Shadowrocket has no deep link for it, so the
// panel hands out the URL to paste under Config > Add remote.
// The list rides in the file name (Shadowrocket names the config after it):
// /sub/<token>/RWL-CN.conf.
export function buildShadowrocketConfUrl(subUrl: string, rules: RuleSet): string {
  return `${subUrl}/${profileName(rules)}.conf`
}

// The official sing-box apps (SFI on iOS/macOS, SFA on Android) import a
// remote profile from this link (libbox GenerateRemoteProfileImportLink). It
// points at ?format=singbox&rules=…, the complete config that carries the
// blocked-site rules, so only listed sites ride the proxy. sing-box ignores
// the profile-title header, hence the #name.
export function buildSingboxDeepLink(subUrl: string, rules: RuleSet = 'cn'): string {
  return `sing-box://import-remote-profile?url=${encodeURIComponent(`${subUrl}?format=singbox&rules=${rules}`)}#${encodeURIComponent(profileName(rules))}`
}
