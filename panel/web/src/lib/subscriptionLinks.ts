export function buildPublicSubscriptionUrl(origin: string, token: string): string {
  return `${origin}/sub/${token}`
}

export function buildShadowrocketDeepLink(subUrl: string, remark = 'RWL8899'): string {
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

// The official sing-box apps (SFI on iOS/macOS, SFA on Android) import a
// remote profile from this link (libbox GenerateRemoteProfileImportLink). It
// points at ?format=singbox, the complete config that carries the
// blocked-site rules, so only listed sites ride the proxy. No ?rules= is
// added: the config then follows the operator's travel mode (Telegram /mode).
// sing-box ignores the profile-title header, hence the #name.
export function buildSingboxDeepLink(subUrl: string, name = 'RWL8899'): string {
  return `sing-box://import-remote-profile?url=${encodeURIComponent(`${subUrl}?format=singbox`)}#${encodeURIComponent(name)}`
}
