export function entityTranslationFiles(entity, dictionary, language) {
  const strings = new Map();
  function collect(value) {
    if (!value || typeof value !== 'object') return;
    for (const key of Object.values(value.TID || {})) {
      if (typeof key === 'string' && typeof dictionary[key] === 'string') strings.set(key, dictionary[key]);
    }
    for (const child of Object.values(value)) collect(child);
  }
  collect(entity);
  if (!strings.size) return [];
  return [{
    path: 'translations-' + language.toLowerCase() + '.json',
    data: new TextEncoder().encode(JSON.stringify(Object.fromEntries(strings), null, 2) + '\n'),
  }];
}

export async function createAssetArchive(files, readAsset) {
  const { zip } = await import('fflate');
  const parts = Object.create(null);
  const basenames = files.map(({ path }) => path.split('/').at(-1));
  const originalNames = new Set(basenames.map((name) => name.toLowerCase()));
  const usedNames = new Set();
  const entryNames = basenames.map((name) => {
    const key = name.toLowerCase();
    if (!usedNames.has(key)) {
      usedNames.add(key);
      return name;
    }

    // Keep duplicate basenames distinct on case-insensitive filesystems without claiming another file's original name.
    const extensionAt = name.lastIndexOf('.');
    const stem = extensionAt > 0 ? name.slice(0, extensionAt) : name;
    const extension = extensionAt > 0 ? name.slice(extensionAt) : '';
    for (let suffix = 2; ; suffix++) {
      const candidate = stem + '-' + suffix + extension;
      const candidateKey = candidate.toLowerCase();
      if (originalNames.has(candidateKey) || usedNames.has(candidateKey)) continue;
      usedNames.add(candidateKey);
      return candidate;
    }
  });
  for (let start = 0; start < files.length; start += 5) {
    await Promise.all(files.slice(start, start + 5).map(async ({ path, data }, index) => {
      parts[entryNames[start + index]] = data ?? await readAsset(path);
    }));
  }
  return new Promise((resolve, reject) => zip(parts, { level: 0 }, (error, bytes) => error ? reject(error) : resolve(bytes)));
}
