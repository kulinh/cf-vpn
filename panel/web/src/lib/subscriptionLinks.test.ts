import { describe, expect, it } from 'vitest'
import {
  buildPublicSubscriptionUrl,
  buildShadowrocketDeepLink,
  buildHiddifyDeepLink,
  buildSingboxDeepLink,
  buildShadowrocketConfUrl,
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
      `shadowrocket://add/sub://${btoa(subUrl)}?remark=${encodeURIComponent('RWL')}`,
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
      `shadowrocket://add/sub://${encoded}?remark=${encodeURIComponent('RWL')}`,
    )
  })

  it('builds hiddify import link with the raw subscription url as the path and no #name', () => {
    const subUrl = 'https://panel.example.com/sub/abc123'
    expect(buildHiddifyDeepLink(subUrl)).toBe('hiddify://import/https://panel.example.com/sub/abc123')
  })

  it('falls back to the ?url= form when the subscription url carries a query string', () => {
    const subUrl = 'https://panel.example.com/sub/abc123?rules=uae'
    expect(buildHiddifyDeepLink(subUrl)).toBe(`hiddify://import/?url=${encodeURIComponent(subUrl)}`)
  })

  it('builds a sing-box remote-profile link per rule set, named after it', () => {
    const subUrl = 'https://panel.example.com/sub/abc123'
    expect(buildSingboxDeepLink(subUrl)).toBe(
      `sing-box://import-remote-profile?url=${encodeURIComponent(`${subUrl}?format=singbox&rules=cn`)}#RWL-CN`,
    )
    expect(buildSingboxDeepLink(subUrl, 'uae')).toBe(
      `sing-box://import-remote-profile?url=${encodeURIComponent(`${subUrl}?format=singbox&rules=uae`)}#RWL-UAE`,
    )
  })

  it('builds the Shadowrocket remote-config URL per rule set', () => {
    expect(buildShadowrocketConfUrl('https://panel.example.com/sub/abc123', 'uae')).toBe(
      'https://panel.example.com/sub/abc123?format=shadowrocket&rules=uae',
    )
  })
})
