import { createReadStream, readFileSync, readdirSync, statSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import { extname, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { defineConfig, loadEnv } from 'vite';
import { createAssetArchive, entityTranslationFiles } from './src/archive.js';
import { entityAssetFiles, makeCatalogue } from './src/catalog.js';

const assetsRoot = fileURLToPath(new URL('../assets/', import.meta.url)).replace(/\/$/, '');
const types = {
  '.gif': 'image/gif',
  '.jpg': 'image/jpeg',
  '.json': 'application/json',
  '.ogg': 'audio/ogg',
  '.png': 'image/png',
  '.svg': 'image/svg+xml',
  '.ttf': 'font/ttf',
  '.webp': 'image/webp',
  '.woff2': 'font/woff2',
};

function audioBytes(range, size) {
  const match = /^bytes=(\d*)-(\d*)$/.exec(range || '');
  if (!match || (!match[1] && !match[2])) return null;
  const start = match[1] ? Number(match[1]) : Math.max(0, size - Number(match[2]));
  const end = match[1] && match[2] ? Math.min(size - 1, Number(match[2])) : size - 1;
  return { start, end };
}

function localAssets(staticDataPath = resolve(assetsRoot, 'static_data.json'), artworkOverlay) {
  const mediaFile = (path) => {
    if (artworkOverlay) {
      const extra = resolve(artworkOverlay, path);
      if (extra.startsWith(resolve(artworkOverlay) + sep) && statSync(extra, { throwIfNoEntry: false })?.isFile()) return extra;
    }
    return resolve(assetsRoot, path);
  };
  const manifest = JSON.parse(readFileSync(mediaFile('manifest.json'), 'utf8'));
  const data = JSON.parse(readFileSync(staticDataPath, 'utf8'));
  const abilities = JSON.parse(readFileSync(resolve(assetsRoot, 'static_data/abilities.json'), 'utf8'));
  const sfx = JSON.parse(readFileSync(resolve(assetsRoot, 'static_data/sfx.json'), 'utf8'));
  const translations = JSON.parse(readFileSync(resolve(assetsRoot, 'translations/EN.json'), 'utf8'));
  const audio = [...new Map(data.sceneries.flatMap(({ music, name }) => {
    if (!/^sceneries\/[^/]+\/music\.ogg$/.test(music || '')) return [];
    return statSync(resolve(assetsRoot, music), { throwIfNoEntry: false })?.isFile()
      ? [[music, { path: music, display_name: name + ' soundtrack', group: 'audio' }]] : [];
  })).values()];
  const effectRoot = resolve(assetsRoot, 'sfx');
  if (statSync(effectRoot, { throwIfNoEntry: false })?.isDirectory()) {
    for (const entry of readdirSync(effectRoot, { withFileTypes: true })) {
      if (!entry.isFile() || !entry.name.toLowerCase().endsWith('.ogg')) continue;
      audio.push({
        path: 'sfx/' + entry.name,
        display_name: entry.name.slice(0, -4).replace(/([a-z])([A-Z])/g, '$1 $2').replace(/[_-]+/g, ' '),
        group: 'sfx',
      });
    }
  }
  const { entitiesByKey } = makeCatalogue(data, manifest, abilities, translations, audio, sfx);
  const virtualAudio = '\0virtual:wiki-audio';
  const localePaths = new Map(readdirSync(resolve(assetsRoot, 'translations'))
    .filter((name) => /^[A-Z]{2,3}\.json$/.test(name)).map((name) => [name.slice(0, -5), 'translations/' + name]));
  const paths = new Set([
    'manifest.json', 'static_data.json', 'static_data/abilities.json', 'static_data/sfx.json', 'translations/EN.json',
    'fonts/clashking.woff2', 'fonts/SCmagic.ttf', 'logos/crown-red/ClashKing-crown.svg',
    'clashking/headers/assets-goblin-v2.webp',
    ...Object.values(manifest.assets).flatMap((group) => group.map(({ path }) => path)),
    ...localePaths.values(),
    ...audio.map(({ path }) => path),
  ]);

  function mount(server) {
    server.middlewares.use('/media', async (request, response) => {
      if (!['GET', 'HEAD'].includes(request.method)) {
        response.writeHead(405, { Allow: 'GET, HEAD' }).end();
        return;
      }

      let path;
      try {
        path = decodeURIComponent(request.url.split('?')[0]).slice(1);
      } catch {
        response.writeHead(400).end();
        return;
      }

      const bundle = /^bundles\/([^/]+)\.zip$/.exec(path);
      if (bundle) {
        // Use the same item catalogue as the gallery. The request selects an
        // item, so archive reads never use paths supplied by the caller.
        const entity = entitiesByKey.get(bundle[1]);
        if (!entity) {
          response.writeHead(404).end();
          return;
        }
        const language = new URLSearchParams(request.url.split('?')[1]).get('lang') ?? 'EN';
        const localePath = localePaths.get(language);
        if (!localePath) {
          response.writeHead(400).end('Unknown translation language.');
          return;
        }
        try {
          const dictionary = language === 'EN' ? translations : JSON.parse(await readFile(mediaFile(localePath), 'utf8'));
          const files = [...entityAssetFiles(entity), ...entityTranslationFiles(entity, dictionary, language)];
          if (files.length < 2) {
            response.writeHead(404).end();
            return;
          }
          const bytes = await createAssetArchive(files, (path) => readFile(mediaFile(path)));
          const filename = entity.route.split('/').at(-1) + '-assets.zip';
          response.writeHead(200, {
            'Content-Type': 'application/zip',
            'Content-Length': bytes.byteLength,
            'Content-Disposition': 'attachment; filename="' + filename + '"',
          });
          response.end(request.method === 'HEAD' ? undefined : bytes);
        } catch (error) {
          server.config.logger.error('Asset ZIP failed for ' + entity.key + ': ' + error.message);
          response.writeHead(500).end('Could not prepare the asset ZIP.');
        }
        return;
      }

      const assetFile = resolve(assetsRoot, path);
      const file = path === 'static_data.json' ? staticDataPath : mediaFile(path);
      const type = types[extname(path).toLowerCase()];
      if (!paths.has(path) || !assetFile.startsWith(assetsRoot + sep) || !type) {
        response.writeHead(404).end();
        return;
      }

      const stat = statSync(file, { throwIfNoEntry: false });
      if (!stat?.isFile()) {
        response.writeHead(404).end();
        return;
      }
      const range = type === 'audio/ogg' ? audioBytes(request.headers.range, stat.size) : null;
      if (range && range.start > range.end) {
        response.writeHead(416, { 'Content-Range': 'bytes */' + stat.size }).end();
        return;
      }
      response.writeHead(range ? 206 : 200, {
        'Content-Type': type,
        'Content-Length': range ? range.end - range.start + 1 : stat.size,
        'X-Content-Type-Options': 'nosniff',
        ...(type === 'audio/ogg' && { 'Accept-Ranges': 'bytes' }),
        ...(range && { 'Content-Range': `bytes ${range.start}-${range.end}/${stat.size}` }),
      });
      if (request.method === 'HEAD') {
        response.end();
      } else {
        const stream = createReadStream(file, range || undefined);
        stream.on('error', () => response.destroy());
        response.on('close', () => stream.destroy());
        stream.pipe(response);
      }
    });
  }

  return {
    name: 'clashking-local-assets',
    resolveId(id) { if (id === 'virtual:wiki-audio') return virtualAudio; },
    load(id) { if (id === virtualAudio) return 'export default ' + JSON.stringify(audio); },
    configureServer: mount,
    configurePreviewServer: mount,
  };
}

export default defineConfig(({ mode }) => {
  const { WIKI_STATIC_DATA_PATH, WIKI_ARTWORK_OVERLAY } = loadEnv(mode, fileURLToPath(new URL('.', import.meta.url)), 'WIKI_');
  return {
    publicDir: false,
    build: { assetsDir: 'site/_assets' },
    plugins: [localAssets(WIKI_STATIC_DATA_PATH || undefined, WIKI_ARTWORK_OVERLAY)],
  };
});
