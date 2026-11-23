import { StrictMode, useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { ChevronRight, Moon, Sun } from 'lucide-react';
import { assetUrl, categories, makeCatalogue, villages, wiki } from './catalog.js';
import { Art, AutoLoadResults, Empty, SearchBox, resultsBatchSize } from './ui.jsx';
import { EntityPage } from './entity.jsx';
import { AssetGallery } from './gallery.jsx';
import { LanguagePicker } from './language-picker.jsx';
import { makeReferences, referenceMatches, referencePath } from './reference-data.js';
import { ReferenceLinks, ReferencePage } from './reference.jsx';
import { WikiLocale, useWikiText } from './locale.js';
import audio from 'virtual:wiki-audio';
import crownDark from '../../assets/logos/crown-arrow-dark-bg/ClashKing-1.svg?url';
import crownLight from '../../assets/logos/crown-arrow-white-bg/ClashKing-2.svg?url';
import './styles.css';

const featured = [1000106, 28000001, 4000106, 90000010];
const nav = [
  ['Wiki', wiki, 'wiki'],
  ['Reference', referencePath, 'reference'],
  ['Assets', wiki + '/assets', 'assets'],
];

function defaultTheme() {
  return localStorage.getItem('ck-wiki-theme') || (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
}
document.documentElement.dataset.theme = defaultTheme();

async function load(path) {
  const response = await fetch(assetUrl(path));
  if (!response.ok) throw new Error(path + ' returned ' + response.status);
  return response.json();
}

const alternateLanguageTags = { CN: 'zh-Hans', CNT: 'zh-Hant', JP: 'ja', KR: 'ko' };
const englishNames = new Intl.DisplayNames('en', { type: 'language' });

async function loadSources() {
  const [data, manifest, abilities, sfx, translations] = await Promise.all([
    load('static_data.json'), load('manifest.json'), load('static_data/abilities.json'),
    load('static_data/sfx.json'), load('translations/EN.json'),
  ]);
  const languages = new Map(data.locales.map(({ locale, name }) => [locale, name]));
  const locales = manifest.data?.translations || [];
  const options = new Map([['EN', { code: 'EN', tag: 'en', name: 'English', english: 'English' }]]);
  for (const { path } of locales) {
    const code = /^translations\/([A-Z]{2,3})\.json$/.exec(path)?.[1];
    if (!code || code === 'EN') continue;
    const tag = alternateLanguageTags[code] || code.toLowerCase();
    // The export has a mixed-script Russian label and omits some locales from its name list.
    const native = (code === 'RU' ? null : languages.get(code)) || new Intl.DisplayNames(tag, { type: 'language' }).of(tag);
    const name = native ? native[0].toLocaleUpperCase(tag) + native.slice(1) : code;
    options.set(code, { code, tag, name, english: englishNames.of(tag) || name });
  }
  const ordered = [...options.values()].sort((a, b) => a.code === 'EN' ? -1 : b.code === 'EN' ? 1 : a.english.localeCompare(b.english, 'en'));
  return { data, manifest, abilities, sfx, translations, options: ordered };
}
const sourcesRequest = loadSources();
const translationRequests = new Map();
const defaultLanguage = localStorage.getItem('ck-wiki-language') || 'EN';
function Topbar({ area, query, onSearch, language, languages, onLanguage, pendingLanguage }) {
  const t = useWikiText();
  const [theme, setTheme] = useState(defaultTheme);
  const ThemeIcon = theme === 'dark' ? Sun : Moon;
  const switchTheme = () => {
    const next = theme === 'dark' ? 'light' : 'dark';
    localStorage.setItem('ck-wiki-theme', next);
    document.documentElement.dataset.theme = next;
    setTheme(next);
  };
  return <header className="site-header">
    <div className="site-header-inner">
      <a href={wiki} className="brand">
        <img src={theme === 'dark' ? crownDark : crownLight} alt="" width="24" height="26" />
        <span className="brand-name">ClashKing</span>
      </a>
      <nav className="site-nav" aria-label="Main navigation">{nav.map(([name, href, id]) =>
        <a key={href} href={href} aria-current={area === id ? 'page' : undefined}>{t(name)}</a>)}
      </nav>
      <div className="header-controls">
        <SearchBox className="header-search" value={query} onChange={onSearch} shortcut
          placeholder={language === 'EN' ? (area === 'assets' ? 'Search assets' : 'Search') : t('Search')} />
        {languages?.length > 1 && <LanguagePicker languages={languages} value={language} pending={pendingLanguage} onChange={onLanguage} />}
        <button className="icon-button theme-button" aria-label={'Switch to ' + (theme === 'dark' ? 'light' : 'dark') + ' theme'} onClick={switchTheme}><ThemeIcon size={17} /></button>
      </div>
    </div>
  </header>;
}

function cardMeta(entity, t, showCategory) {
  const cat = entity.isSquad ? 'squads' : entity.category;
  const parts = [];
  if (showCategory && categories[cat]) parts.push(t(categories[cat][0]));
  if (entity.kind === 'equipment' && entity.hero) parts.push(entity.hero);
  else if (entity.levels?.length > 1) parts.push(entity.levels.length + ' levels');
  return parts.join(' · ');
}

function WikiCard({ entity, showCategory = true }) {
  const t = useWikiText();
  const meta = cardMeta(entity, t, showCategory);
  return <a className={'tile ' + entity.village} href={entity.route}>
    <span className="tile-art"><Art file={entity.art} /></span>
    <span className="tile-name">{entity.name}</span>
    <span className="tile-meta">{entity.rarity === 'Epic' && <span className="epic-text">Epic</span>}{entity.rarity === 'Epic' && meta ? ' · ' : ''}{meta}</span>
  </a>;
}

const villageTabs = [
  ['home', 'buildings/home-village/town_hall/level_19.webp', wiki],
  ['builder-base', 'buildings/builder-base/builder_hall/level_10.webp', wiki + '/builder-base'],
];

function WikiResults({ items, resetKey, label, showCategory }) {
  if (!items.length) return <Empty>No entries match this search.</Empty>;
  return <AutoLoadResults count={items.length} resetKey={resetKey} label={label}>{(limit) =>
    <div className="tile-grid">{items.slice(0, limit).map((entity) => <WikiCard entity={entity} key={entity.key} showCategory={showCategory} />)}</div>
  }</AutoLoadResults>;
}

function WikiHome({ catalogue }) {
  const highlights = featured.map((id) => catalogue.entities.find((entity) => entity._id === id)).filter(Boolean);
  const topics = ['upgrades', 'ranked-leagues', 'clan-war-leagues', 'star-bonus', 'magic-items', 'battle-modes']
    .map((id) => catalogue.references.get(id)).filter(Boolean);
  return <section className="home-intro">
    <div className="home-copy">
      <h1>Clash of Clans Field Guide</h1>
      <p>Troop stats, building upgrades and artwork.</p>
      {highlights.length > 0 && <p className="home-popular">
        <span className="home-popular-label">Popular</span>
        {highlights.map((entity) => <a key={entity.key} href={entity.route}><Art file={entity.art} />{entity.name}</a>)}
      </p>}
    </div>
    <nav className="home-guides" aria-labelledby="home-guides-title">
      <div className="home-guides-head">
        <h2 id="home-guides-title">Game reference</h2>
        <a className="more-link" href={referencePath}>All guides<ChevronRight size={14} aria-hidden="true" /></a>
      </div>
      <ul>{topics.map((topic) => <li key={topic.id}>
        <a href={topic.route}><Art file={topic.art} /><span><strong>{topic.title}</strong><small>{topic.detail}</small></span></a>
      </li>)}</ul>
    </nav>
  </section>;
}

function Browse({ catalogue, area, initialCategory, query }) {
  const t = useWikiText();
  const [category, setCategory] = useState(initialCategory || 'all');
  const mainPage = area === 'wiki';
  const village = mainPage ? 'home' : area;
  const term = query.trim().toLowerCase().replace(/\s+/g, ' ');
  const all = catalogue.entities.filter((item) => item.village === village);
  const inCategory = (item, key) => key === 'squads' ? item.isSquad : item.category === key && (key !== 'troops' || !item.isSquad);
  const categoryItems = Object.entries(categories).filter(([key]) => all.some((item) => inCategory(item, key)));
  const shown = (term ? catalogue.entities : all).filter((item) =>
    (!term ? category === 'all' || inCategory(item, category) : [item.name, item.sourceName, item.village, item.hero, item.category, item.type].some((value) => value?.toLowerCase().includes(term))));
  const referenceResults = referenceMatches(catalogue.references, term);
  const showDiscovery = mainPage && !term;
  const villageResults = term ? Object.keys(villages).map((key) => ({
    key,
    items: shown.filter((item) => item.village === key),
  })).filter(({ items }) => items.length) : [];
  const firstLongGroup = villageResults.findIndex(({ items }) => items.length > resultsBatchSize);
  const jumps = firstLongGroup < 0 ? [] : villageResults.slice(firstLongGroup + 1);
  const heading = term ? 'Search results' : t(villages[village]);
  const Heading = showDiscovery ? 'h2' : 'h1';
  return <>
    {mainPage && !showDiscovery && <h1 className="sr-only">ClashKing Wiki</h1>}
    {showDiscovery && <WikiHome catalogue={catalogue} />}
    <section className={'browse' + (showDiscovery ? ' with-intro' : '')}>
      <div className="browse-head">
        <div className="title-row">
          <Heading id={showDiscovery ? 'village-browse' : undefined} tabIndex={showDiscovery ? -1 : undefined}>{heading}</Heading>
          <span className="count">{term ? shown.length + referenceResults.length : shown.length}</span>
        </div>
        {!term && <nav className="segmented" aria-label="Village">
          {villageTabs.map(([key, art, href]) => (
            <a key={key} href={href} aria-current={village === key ? 'page' : undefined}><Art file={art} />{t(villages[key])}</a>
          ))}
        </nav>}
      </div>
      {!term && <div className="tabs" role="group" aria-label="Categories">
        <button type="button" aria-pressed={category === 'all'} onClick={() => setCategory('all')}>All</button>
        {categoryItems.map(([key, [name]]) => <button type="button" key={key} aria-pressed={category === key} onClick={() => setCategory(key)}>{t(name)}</button>)}
      </div>}
      {jumps.length > 0 && <nav className="result-jumps" aria-label="Jump to village results">
        {jumps.map(({ key }) => <a key={key} href={'#search-' + key}>Jump to {villages[key]}</a>)}
      </nav>}
      {referenceResults.length > 0 && <section className="reference-search" aria-labelledby="reference-results-title">
        <h3 id="reference-results-title">Game reference</h3>
        <AutoLoadResults count={referenceResults.length} resetKey={term} label="Game reference">{(limit) =>
          <ReferenceLinks links={referenceResults.slice(0, limit)} />
        }</AutoLoadResults>
      </section>}
      {villageResults.length > 1 ? <div className="search-groups">
        {villageResults.map(({ key, items }) => <section key={key} aria-labelledby={'search-' + key}>
          <h3 id={'search-' + key} tabIndex={-1}>{villages[key]}</h3>
          <WikiResults items={items} resetKey={term} label={villages[key]} />
        </section>)}
      </div> : (shown.length > 0 || !referenceResults.length) && <WikiResults resetKey={term ? 'search:' + term : category} items={shown} showCategory={!!term || category === 'all'} />}
    </section>
  </>;
}

function App() {
  const [sources, setSources] = useState();
  const [localized, setLocalized] = useState(null);
  const [failure, setFailure] = useState(false);
  const [search, setSearch] = useState(null);
  const [route, setRoute] = useState(() => location.pathname + location.search + location.hash);
  useEffect(() => {
    let mounted = true;
    sourcesRequest.then((data) => { if (mounted) setSources(data); }, () => { if (mounted) setFailure(true); });
    const sync = () => setRoute(location.pathname + location.search + location.hash);
    window.addEventListener('popstate', sync);
    window.addEventListener('hashchange', sync);
    return () => { mounted = false; window.removeEventListener('popstate', sync); window.removeEventListener('hashchange', sync); };
  }, []);
  const language = localized?.code || 'EN';
  const dictionary = localized?.dictionary || sources?.translations;
  const catalogue = useMemo(() => {
    if (!sources) return undefined;
    const strings = localized ? { ...sources.translations, ...localized.dictionary } : sources.translations;
    const result = makeCatalogue(sources.data, sources.manifest, sources.abilities, strings, audio, sources.sfx);
    return { ...result, references: makeReferences(sources.data, strings, result.byPath, language) };
  }, [sources, localized, language]);
  const [languageError, setLanguageError] = useState('');
  const [pendingLanguage, setPendingLanguage] = useState(null);
  const languageRequest = useRef('');
  const changeLanguage = async (code) => {
    const option = sources?.options.find((option) => option.code === code);
    if (!option) return;
    languageRequest.current = code;
    setLanguageError('');
    setPendingLanguage(code);
    if (code === 'EN') setLocalized(null);
    else {
      try {
        if (!translationRequests.has(code)) translationRequests.set(code, load('translations/' + code + '.json'));
        const dictionary = await translationRequests.get(code);
        if (languageRequest.current !== code) return;
        setLocalized({ code, dictionary });
      } catch {
        translationRequests.delete(code);
        if (languageRequest.current === code) {
          setLanguageError('Could not load that language. Try again.');
          setPendingLanguage(null);
        }
        return;
      }
    }
    document.documentElement.lang = option.tag;
    localStorage.setItem('ck-wiki-language', code);
    setPendingLanguage(null);
  };
  useEffect(() => { if (sources && defaultLanguage !== 'EN') changeLanguage(defaultLanguage); }, [sources]);
  const url = new URL(route, location.origin);
  const path = url.pathname.replace(/\/$/, '') || wiki;
  const [, first, page, category, id, slug, tool, ...extra] = path.split('/');
  const entity = id && !extra.length && catalogue?.entities.find((item) =>
    item.village === page && item.category === category && String(item._id) === id);
  const entityPath = entity && slug === entity.route.split('/').at(-1) ? entity.route : null;
  const entityAssets = entityPath && tool === 'assets';
  const cosmetic = id && catalogue?.entitiesByKey.get(category + ':' + id);
  const oldCosmetic = cosmetic?.assetOnly && [cosmetic.route, cosmetic.route + '/assets', cosmetic.route + '/artwork'].includes(path);
  const oldCosmeticGroup = path === wiki + '/cosmetics' || path === wiki + '/cosmetics/skins'
    || path === wiki + '/cosmetics/sceneries' || path === wiki + '/home/decorations';
  let assetsTarget = entityPath && tool === 'artwork' ? entityPath + '/assets' + url.search + url.hash : null;
  if (oldCosmetic || oldCosmeticGroup) {
    const filters = new URLSearchParams(url.search);
    if (oldCosmetic) filters.set('entity', cosmetic.key);
    else if (category) filters.set('group', category);
    assetsTarget = wiki + '/assets' + (filters.size ? '?' + filters : '') + url.hash;
  }
  const formerChart = entityPath && tool === 'charts';
  const legacyChartEntity = path === wiki + '/charts' && catalogue?.entitiesByKey.get(url.searchParams.get('entity'));
  const chartEntity = formerChart ? entity : legacyChartEntity?.assetOnly ? null : legacyChartEntity;
  const chartParams = new URLSearchParams(url.search);
  chartParams.delete('entity');
  const chartTarget = chartEntity ? chartEntity.route + (chartParams.size ? '?' + chartParams : '') + '#charts' : null;
  const gallery = entityAssets || path === wiki + '/assets';
  const referenceTopic = page === 'reference' && !id && catalogue?.references.get(category);
  const reference = path === referencePath || !!referenceTopic;
  const browse = first === 'wiki' && (!page || villages[page]) && (!category || categories[category]) && !id;
  const primaryArea = gallery ? 'assets' : reference ? 'reference' : 'wiki';
  const initialQuery = url.searchParams.get('q') || '';
  const scopedAssetKey = gallery && url.searchParams.get('entity');
  const searchScope = path
    + (scopedAssetKey ? '?entity=' + encodeURIComponent(scopedAssetKey) : '')
    + (initialQuery ? '#q=' + encodeURIComponent(initialQuery) : '');
  const query = search?.scope === searchScope ? search.value : initialQuery;
  useEffect(() => {
    setSearch((current) => current?.scope === searchScope ? current : null);
  }, [searchScope]);
  const changeSearch = (value) => {
    if (!gallery && !browse && value.trim()) {
      // Use the same results as Wiki home and preserve focus in the shared header.
      history.pushState(null, '', wiki);
      setSearch({ scope: wiki, value });
      setRoute(wiki);
      window.scrollTo(0, 0);
    } else {
      setSearch({ scope: searchScope, value });
    }
  };
  useEffect(() => {
    if (!catalogue) return;
    const replacement = assetsTarget || chartTarget || (path === wiki + '/charts' ? wiki : null);
    if (!replacement) return;
    history.replaceState(null, '', replacement);
    setRoute(replacement);
  }, [catalogue, assetsTarget, chartTarget, path]);
  useEffect(() => {
    if (!catalogue || !(url.hash === '#charts' || (reference && url.hash.startsWith('#entry-')))) return;
    const section = document.getElementById(url.hash.slice(1));
    section?.scrollIntoView();
    if (reference) section?.focus({ preventScroll: true });
  }, [catalogue, reference, route, url.hash]);
  useEffect(() => {
    const suffix = entityAssets ? ' assets' : '';
    document.title = (entity ? entity.name + suffix : reference ? referenceTopic?.title || 'Game reference' : nav.find(([, , id]) => id === primaryArea)?.[0] || 'Wiki') + ' · ClashKing';
  }, [entity, entityAssets, primaryArea, reference, referenceTopic]);
  const navigate = (event) => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    const anchor = event.target.closest('a');
    if (!anchor || anchor.target || anchor.hasAttribute('download')) return;
    const next = new URL(anchor.href);
    if (next.origin !== location.origin || !/^\/wiki(?:\/|$)/.test(next.pathname)) return;
    event.preventDefault();
    if (next.pathname === location.pathname && next.search === location.search && next.hash) {
      const section = document.getElementById(next.hash.slice(1));
      if (section) {
        section.scrollIntoView();
        section.focus({ preventScroll: true });
        return;
      }
    }
    const target = next.pathname + next.search + next.hash;
    if (target === location.pathname + location.search + location.hash) return;
    history.pushState(null, '', target);
    setRoute(target);
    if (next.pathname !== url.pathname) {
      window.scrollTo(0, 0);
      document.getElementById('content')?.focus({ preventScroll: true });
    }
  };
  let content;
  if (failure) content = <Empty>The wiki data could not load. <button className="button" onClick={() => location.reload()}>Reload</button></Empty>;
  else if (!catalogue) content = <p className="loading" role="status">Loading the wiki…</p>;
  else if (first !== 'wiki') content = <Empty>This page does not exist. <a href={wiki}>Browse the wiki</a></Empty>;
  else if (gallery) content = <AssetGallery key={route} catalogue={catalogue} url={url} scopedEntity={entityAssets ? entity : undefined} language={language} dictionary={dictionary} query={query} />;
  else if (assetsTarget || chartTarget || path === wiki + '/charts') content = null;
  else if (reference) content = <ReferencePage key={path} catalogue={catalogue} references={catalogue.references} topic={referenceTopic || undefined} url={url} onUrlChange={setRoute} />;
  else if (entity && !tool) content = <EntityPage key={route} catalogue={catalogue} entity={entity} url={url} />;
  else if (browse) content = <Browse key={path} catalogue={catalogue} area={page || 'wiki'} initialCategory={category} query={query} />;
  else content = <Empty>This page does not exist. <a href={wiki}>Browse the wiki</a></Empty>;
  return <WikiLocale.Provider value={language}><div className="app-shell" onClick={navigate}>
    <a className="skip" href="#content">Skip to content</a>
    <a className="skip" href="#site-footer">Skip to footer</a>
    <Topbar area={primaryArea} query={query} onSearch={changeSearch} language={language} languages={sources?.options}
      onLanguage={changeLanguage} pendingLanguage={pendingLanguage} />
    <main id="content" tabIndex={-1} className="site-main">{languageError && <p className="note" role="status">{languageError}</p>}{content}</main>
    <footer id="site-footer" className="site-footer" tabIndex={-1}>
      <span>ClashKing · Unofficial fan content</span>
      <a href="https://supercell.com/en/fan-content-policy/" target="_blank" rel="noreferrer">Supercell Fan Content Policy</a>
    </footer>
  </div></WikiLocale.Provider>;
}

createRoot(document.getElementById('root')).render(<StrictMode><App /></StrictMode>);
