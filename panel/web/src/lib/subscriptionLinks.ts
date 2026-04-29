export function buildPublicSubscriptionUrl(origin: string, token: string): string {
  return `${origin}/sub/${token}`
}

export function buildShadowrocketDeepLink(subUrl: string): string {
  return `shadowrocket://add/sub/${btoa(subUrl)}`
}

export function buildV2rayNgDeepLink(subUrl: string, remarks: string): string {
  return `v2rayng://install-sub?url=${encodeURIComponent(subUrl)}#${encodeURIComponent(remarks)}`
}
