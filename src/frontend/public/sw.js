/**
 * Lattix PWA service worker.
 *
 * Deliberately dependency-free and small enough to audit in one read.
 *
 * - Cache version is bumped with this file; activation removes older caches.
 * - Install precaches the manifest, icons and the data-free SPA shell.
 * - Never touches non-GET, cross-origin, /api/ or /sub/ requests.
 * - Navigations are network-first with the cached shell as offline fallback.
 * - Hashed /assets/ and icons are cache-first with a network fallback.
 */
const CACHE_VERSION = 'v1'

const CACHE_PREFIX = 'lattix-pwa-'
const SHELL_CACHE = `${CACHE_PREFIX}shell-${CACHE_VERSION}`
const STATIC_CACHE = `${CACHE_PREFIX}static-${CACHE_VERSION}`

// The shell contains no user data; backend SPA fallback serves it for every app route.
const SHELL_URL = '/index.html'
const PRECACHE_URLS = [
  '/manifest.webmanifest',
  '/icons/icon-192.png',
  '/icons/icon-512.png',
  '/icons/icon-maskable-192.png',
  '/icons/icon-maskable-512.png',
  '/icons/apple-touch-icon.png',
]

self.addEventListener('install', (event) => {
  event.waitUntil(
    Promise.all([
      caches.open(STATIC_CACHE).then((cache) => cache.addAll(PRECACHE_URLS)),
      caches.open(SHELL_CACHE).then((cache) => cache.add(SHELL_URL)),
    ]),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      const current = new Set([SHELL_CACHE, STATIC_CACHE])
      const keys = await caches.keys()
      await Promise.all(
        keys
          .filter((key) => key.startsWith(CACHE_PREFIX) && !current.has(key))
          .map((key) => caches.delete(key)),
      )
      await self.clients.claim()
    })(),
  )
})

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return

  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return
  if (isPrivatePath(url.pathname)) return

  if (request.mode === 'navigate') {
    event.respondWith(networkFirstShell(request))
    return
  }
  if (isStaticAsset(url.pathname)) {
    event.respondWith(cacheFirst(request))
  }
})

/** API and subscription routes stay strictly outside the cache. */
function isPrivatePath(pathname) {
  return (
    pathname === '/api' ||
    pathname.startsWith('/api/') ||
    pathname === '/sub' ||
    pathname.startsWith('/sub/')
  )
}

function isStaticAsset(pathname) {
  return (
    pathname.startsWith('/assets/') ||
    pathname.startsWith('/icons/') ||
    pathname === '/manifest.webmanifest'
  )
}

async function networkFirstShell(request) {
  try {
    const response = await fetch(request)
    // Keep the fallback shell fresh after panel upgrades without caching data.
    if (response.ok && response.type === 'basic') {
      const contentType = response.headers.get('Content-Type') || ''
      if (contentType.includes('text/html')) {
        const cache = await caches.open(SHELL_CACHE)
        await cache.put(SHELL_URL, response.clone())
      }
    }
    return response
  } catch {
    const cache = await caches.open(SHELL_CACHE)
    return (await cache.match(SHELL_URL)) || Response.error()
  }
}

async function cacheFirst(request) {
  const cached = await caches.match(request)
  if (cached) return cached

  const response = await fetch(request)
  if (response.ok && response.type === 'basic') {
    const cache = await caches.open(STATIC_CACHE)
    await cache.put(request, response.clone())
  }
  return response
}
