import { WorkerEntrypoint } from 'cloudflare:workers';
import {
  clientResponse, errorResponse, purgeRequest, PURGE_PATH, serveAsset,
} from './assets.ts';

interface AssetsEnv extends Omit<Cloudflare.Env, 'PURGE_ENABLED'> {
  PURGE_TOKEN?: string;
  PURGE_ENABLED: string;
}

function isSiteRoute(pathname: string): boolean {
  if (pathname === '/' || pathname === '/site') return true;
  if (!pathname.startsWith('/site/')) return false;
  return !pathname.startsWith('/site/_assets/');
}

// Workers Cache checks its lower and upper tiers before this entrypoint runs.
// A warm public request therefore executes no Worker code and makes no loopback call.
export default class AssetsWorker extends WorkerEntrypoint<AssetsEnv> {
  async fetch(request: Request): Promise<Response> {
    try {
      const url = new URL(request.url);
      if (url.pathname === PURGE_PATH) {
        return purgeRequest(request, this.env.PURGE_TOKEN, this.env.PURGE_ENABLED,
          (tags) => {
            if (!this.ctx.cache) throw new Error('Workers Cache API is unavailable');
            return this.ctx.cache.purge({ tags });
          });
      }
      if (request.method === 'OPTIONS') {
        return new Response(null, { status: 204, headers: {
          'Access-Control-Allow-Origin': '*', 'Access-Control-Allow-Methods': 'GET, HEAD, OPTIONS',
          'Access-Control-Allow-Headers': 'If-None-Match, If-Modified-Since, Range, If-Range',
          'Access-Control-Max-Age': '86400', 'Cache-Control': 'no-store',
        } });
      }
      if (request.method !== 'GET' && request.method !== 'HEAD') return errorResponse(405, 'GET or HEAD required');
      if (isSiteRoute(url.pathname)) {
        const indexUrl = new URL('/index.html', url);
        return this.env.WIKI_ASSETS.fetch(new Request(indexUrl, { method: request.method, headers: request.headers }));
      }
      if (url.pathname.startsWith('/site/_assets/')) return errorResponse(404, 'Asset not found');
      return clientResponse(request, await serveAsset(request, this.env));
    } catch { return errorResponse(502, 'Asset service unavailable'); }
  }
}
