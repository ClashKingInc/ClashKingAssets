import { duration, stem, villageArt, wiki } from './catalog.js';
import { wikiText } from './locale.js';

export const referencePath = wiki + '/reference';
export const referenceGroups = [
  ['Leagues and rewards', [
    { id: 'ranked-leagues', source: 'league_tiers', title: 'Ranked Leagues', detail: 'Weekly medals, rules & loot bonuses', tone: 'crimson', art: 'leagues/league-tier/legend_league_1.png', keywords: 'ranked medals league bonus trophies loot' },
    { id: 'clan-war-leagues', source: 'war_leagues', title: 'Clan War Leagues', detail: 'Placement & bonus medals by tier', tone: 'amber', art: 'resources/league_medals.webp', keywords: 'cwl war size league medals' },
    { id: 'builder-leagues', source: 'builder_leagues', title: 'Builder Base leagues', detail: 'Trophy thresholds across all tiers', tone: 'slate', art: 'leagues/builder-base/wood_league_1.png', keywords: 'builder hall trophies ladder builder leagues' },
    { id: 'capital-leagues', source: 'capital_leagues', title: 'Clan Capital leagues', detail: 'Trophy cutoffs & Raid Weekend XP', tone: 'purple', art: 'leagues/capital-leagues/legend_league.png', keywords: 'raid weekend trophies clan xp capital leagues' },
    { id: 'star-bonus', source: 'star_bonus', title: 'Star Bonus', detail: 'Daily loot & Hero Ore by Town Hall', tone: 'sky', art: 'resources/shiny_ore.webp', keywords: 'loot ores town hall' },
    { id: 'upgrades', source: 'townhall_upgrades', title: 'Upgrade costs', detail: 'Time, builder, lab & resource totals', tone: 'gold', art: 'buildings/home-village/town_hall/level_19.webp', keywords: 'upgrade planner time duration town hall costs walls lab' },
  ]],
  ['Items and rules', [
    { id: 'magic-items', source: 'magic_items', title: 'Magic Items', detail: 'Books, hammers, potions & runes', tone: 'pink', art: 'magic_items/book_of_building.webp', keywords: 'books potions runes hammers storage inventory' },
    { id: 'magic-snacks', source: 'magic_snacks', title: 'Magic Snacks', detail: 'Temporary boosts & consumables', tone: 'orange', art: 'magic_snacks/builder_bite.webp', keywords: 'consumables boosts' },
    { id: 'battle-modes', source: 'battle_modifiers', title: 'Battle Modes', detail: 'Esports & Legend League modifiers', tone: 'red', art: 'icons/Icon_HV_Sword.png', keywords: 'esports hard mode modifiers' },
    { id: 'obstacles', source: 'obstacles', title: 'Obstacles', detail: 'Clear costs, loot & seasonal spawns', tone: 'emerald', art: 'obstacles/home-village/tree.webp', keywords: 'removal clear cost loot trees' },
  ]],
];
export const referenceTopics = referenceGroups.flatMap(([, topics]) => topics);
const leagueFolders = { league_tiers: 'league-tier', war_leagues: 'cwl', builder_leagues: 'builder-base', capital_leagues: 'capital-leagues' };
export const leagueTopicIds = referenceTopics.filter(({ source }) => Object.hasOwn(leagueFolders, source)).map(({ id }) => id);
const retired = new Set(['Training Potion', 'Training Treat']);
const romanNumerals = { i: 1, ii: 2, iii: 3, iv: 4, v: 5 };

function artPath(source, item) {
  const name = stem((item.name || '').replaceAll('\\q', ''));
  if (leagueFolders[source]) {
    const badge = name.replace(/^p_e_k_k_a_/, 'pekka_').replace(/_(i|ii|iii|iv|v)$/, (_, roman) => '_' + romanNumerals[roman]);
    return `leagues/${leagueFolders[source]}/${badge}.png`;
  }
  if (source === 'star_bonus') return villageArt('home', item.townhall_level);
  if (source === 'obstacles') {
    const village = ['builderBase', 'builder'].includes(item.village) ? 'builder-base' : 'home-village';
    return `obstacles/${village}/${name.replace(/[^\w'-]/g, '')}.webp`;
  }
  return `${source}/${name}.webp`;
}

function itemDescription(item, translations) {
  let text = (translations[item.TID?.info] || item.info || '').replaceAll('\\n', '\n');
  // Older snapshots omit some effect values that the description still references.
  if (!Number.isFinite(item.boost_multiplier)) {
    text = text.replaceAll('<num>x as fast', 'faster').replaceAll('to <multiplier>x ', 'above ');
  }
  if (!(item.duration > 0)) text = text.replaceAll('<time>', 'a limited time');
  if (!Number.isFinite(item.battle_uses)) text = text.replaceAll('the next <battles> battles', 'upcoming battles');
  const replacements = { time: duration(item.duration), num: item.boost_multiplier, multiplier: item.boost_multiplier, battles: item.battle_uses };
  return text.replace(/<([a-z]+)>/gi, (_, token) => replacements[token] ?? '—');
}

export function makeReferences(data, translations, byPath, language = 'EN') {
  return new Map(referenceTopics.map((topic) => {
    const key = topic.source;
    const entries = (data[key] || []).filter((item) => item.available !== false && !retired.has(item.name)).map((item) => {
      const translated = language !== 'EN' && translations[item.TID?.name];
      const name = key === 'star_bonus' ? 'Town Hall ' + item.townhall_level : (translated || item.name).replaceAll('\\q', '"');
      const info = itemDescription(item, translations);
      return { ...item, title: name, info, key: String(item._id ?? item.townhall_level ?? item.name), art: byPath.get(artPath(key, item)) };
    });
    return [topic.id, { ...topic, title: wikiText(language, topic.title), entries, art: byPath.get(topic.art), route: referencePath + '/' + topic.id }];
  }));
}

export function referenceMatches(references, term) {
  if (!term) return [];
  return [...references.values()].flatMap((topic) => {
    const title = `${topic.title} ${topic.keywords}`.toLowerCase();
    const names = (entry) => [entry.title, entry.name].filter(Boolean).map((item) => item.toLowerCase());
    const matches = topic.entries.filter((entry) => names(entry).some((name) => name.includes(term)));
    const exact = matches.filter((entry) => names(entry).includes(term));
    if (title.includes(term) || (leagueTopicIds.includes(topic.id) && !exact.length && matches.length > 1)) {
      return [{ ...topic, key: topic.id }];
    }
    return (exact.length ? exact : matches).map((entry) => ({
      key: topic.id + ':' + entry.key,
      title: entry.title,
      art: entry.art || topic.art,
      topic: topic.title,
      route: topic.route + '#' + referenceEntryId(entry),
    }));
  });
}

export const referenceEntryId = (entry) => 'entry-' + entry.key.replace(/[^a-z0-9_-]+/gi, '-').toLowerCase();
