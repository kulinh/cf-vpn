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
