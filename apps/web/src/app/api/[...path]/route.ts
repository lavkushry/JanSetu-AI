import { NextRequest } from 'next/server';

export const dynamic = 'force-dynamic';
async function forward(request: NextRequest, { params }: { params: Promise<{ path: string[] }> }) {
  const { path } = await params;
  if (path.some((p) => p === '.' || p === '..' || p.includes('/') || p.includes('\\')))
    return Response.json(
      { code: 'INVALID_REQUEST', title: 'Invalid request path' },
      { status: 400 },
    );
  if (!['GET', 'HEAD'].includes(request.method)) {
    const origin = request.headers.get('origin');
    const allowed = process.env.JANSETU_WEB_ORIGIN || 'http://localhost:3100';
    if ((origin && origin !== allowed) || request.headers.get('x-jansetu-csrf') !== '1')
      return Response.json(
        { code: 'ORIGIN_DENIED', title: 'Invalid request origin' },
        { status: 403 },
      );
  }
  const target = new URL(
    '/v1/' + path.map(encodeURIComponent).join('/'),
    process.env.JANSETU_API_URL || 'http://127.0.0.1:8081',
  );
  target.search = request.nextUrl.search;
  const headers = new Headers();
  for (const key of ['cookie', 'content-type', 'idempotency-key', 'if-match', 'x-jansetu-csrf']) {
    const value = request.headers.get(key);
    if (value) headers.set(key, value);
  }
  if (!['GET', 'HEAD'].includes(request.method))
    headers.set('origin', process.env.JANSETU_WEB_ORIGIN || 'http://localhost:3100');
  try {
    let body: ArrayBuffer | undefined;
    if (!['GET', 'HEAD'].includes(request.method)) {
      if (Number(request.headers.get('content-length') || 0) > 65536)
        return Response.json(
          { code: 'BODY_TOO_LARGE', title: 'Request is too large' },
          { status: 413 },
        );
      // Read at most 64 KiB even when transfer encoding omits Content-Length.
      const reader = request.body?.getReader();
      const chunks: Uint8Array[] = [];
      let size = 0;
      if (reader) {
        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          size += value.length;
          if (size > 65536) {
            await reader.cancel();
            return Response.json(
              { code: 'BODY_TOO_LARGE', title: 'Request is too large' },
              { status: 413 },
            );
          }
          chunks.push(value);
        }
      }
      const bytes = new Uint8Array(size);
      let offset = 0;
      for (const chunk of chunks) {
        bytes.set(chunk, offset);
        offset += chunk.length;
      }
      body = bytes.buffer;
    }
    const upstream = await fetch(target, {
      method: request.method,
      headers,
      body,
      cache: 'no-store',
      redirect: 'manual',
      signal: AbortSignal.timeout(15000),
    });
    const responseHeaders = new Headers({
      'cache-control': 'private, no-store',
      'content-type': 'application/json',
      'x-content-type-options': 'nosniff',
      'referrer-policy': 'no-referrer',
    });
    for (const cookie of upstream.headers.getSetCookie())
      responseHeaders.append('set-cookie', cookie);
    for (const key of ['etag', 'x-request-id', 'location']) {
      const value = upstream.headers.get(key);
      if (value) responseHeaders.set(key, value);
    }
    return new Response(upstream.body, { status: upstream.status, headers: responseHeaders });
  } catch {
    return Response.json(
      {
        status: 503,
        code: 'DEPENDENCY_UNAVAILABLE',
        title: 'JanSetu is temporarily unavailable. Your draft is preserved.',
        retryable: true,
      },
      { status: 503 },
    );
  }
}
export { forward as GET, forward as POST, forward as PUT, forward as PATCH, forward as DELETE };
