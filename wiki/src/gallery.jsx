import { useMemo, useState } from 'react';
import { ArrowDownToLine, ArrowUpRight, Copy, Image, LoaderCircle, Maximize2, Music2 } from 'lucide-react';
import { assetOrigin, assetUrl, cdnUrl, entityAssetFiles, number, titleCase, wiki } from './catalog.js';
import { entityTranslationFiles } from './archive.js';
import { downloadAsset, downloadAssets } from './exports.js';
import { ActionMenu, ActionStatus, Art, AudioPlayer, AutoLoadResults, Crumbs, Empty, MenuAction, Modal, Toggle, WikiSelect, resultsBatchSize } from './ui.jsx';

const examples = [
  'buildings/home-village/squad_barracks/level_5.webp', 'heroes/archer_queen/icon.webp',
  'sceneries/tiger_mountain/thumbnail.webp', 'equipment/giant_gauntlet/icon.webp',
  'troops/electrofire_wizard/icon.webp', 'resources/starry_ore.webp',
  'buildings/home-village/town_hall/level_19.webp', 'skins/archer_queen/icon.webp',
  'spells/rage_spell/icon.webp', 'buildings/builder-base/builder_hall/level_10.webp',
  'equipment/dark_crown/icon.webp', 'buildings/seasonal-defense/hot_candle/level_1.webp',
];
const sort = new Intl.Collator('en', { numeric: true });
const featuredAssetGroups = ['buildings', 'troops', 'heroes', 'equipment', 'spells', 'sceneries', 'skins', 'resources', 'sfx', 'audio'];
function fileTitle(file) {
  if (file.group === 'audio') return file.display_name;
  if (file.group === 'sfx') return titleCase(file.display_name);
  const parts = file.path.split('/');
  const name = file.display_name || parts.at(-1).split('.')[0];
  const variant = parts.at(-1).split('.')[0];
  if (['thumbnail', 'icon', 'portrait'].includes(variant) && parts.length >= 3) return titleCase(parts.at(-2)) + ' ' + (variant === 'thumbnail' ? 'preview' : variant);
  return titleCase(name) + (file.group === 'skins' ? ' skin' : '');
}
const groupNames = { all: 'All assets', sfx: 'Sound effects', audio: 'Scenery music' };
const groupTitle = (group) => groupNames[group] || titleCase(group);
const isAudio = (group) => group === 'sfx' || group === 'audio';
const soundScopes = [['all', 'All sounds'], ['general', 'General']];

function FileDialog({ file, owner, scoped, onClose }) {
  const [status, setStatus] = useState('');
  const [busy, setBusy] = useState(false);
  const [dimensions, setDimensions] = useState('');
  const type = file.path.split('.').at(-1);
  const copyUrl = async () => {
    setStatus('');
    try {
      await navigator.clipboard.writeText(file.group === 'sfx'
        ? new URL(assetUrl(file.path), location.origin).href
        : cdnUrl(file.path));
      setStatus('URL copied');
    } catch {
      setStatus('Could not copy the URL');
    }
  };
  return <Modal title={fileTitle(file)} onClose={onClose} className="file-modal">
    {isAudio(file.group)
      ? <div className="scenery-audio"><AudioPlayer track={file} onError={() => setStatus('Audio preview unavailable')} /></div>
      : <div className="file-stage">
        <img src={assetUrl(file.path)} alt={fileTitle(file)}
          onLoad={(event) => setDimensions(event.currentTarget.naturalWidth + ' × ' + event.currentTarget.naturalHeight + ' px')}
          onError={() => setStatus('Preview unavailable')} />
      </div>}
    <div className="file-info"><div><span>{type}</span>{dimensions && <span>{dimensions}</span>}{file.animated && <span>Animated</span>}</div>
      <details><summary>File details</summary><code>{file.path}</code>{file.sha && <code>SHA-256 {file.sha}</code>}</details>
    </div>
    <div className="modal-actions">
      <button type="button" className="button primary" disabled={busy} onClick={async () => {
        setBusy(true);
        setStatus('');
        try { await downloadAsset(file.path); }
        catch { setStatus('Download failed. Try again.'); }
        finally { setBusy(false); }
      }}><ArrowDownToLine size={17} />Download</button>
      <ActionMenu label="More" side="top">
        <MenuAction onSelect={copyUrl}><Copy size={16} />Copy URL</MenuAction>
        {owner?.assetOnly
          ? !scoped && <MenuAction asChild><a href={wiki + '/assets?entity=' + encodeURIComponent(owner.key)}><ArrowUpRight size={16} />Item assets</a></MenuAction>
          : owner && <MenuAction asChild><a href={owner.route + (file.path.match(/\/level_(\d+)\.webp$/) ? '?level=' + file.path.match(/\/level_(\d+)\.webp$/)[1] : '')}><ArrowUpRight size={16} />Wiki page</a></MenuAction>}
      </ActionMenu>
      <ActionStatus>{status}</ActionStatus>
    </div>
  </Modal>;
}

