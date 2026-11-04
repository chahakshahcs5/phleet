// Minimal PWA service worker: network first, static asset fallback only.
// API, agent, webhook and download routes are never cached.
self.addEventListener('install', () => self.skipWaiting())
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()))

self.addEventListener('fetch', (event) => {
  const request = event.request
  if (request.method !== 'GET') return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return
  if (
    url.pathname.startsWith('/api') ||
    url.pathname.startsWith('/agent') ||
    url.pathname.startsWith('/hooks') ||
    url.pathname.startsWith('/download')
  ) {
    return
  }
  event.respondWith(
    fetch(request).catch(async () => {
      const cached = await caches.match(request)
      return cached || new Response('offline', { status: 503, headers: { 'Content-Type': 'text/plain' } })
    }),
  )
})
