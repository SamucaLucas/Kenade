const CACHE_NAME = 'kenade-pwa-v1';
const STATIC_ASSETS = [
  '/styles.css',
  '/common.js',
  '/favicon.svg',
  '/logo.svg',
  '/icon-180.png',
  '/icon-192.png',
  '/icon-512.png',
  '/manifest.webmanifest',
];

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) => cache.addAll(STATIC_ASSETS)).catch(() => {})
  );
  self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys.map((key) => {
          if (key !== CACHE_NAME) {
            return caches.delete(key);
          }
        })
      )
    )
  );
  self.clients.claim();
});

self.addEventListener('fetch', (event) => {
  const req = event.request;
  if (req.method !== 'GET') return;

  const url = new URL(req.url);
  if (url.origin !== self.location.origin) return;

  // Rotas de API e WebSocket vão sempre direto para a rede
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/')) {
    return;
  }

  // Arquivos estáticos (.css, .js, .svg, .png, .webmanifest): Stale-While-Revalidate para abertura instantânea
  const isStaticAsset =
    url.pathname.endsWith('.css') ||
    url.pathname.endsWith('.js') ||
    url.pathname.endsWith('.svg') ||
    url.pathname.endsWith('.png') ||
    url.pathname.endsWith('.webmanifest');

  if (isStaticAsset) {
    event.respondWith(
      caches.match(req).then((cached) => {
        const networkFetch = fetch(req)
          .then((resp) => {
            if (resp && resp.status === 200) {
              const copy = resp.clone();
              caches.open(CACHE_NAME).then((cache) => cache.put(req, copy));
            }
            return resp;
          })
          .catch(() => cached);
        return cached || networkFetch;
      })
    );
    return;
  }

  // Navegação de páginas HTML: Network-First com fallback de cache
  event.respondWith(
    fetch(req)
      .then((resp) => {
        if (resp && resp.status === 200 && resp.type === 'basic') {
          const copy = resp.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(req, copy));
        }
        return resp;
      })
      .catch(() => caches.match(req))
  );
});
