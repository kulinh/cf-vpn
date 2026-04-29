import { describe, expect, it } from 'vitest'
import {
  buildPublicSubscriptionUrl,
  buildShadowrocketDeepLink,
  buildV2rayNgDeepLink,
} from './subscriptionLinks'

describe('subscriptionLinks', () => {
  it('builds public subscription URL from origin and token', () => {
    expect(buildPublicSubscriptionUrl('https://panel.example.com', 'abc123')).toBe(
      'https://panel.example.com/sub/abc123',
    )
  })

  it('builds shadowrocket deep link with URL-safe base64 subscription url', () => {
    const subUrl = 'https://panel.example.com/sub/abc123'
    const encoded = btoa(subUrl).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
    expect(buildShadowrocketDeepLink(subUrl)).toBe(`shadowrocket://add/sub/${encoded}`)
  })

  it('builds v2rayng deep link with encoded subscription url and remarks', () => {
    const subUrl = 'https://panel.example.com/sub/abc123'
    expect(buildV2rayNgDeepLink(subUrl, 'RWL8899')).toBe(
      `v2rayng://install-sub?url=${encodeURIComponent(subUrl)}#${encodeURIComponent('RWL8899')}`,
    )
  })
})
