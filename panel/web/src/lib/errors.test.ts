import { describe, expect, it } from 'vitest'
import { describeLoadError } from './errors'

describe('describeLoadError', () => {
  it('shows a session-expired banner for the session-expired sentinel error', () => {
    expect(describeLoadError(new Error('session-expired'))).toBe(
      'Session expired — reload the page',
    )
  })

  it('shows a generic failed-to-load banner with the error message otherwise', () => {
    expect(describeLoadError(new Error('nodes failed'))).toBe(
      'Failed to load — nodes failed. Reload.',
    )
  })

  it('falls back to "unknown error" for non-Error rejections', () => {
    expect(describeLoadError('boom')).toBe('Failed to load — unknown error. Reload.')
  })
})
