export function buildPublicSubscriptionUrl(origin: string, token: string): string {
  return `${origin}/sub/${token}`
}

function base64Url(input: string): string {
  return btoa(input).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
}

export function buildShadowrocketDeepLink(subUrl: string): string {
  return `shadowrocket://add/sub/${base64Url(subUrl)}`
}

export function buildV2rayNgDeepLink(subUrl: string, remarks: string): string {
  return `v2rayng://install-sub?url=${encodeURIComponent(subUrl)}#${encodeURIComponent(remarks)}`
}