function DownloadArtwork({ entity, files, language }) {
  const [status, setStatus] = useState('');
  const [busy, setBusy] = useState(false);
  const download = async () => {
    setBusy(true);
    setStatus('');
    try {
      await downloadAssets(files, entity.route.split('/').at(-1) + '-assets.zip');
    } catch {
      setStatus('Download failed. Try again.');
    } finally {
      setBusy(false);
    }
  };
  if (assetOrigin === '/media') return <a className="button bundle-button" href={assetUrl('bundles/' + entity.key + '.zip') + '?lang=' + encodeURIComponent(language)}>
    <ArrowDownToLine size={17} aria-hidden="true" />Download ZIP
  </a>;
  return <>
    <button type="button" className="button bundle-button" disabled={busy} aria-busy={busy} onClick={download}>
      {busy ? <LoaderCircle className="action-spinner" size={17} aria-hidden="true" /> : <ArrowDownToLine size={17} aria-hidden="true" />}
      Download ZIP
    </button>
    <span className="sr-only" role="status">{busy ? 'Preparing ZIP…' : ''}</span>
    <ActionStatus>{status}</ActionStatus>
  </>;
}

function AssetTiles({ files, titleFor, onSelect, resetKey, label, compact = false }) {
  if (!files.length) return <Empty>No assets match this search.</Empty>;
  return <AutoLoadResults count={files.length} resetKey={resetKey} label={label}>{(limit) =>
    <div className={compact ? 'asset-grid sound-grid' : 'asset-grid'}>{files.slice(0, limit).map((file) =>
      <button className="asset-tile" key={file.path} onClick={() => onSelect(file.path)}>
        <span className="asset-art">{isAudio(file.group) ? <Music2 size={compact ? 18 : 34} className="asset-audio-icon" /> : <Art file={file} />}</span>
        <span className="asset-label">{titleFor(file)}<Maximize2 size={13} /></span>
      </button>)}</div>
  }</AutoLoadResults>;
}

