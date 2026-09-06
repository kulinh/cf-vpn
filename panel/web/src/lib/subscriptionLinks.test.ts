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

  it('builds shadowrocket deep link with a sub:// pseudo-URI and standard base64 (with padding)', () => {
    const subUrl = 'https://panel.example.com/sub/abc123'
    expect(buildShadowrocketDeepLink(subUrl)).toBe(
      `shadowrocket://add/sub://${btoa(subUrl)}?remark=${encodeURIComponent('RWL8899')}`,
    )
  })

  it('lets callers override the shadowrocket remark', () => {
    const subUrl = 'https://panel.example.com/sub/abc123'
    expect(buildShadowrocketDeepLink(subUrl, 'My Remark')).toBe(
      `shadowrocket://add/sub://${btoa(subUrl)}?remark=${encodeURIComponent('My Remark')}`,
    )
  })

  it('uses standard base64 (not URL-safe) so bytes that would map to "/" survive unescaped', () => {
    // Bytes chosen so their base64 encoding contains "/". The pre-fix
    // implementation ran URL-safe substitution (`/` -> `_`) on this, which
    // corrupts the payload since Shadowrocket decodes standard base64.
    const raw = [0xb9, 0x8c, 0x21, 0x1a, 0x6f, 0xe8, 0x9c, 0x59, 0xf5, 0x26, 0x95, 0x59]
    const subUrl = raw.map((b) => String.fromCharCode(b)).join('')
    const encoded = btoa(subUrl)
    expect(encoded).toContain('/')
    expect(buildShadowrocketDeepLink(subUrl)).toBe(
      `shadowrocket://add/sub://${encoded}?remark=${encodeURIComponent('RWL8899')}`,
    )
  })

  it('builds v2rayng deep link with encoded subscription url and a name query param (not a fragment)', () => {
    const subUrl = 'https://panel.example.com/sub/abc123'
    expect(buildV2rayNgDeepLink(subUrl, 'RWL8899')).toBe(
      `v2rayng://install-sub?url=${encodeURIComponent(subUrl)}&name=${encodeURIComponent('RWL8899')}`,
    )
  })
})
