function corsHeaders(env) {
  return {
    'access-control-allow-origin': env.ALLOWED_ORIGIN || '*',
    'access-control-allow-methods': 'GET,POST,OPTIONS',
    'access-control-allow-headers': 'authorization,content-type',
  };
}

function json(body, init = {}, env = {}) {
  return new Response(JSON.stringify(body), {
    ...init,
    headers: {
      'content-type': 'application/json',
      ...corsHeaders(env),
      ...(init.headers || {}),
    },
  });
}

function subscriptionKey(endpoint) {
  const bytes = new TextEncoder().encode(endpoint);
  return crypto.subtle.digest('SHA-256', bytes).then((hash) =>
    [...new Uint8Array(hash)].map((b) => b.toString(16).padStart(2, '0')).join('')
  );
}

function requireAdmin(request, env) {
  const expected = env.PUSH_ADMIN_TOKEN;
  if (!expected) return false;
  const actual = request.headers.get('authorization') || '';
  return actual === `Bearer ${expected}`;
}

function requireOrigin(request, env) {
  if (!env.ALLOWED_ORIGIN) return true;
  return request.headers.get('origin') === env.ALLOWED_ORIGIN;
}

async function readJSON(request) {
  try {
    return await request.json();
  } catch {
    return null;
  }
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (request.method === 'OPTIONS') {
      return new Response(null, { status: 204, headers: corsHeaders(env) });
    }

    if (url.pathname === '/health') {
      return json({ ok: true }, {}, env);
    }

    if (url.pathname === '/subscribe' && request.method === 'POST') {
      if (!requireOrigin(request, env)) {
        return json({ error: 'forbidden-origin' }, { status: 403 }, env);
      }
      const body = await readJSON(request);
      if (!body || typeof body.endpoint !== 'string' || !body.keys) {
        return json({ error: 'invalid-subscription' }, { status: 400 }, env);
      }
      const key = await subscriptionKey(body.endpoint);
      await env.PUSH_SUBSCRIPTIONS.put(key, JSON.stringify(body));
      return json({ ok: true }, {}, env);
    }

    if (url.pathname === '/unsubscribe' && request.method === 'POST') {
      if (!requireOrigin(request, env)) {
        return json({ error: 'forbidden-origin' }, { status: 403 }, env);
      }
      const body = await readJSON(request);
      if (!body || typeof body.endpoint !== 'string') {
        return json({ error: 'invalid-endpoint' }, { status: 400 }, env);
      }
      const key = await subscriptionKey(body.endpoint);
      await env.PUSH_SUBSCRIPTIONS.delete(key);
      return json({ ok: true }, {}, env);
    }

    if (url.pathname === '/subscriptions' && request.method === 'GET') {
      if (!requireAdmin(request, env)) {
        return json({ error: 'unauthorized' }, { status: 401 }, env);
      }

      const subscriptions = [];
      let cursor;
      do {
        const page = await env.PUSH_SUBSCRIPTIONS.list({ cursor });
        cursor = page.cursor;
        for (const key of page.keys) {
          const value = await env.PUSH_SUBSCRIPTIONS.get(key.name);
          if (!value) continue;
          try {
            subscriptions.push(JSON.parse(value));
          } catch {
            await env.PUSH_SUBSCRIPTIONS.delete(key.name);
          }
        }
      } while (cursor);

      return json({ subscriptions }, {}, env);
    }

    if (url.pathname === '/subscriptions' && request.method === 'DELETE') {
      if (!requireAdmin(request, env)) {
        return json({ error: 'unauthorized' }, { status: 401 }, env);
      }
      const endpoint = url.searchParams.get('endpoint');
      if (!endpoint) return json({ error: 'missing-endpoint' }, { status: 400 }, env);
      const key = await subscriptionKey(endpoint);
      await env.PUSH_SUBSCRIPTIONS.delete(key);
      return json({ ok: true }, {}, env);
    }

    return json({ error: 'not-found' }, { status: 404 }, env);
  },
};
