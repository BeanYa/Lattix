import { useCallback, useEffect, useState, useSyncExternalStore } from 'react'

/**
 * Chromium 的非标准安装提示事件；Safari/Firefox 不会触发。
 * 事件只会触发一次，prompt() 后必须丢弃。
 */
export interface BeforeInstallPromptEvent extends Event {
  prompt: () => Promise<void>
  userChoice?: Promise<{ outcome: 'accepted' | 'dismissed'; platform: string }>
}

export interface PwaInstallEnvironment {
  /** 已以独立窗口（standalone / iOS 主屏幕）运行。 */
  standalone: boolean
  /** 已捕获可用的 beforeinstallprompt 事件。 */
  promptAvailable: boolean
  /** iOS / iPadOS 设备：没有 beforeinstallprompt，只能走分享菜单。 */
  ios: boolean
}

export type PwaInstallMode = 'installed' | 'prompt' | 'ios-instructions' | 'browser-instructions'

export interface PwaInstallGuide {
  mode: PwaInstallMode
  message: string
  /** 仅 prompt 模式有可执行按钮。 */
  actionLabel: string | null
}

/**
 * 识别 iOS / iPadOS。iPadOS 13+ 默认发送桌面版 macOS UA，
 * 只能靠 MacIntel + 多点触控区分。
 */
export function isIosDevice(userAgent: string, platform = '', maxTouchPoints = 0): boolean {
  return /iPad|iPhone|iPod/i.test(userAgent) || (platform === 'MacIntel' && maxTouchPoints > 1)
}

/** 纯函数：把运行环境收敛为四种互斥的安装引导。 */
export function resolvePwaInstallMode(environment: PwaInstallEnvironment): PwaInstallMode {
  if (environment.standalone) return 'installed'
  if (environment.promptAvailable) return 'prompt'
  if (environment.ios) return 'ios-instructions'
  return 'browser-instructions'
}

export function pwaInstallGuide(environment: PwaInstallEnvironment): PwaInstallGuide {
  const mode = resolvePwaInstallMode(environment)
  switch (mode) {
    case 'installed':
      return { mode, message: '已安装，可像应用一样打开。', actionLabel: null }
    case 'prompt':
      return {
        mode,
        message: '安装后可从主屏幕直接打开，像应用一样独立运行。',
        actionLabel: '安装到桌面',
      }
    case 'ios-instructions':
      return { mode, message: 'Safari 分享 → 添加到主屏幕。', actionLabel: null }
    case 'browser-instructions':
      return {
        mode,
        message: '打开浏览器菜单（⋮ 或 …），选择「安装应用」或「添加到主屏幕」。',
        actionLabel: null,
      }
  }
}

function detectStandalone(): boolean {
  if (typeof window === 'undefined') return false
  const displayModeStandalone =
    typeof window.matchMedia === 'function' &&
    window.matchMedia('(display-mode: standalone)').matches
  const iosStandalone =
    (window.navigator as Navigator & { standalone?: boolean }).standalone === true
  return displayModeStandalone || iosStandalone
}

function detectIosBrowser(): boolean {
  if (typeof navigator === 'undefined') return false
  return isIosDevice(navigator.userAgent, navigator.platform, navigator.maxTouchPoints)
}

// beforeinstallprompt 全局只触发一次，必须早于设置页挂载时捕获；
// 这里用一个最小的外部 store 让任意页面都能拿到同一次提示。
let installPrompt: BeforeInstallPromptEvent | null = null
let promptCaptureStarted = false
const promptListeners = new Set<() => void>()

function notifyPromptListeners() {
  for (const listener of promptListeners) listener()
}

/** 在应用启动时调用一次；重复调用是安全的。 */
export function initPwaInstallPromptCapture(): void {
  if (promptCaptureStarted || typeof window === 'undefined') return
  promptCaptureStarted = true
  window.addEventListener('beforeinstallprompt', (event) => {
    event.preventDefault()
    installPrompt = event as BeforeInstallPromptEvent
    notifyPromptListeners()
  })
  window.addEventListener('appinstalled', () => {
    installPrompt = null
    notifyPromptListeners()
  })
}

function getInstallPrompt(): BeforeInstallPromptEvent | null {
  return installPrompt
}

function subscribeInstallPrompt(listener: () => void): () => void {
  promptListeners.add(listener)
  return () => {
    promptListeners.delete(listener)
  }
}

/**
 * 捕获安装提示并给出当前应展示的引导。
 * beforeinstallprompt 只触发一次，安装/关闭后立即从状态中移除。
 */
export function usePwaInstall(): {
  guide: PwaInstallGuide
  canPrompt: boolean
  promptInstall: () => Promise<void>
} {
  const [standalone, setStandalone] = useState(detectStandalone)
  const [ios] = useState(detectIosBrowser)
  const promptEvent = useSyncExternalStore(subscribeInstallPrompt, getInstallPrompt, () => null)

  useEffect(() => {
    const media =
      typeof window.matchMedia === 'function'
        ? window.matchMedia('(display-mode: standalone)')
        : null

    const handleInstalled = () => setStandalone(true)
    const handleDisplayModeChange = () => setStandalone(detectStandalone())

    window.addEventListener('appinstalled', handleInstalled)
    media?.addEventListener('change', handleDisplayModeChange)

    return () => {
      window.removeEventListener('appinstalled', handleInstalled)
      media?.removeEventListener('change', handleDisplayModeChange)
    }
  }, [])

  const promptInstall = useCallback(async () => {
    const event = promptEvent
    if (!event) return
    installPrompt = null
    notifyPromptListeners()
    try {
      await event.prompt()
      const choice = await event.userChoice?.catch(() => null)
      if (choice?.outcome === 'accepted') setStandalone(true)
    } catch {
      // 浏览器可能在重复调用或提示已过期时拒绝；保留菜单指引即可。
    }
  }, [promptEvent])

  return {
    guide: pwaInstallGuide({ standalone, promptAvailable: promptEvent !== null, ios }),
    canPrompt: promptEvent !== null,
    promptInstall,
  }
}

/**
 * 只在生产构建注册 Service Worker；开发服务器不缓存资源。
 * 注册失败不影响面板使用，仅记录警告。
 */
export function registerLattixServiceWorker(): void {
  if (!('serviceWorker' in navigator)) return
  navigator.serviceWorker.register('/sw.js', { scope: '/' }).catch((error: unknown) => {
    console.warn('[pwa] service worker registration failed', error)
  })
}
