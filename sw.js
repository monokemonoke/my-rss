const CACHE = 'kijiyomu-v2';

self.addEventListener('install', () => self.skipWaiting());

self.addEventListener('activate', e => {
  e.waitUntil(
    caches.keys()
      .then(keys => Promise.all(keys.filter(k => k !== CACHE).map(k => caches.delete(k))))
      .then(() => self.clients.claim())
  );
});

function store(req, res) {
  if (res.ok) {
    const copy = res.clone();
    caches.open(CACHE).then(c => c.put(req, copy));
  }
  return res;
}

self.addEventListener('fetch', e => {
  const req = e.request;
  if (req.method !== 'GET') return;

  // HTML と記事データは 2 時間ごとに更新されるので毎回取りに行き、
  // オフライン時だけキャッシュを使う
  if (req.mode === 'navigate' || new URL(req.url).pathname.endsWith('/data.json')) {
    e.respondWith(fetch(req).then(res => store(req, res)).catch(() => caches.match(req)));
    return;
  }

  // ロゴ・アイコンは内容が変わらないのでキャッシュ優先
  if (req.destination === 'image' && new URL(req.url).origin === self.location.origin) {
    e.respondWith(caches.match(req).then(hit => hit || fetch(req).then(res => store(req, res))));
  }
});