export function AssetGallery({ catalogue, url, scopedEntity, language = 'EN', dictionary = {}, query = url.searchParams.get('q') || '' }) {
  const entity = scopedEntity || catalogue.entitiesByKey.get(url.searchParams.get('entity'));
  const bundleFiles = useMemo(() => entity ? [
    ...entityAssetFiles(entity), ...entityTranslationFiles(entity, dictionary, language),
  ] : [], [entity, dictionary, language]);
  const level = url.searchParams.get('level');
  const fragment = new URLSearchParams(url.hash.slice(1));
  const initialPath = url.searchParams.get('path') || fragment.get('path') || fragment.get('asset');
  const [activePath, setActivePath] = useState(initialPath);
  const initialGroup = url.searchParams.get('group');
  const [group, setGroup] = useState(!entity && catalogue.groups.includes(initialGroup) ? initialGroup : 'all');
  const [soundScope, setSoundScope] = useState(initialGroup === 'sfx' && url.searchParams.get('sound') === 'general' ? 'general' : 'all');
  const groups = [['all', groupTitle('all')], ...catalogue.groups
    .map((key) => [key, groupTitle(key)])
    .sort(([, a], [, b]) => sort.compare(a, b))];
  const term = query.toLowerCase().trim().replace(/\s+/g, ' ');
  const chosen = entity
    ? entity.files.find((file) => file.path === activePath) || entity.soundEffects.find((file) => file.path === activePath)
      || (entity.track?.path === activePath ? entity.track : undefined)
    : catalogue.byPath.get(activePath);
  const tileTitle = (file) => {
    const name = fileTitle(file);
    return entity && name.toLowerCase().startsWith(entity.name.toLowerCase() + ' ') ? name.slice(entity.name.length + 1) : name;
  };
  const files = useMemo(() => {
    if (entity) return entity.files;
    const first = examples.map((path) => catalogue.byPath.get(path)).filter(Boolean);
    const sorted = catalogue.files.filter((file) => !examples.includes(file.path)).sort((a, b) => sort.compare(a.path, b.path));
    const queues = [...featuredAssetGroups, ...catalogue.groups.filter((key) => !featuredAssetGroups.includes(key))]
      .map((group) => sorted.filter((file) => file.group === group));
    const mix = [];
    for (let index = 0; index < Math.max(...queues.map((queue) => queue.length)); index++) {
      for (const queue of queues) if (queue[index]) mix.push(queue[index]);
    }
    return [...first, ...mix];
  }, [entity, catalogue]);
  const matchesQuery = useMemo(() => {
    if (!term) return () => true;
    const parts = term.split(' ');
    return (file) => {
      const keywords = [file.path, file.display_name, file.group].join(' ').replaceAll('_', ' ').toLowerCase();
      return parts.every((part) => keywords.includes(part));
    };
  }, [term]);
  const shown = useMemo(() => {
    const general = group === 'sfx' && soundScope === 'general';
    const matches = files.filter((file) => (group === 'all' || file.group === group)
      && (!general || !catalogue.itemSoundPaths.has(file.path)) && matchesQuery(file));
    if (group !== 'all') matches.sort((a, b) => sort.compare(a.path, b.path));
    return matches;
  }, [files, group, soundScope, catalogue.itemSoundPaths, matchesQuery]);
  const sections = entity ? [
    entity.files.length > 0 && { id: 'artwork-heading', title: 'Artwork', files: shown },
    entity.soundEffects.length > 0 && { id: 'sound-effects-heading', title: 'Sound effects', files: entity.soundEffects.filter(matchesQuery), audio: true },
    entity.track && { id: 'scenery-music-heading', title: 'Scenery music', files: [entity.track].filter(matchesQuery), audio: true },
  ].filter(Boolean) : [];
  const firstLongSection = sections.findIndex((section) => section.files.length > resultsBatchSize);
  const jumps = firstLongSection < 0 ? [] : sections.slice(firstLongSection + 1).filter((section) => section.files.length);
  const setFile = (path) => {
    setActivePath(path);
    const next = new URL(location.href);
    next.hash = '';
    if (path) next.searchParams.set('path', path);
    else next.searchParams.delete('path');
    history.replaceState(null, '', next);
  };
  const changeGroup = (key) => {
    setGroup(key);
    const next = new URL(location.href);
    if (key === 'all') next.searchParams.delete('group');
    else next.searchParams.set('group', key);
    if (key !== 'sfx') {
      setSoundScope('all');
      next.searchParams.delete('sound');
    }
    history.replaceState(null, '', next);
  };
  const changeSoundScope = (scope) => {
    setSoundScope(scope);
    const next = new URL(location.href);
    if (scope === 'general') next.searchParams.set('sound', scope);
    else next.searchParams.delete('sound');
    history.replaceState(null, '', next);
  };

  return <>
    {entity && <div className="asset-heading">
      <Crumbs items={[[entity.name, entity.assetOnly ? undefined : entity.route + (level ? '?level=' + encodeURIComponent(level) : '')], ['Assets']]} />
      <div className="gallery-actions">
        {bundleFiles.length > 1 && <DownloadArtwork entity={entity} files={bundleFiles} language={language} />}
      </div>
    </div>}
    <h1 className="sr-only">{entity ? entity.name + ' assets' : 'Assets'}</h1>
    <div className={'gallery-layout' + (entity ? ' entity-gallery' : '')}>
      {!entity && <aside className="gallery-groups" aria-label="Asset categories">
        <div>Categories</div>
        {groups.map(([key, name]) =>
          <button key={key} type="button" aria-pressed={group === key} onClick={() => changeGroup(key)}>{name}</button>)}
      </aside>}
      <section className="gallery-results" aria-label="Asset results">
        {!entity && <WikiSelect className="gallery-mobile-select" label="Asset category" hideLabel value={group}
          options={groups} onChange={changeGroup} />}
        {entity ? <>
          {jumps.length > 0 && <nav className="result-jumps" aria-label="Jump to asset results">
            {jumps.map(({ id, title }) => <a key={id} href={'#' + id}>Jump to {title.toLowerCase()}</a>)}
          </nav>}
          {sections.map(({ id, title, files, audio }) => <section key={id} className="section" aria-label={sections.length === 1 ? title : undefined} aria-labelledby={sections.length > 1 ? id : undefined}>
            {sections.length > 1 && <div className="section-heading"><h2 id={id} tabIndex={-1}>{title}</h2></div>}
            <AssetTiles files={files} titleFor={tileTitle} onSelect={setFile} resetKey={term} label={title} compact={audio} />
          </section>)}
        </> : <>
          <div className="gallery-summary">
            {group === 'sfx' && <Toggle label="Sound effects filter" items={soundScopes} value={soundScope} onChange={changeSoundScope} />}
            <div className="gallery-count">{isAudio(group) ? <Music2 size={16} /> : <Image size={16} />}{number(shown.length)} {isAudio(group)
              ? (shown.length === 1 ? 'track' : 'tracks')
              : group === 'all' ? (shown.length === 1 ? 'asset' : 'assets') : (shown.length === 1 ? 'image' : 'images')}</div>
          </div>
          <AssetTiles files={shown} titleFor={tileTitle} onSelect={setFile} resetKey={group + ':' + soundScope + ':' + term} compact={isAudio(group)} />
        </>}
      </section>
    </div>
    {chosen && <FileDialog key={chosen.path} file={chosen} owner={entity || catalogue.ownerByPath.get(chosen.path)} scoped={!!entity} onClose={() => setFile(null)} />}
  </>;
}
