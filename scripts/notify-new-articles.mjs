import { readFile } from 'node:fs/promises';
import webpush from 'web-push';

const {
  PUSH_WORKER_URL,
  PUSH_ADMIN_TOKEN,
  VAPID_PUBLIC_KEY,
  VAPID_PRIVATE_KEY,
  VAPID_SUBJECT = 'mailto:notifications@kijiyomu.local',
  PREVIOUS_DATA = 'previous-data.json',
  CURRENT_DATA = 'data.json',
  SITE_URL = '',
} = process.env;

function required(name, value) {
  if (!value) throw new Error(`${name} is required`);
  return value;
}

async function readData(path) {
  try {
    return JSON.parse(await readFile(path, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
}

function articleURL(article) {
  return typeof article?.url === 'string' ? article.url : '';
}

function findNewArticles(previousData, currentData) {
  const previousURLs = new Set((previousData?.articles || []).map(articleURL).filter(Boolean));
  return (currentData?.articles || []).filter((article) => {
    const url = articleURL(article);
    return url && !previousURLs.has(url);
  });
}

async function workerFetch(path, options = {}) {
  const base = required('PUSH_WORKER_URL', PUSH_WORKER_URL).replace(/\/+$/, '');
  const res = await fetch(`${base}${path}`, {
    ...options,
    headers: {
      authorization: `Bearer ${required('PUSH_ADMIN_TOKEN', PUSH_ADMIN_TOKEN)}`,
      ...(options.headers || {}),
    },
  });
  if (!res.ok) {
    throw new Error(`Worker request failed: ${res.status} ${await res.text()}`);
  }
  return res;
}

async function subscriptions() {
  const res = await workerFetch('/subscriptions');
  const body = await res.json();
  return Array.isArray(body.subscriptions) ? body.subscriptions : [];
}

async function deleteSubscription(endpoint) {
  await workerFetch(`/subscriptions?endpoint=${encodeURIComponent(endpoint)}`, { method: 'DELETE' });
}

function notificationPayload(newArticles) {
  const first = newArticles[0];
  const title = newArticles.length === 1
    ? 'KijiYomuに新着記事'
    : `KijiYomuに新着記事 ${newArticles.length}件`;
  const body = newArticles.length === 1
    ? (first.title || '新しい記事があります')
    : `${first.title || '新しい記事'} ほか`;
  return JSON.stringify({
    title,
    body,
    url: SITE_URL || './',
  });
}

const previousData = await readData(PREVIOUS_DATA);
const currentData = await readData(CURRENT_DATA);
if (!currentData) throw new Error(`${CURRENT_DATA} is missing`);

const newArticles = findNewArticles(previousData, currentData);
if (!previousData) {
  console.log('No previous data found; skipping first notification run.');
  process.exit(0);
}
if (newArticles.length === 0) {
  console.log('No new articles; skipping notification.');
  process.exit(0);
}

webpush.setVapidDetails(
  VAPID_SUBJECT,
  required('VAPID_PUBLIC_KEY', VAPID_PUBLIC_KEY),
  required('VAPID_PRIVATE_KEY', VAPID_PRIVATE_KEY),
);

const targets = await subscriptions();
if (targets.length === 0) {
  console.log(`Found ${newArticles.length} new articles, but no push subscriptions are registered.`);
  process.exit(0);
}

const payload = notificationPayload(newArticles);
let sent = 0;
let pruned = 0;

await Promise.all(targets.map(async (subscription) => {
  try {
    await webpush.sendNotification(subscription, payload);
    sent += 1;
  } catch (error) {
    if ((error.statusCode === 404 || error.statusCode === 410) && subscription.endpoint) {
      await deleteSubscription(subscription.endpoint);
      pruned += 1;
      return;
    }
    console.warn(`Push failed for one subscription: ${error.statusCode || error.message}`);
  }
}));

console.log(`New articles: ${newArticles.length}; notifications sent: ${sent}; stale subscriptions pruned: ${pruned}.`);
