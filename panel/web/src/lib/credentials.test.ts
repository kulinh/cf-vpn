import { beforeEach, describe, expect, it, vi } from 'vitest'
import { clearCredentials, encodeBasic, loadCredentials, saveCredentials } from './credentials'

// jsdom in this project exposes `localStorage` as a bare object, not a Storage
// instance, so the suite supplies its own. It also lets a test make storage
// throw, which is what private mode and "block site data" actually do.
function installStorage(overrides: Partial<Storage> = {}) {
  const data = new Map<string, string>()
  const store = {
    getItem: (k: string) => data.get(k) ?? null,
    setItem: (k: string, v: string) => void data.set(k, v),
    removeItem: (k: string) => void data.delete(k),
    clear: () => data.clear(),
    ...overrides,
  }
  vi.stubGlobal('localStorage', store)
  return store
}

describe('credentials', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
    installStorage()
  })

  it('round-trips through storage', () => {
    saveCredentials({ username: 'admin', password: 'p@ss word' })
    expect(loadCredentials()).toEqual({ username: 'admin', password: 'p@ss word' })
  })

  it('encodes a basic header', () => {
    expect(encodeBasic({ username: 'admin', password: 'x' })).toBe(`Basic ${btoa('admin:x')}`)
  })

  it('reports signed out when nothing is stored', () => {
    expect(loadCredentials()).toBeNull()
  })

  it('drops a corrupt entry instead of wedging the app', () => {
    const store = installStorage()
    store.setItem('panel-credentials', '{not json')
    expect(loadCredentials()).toBeNull()
    expect(store.getItem('panel-credentials')).toBeNull()
  })

  it('rejects an entry missing either field', () => {
    const store = installStorage()
    store.setItem('panel-credentials', JSON.stringify({ username: 'admin' }))
    expect(loadCredentials()).toBeNull()
    store.setItem('panel-credentials', JSON.stringify({ username: '', password: 'x' }))
    expect(loadCredentials()).toBeNull()
  })

  it('treats blocked storage as signed out rather than throwing', () => {
    // The app must still boot in private mode instead of dying on read.
    installStorage({
      getItem: () => {
        throw new Error('blocked')
      },
    })
    expect(() => loadCredentials()).not.toThrow()
    expect(loadCredentials()).toBeNull()
  })

  it('does not throw when saving into blocked storage', () => {
    installStorage({
      setItem: () => {
        throw new Error('blocked')
      },
    })
    expect(() => saveCredentials({ username: 'a', password: 'b' })).not.toThrow()
  })

  it('clears', () => {
    saveCredentials({ username: 'admin', password: 'x' })
    clearCredentials()
    expect(loadCredentials()).toBeNull()
  })
})
