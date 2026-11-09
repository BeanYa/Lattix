import { describe, expect, it } from 'vitest'

import {
  isIosDevice,
  pwaInstallGuide,
  resolvePwaInstallMode,
  type PwaInstallEnvironment,
} from './pwa'

const environment = (overrides: Partial<PwaInstallEnvironment> = {}): PwaInstallEnvironment => ({
  standalone: false,
  promptAvailable: false,
  ios: false,
  ...overrides,
})

const iphoneSafari =
  'Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1'
const ipadDesktopMode =
  'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15'
const androidChrome =
  'Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36'
const macSafari = ipadDesktopMode
const windowsChrome =
  'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36'

describe('isIosDevice', () => {
  it('recognizes iPhone and iPad user agents', () => {
    expect(isIosDevice(iphoneSafari, 'iPhone', 5)).toBe(true)
    expect(isIosDevice('Mozilla/5.0 (iPad; CPU OS 16_0 like Mac OS X)', 'iPad', 5)).toBe(true)
  })

  it('recognizes iPadOS desktop-mode user agents via MacIntel + touch', () => {
    expect(isIosDevice(ipadDesktopMode, 'MacIntel', 5)).toBe(true)
    expect(isIosDevice(ipadDesktopMode, 'MacIntel', 0)).toBe(false)
  })

  it('rejects desktop and Android browsers', () => {
    expect(isIosDevice(macSafari, 'MacIntel', 0)).toBe(false)
    expect(isIosDevice(androidChrome, 'Linux armv8l', 5)).toBe(false)
    expect(isIosDevice(windowsChrome, 'Win32', 0)).toBe(false)
  })
})

describe('resolvePwaInstallMode', () => {
  it('prefers the installed state over any pending prompt', () => {
    expect(resolvePwaInstallMode(environment({ standalone: true, promptAvailable: true }))).toBe(
      'installed',
    )
  })

  it('offers the install prompt when the browser exposes it', () => {
    expect(resolvePwaInstallMode(environment({ promptAvailable: true }))).toBe('prompt')
  })

  it('falls back to Share → Add to Home Screen on iOS', () => {
    expect(resolvePwaInstallMode(environment({ ios: true }))).toBe('ios-instructions')
  })

  it('falls back to the browser menu everywhere else', () => {
    expect(resolvePwaInstallMode(environment())).toBe('browser-instructions')
  })
})

describe('pwaInstallGuide', () => {
  it('reports an already installed app', () => {
    const guide = pwaInstallGuide(environment({ standalone: true }))
    expect(guide.mode).toBe('installed')
    expect(guide.message).toBe('已安装，可像应用一样打开。')
    expect(guide.actionLabel).toBeNull()
  })

  it('only exposes the install action label for the prompt mode', () => {
    expect(pwaInstallGuide(environment({ promptAvailable: true })).actionLabel).toBe('安装到桌面')
    expect(pwaInstallGuide(environment({ ios: true })).actionLabel).toBeNull()
    expect(pwaInstallGuide(environment()).actionLabel).toBeNull()
  })

  it('gives iOS and generic browser instructions without an install event', () => {
    expect(pwaInstallGuide(environment({ ios: true })).message).toContain('添加到主屏幕')
    expect(pwaInstallGuide(environment()).message).toContain('浏览器菜单')
  })
})
