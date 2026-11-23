export const wiki = '/wiki';
export const assetOrigin = (import.meta.env?.VITE_ASSET_ORIGIN || '/media').replace(/\/$/, '');
export const assetUrl = (path) => assetOrigin + '/' + path.split('/').map(encodeURIComponent).join('/');
export const cdnUrl = (path) => 'https://assets.clashk.ing/' + path.split('/').map(encodeURIComponent).join('/');
export const villages = {
  home: 'Home Village',
  'builder-base': 'Builder Base',
  'clan-capital': 'Clan Capital',
};

export const categories = {
  buildings: ['Buildings', 'buildings/home-village/town_hall/level_12.webp'],
  defenses: ['Defenses', 'buildings/home-village/cannon/level_12.webp'],
  troops: ['Troops', 'troops/barbarian/icon.webp'],
  squads: ['Squads', 'troops/barch_squad/icon.webp'],
  heroes: ['Heroes', 'heroes/archer_queen/icon.webp'],
  equipment: ['Equipment', 'equipment/giant_gauntlet/icon.webp'],
  spells: ['Spells', 'spells/rage_spell/icon.webp'],
  pets: ['Pets', 'pets/unicorn/icon.webp'],
  traps: ['Traps', 'traps/home-village/bomb/level_3.webp'],
  guardians: ['Guardians', 'guardians/longshot/icon.webp'],
  helpers: ['Helpers', "helpers/builder's_apprentice.webp"],
};

const names = {
  dps: 'Damage per second',
  dps_stage_2: 'Stage 2 DPS',
  dps_stage_3: 'Stage 3 DPS',
  hitpoints: 'Hitpoints',
  housing_space: 'Housing space',
  spell_capacity: 'Spell capacity',
  siege_capacity: 'Siege capacity',
  production_rate: 'Production',
  storage_capacity: 'Storage',
  health_recovery_percent: 'Health recovered',
  healing_per_second: 'Healing per second',
  damage_increase_percent: 'Damage increase',
  health_increase_percent: 'Health increase',
  units_spawned: 'Units summoned',
  heal_on_activation: 'Healing on activation',
  housing_space_lost_required: 'Housing space lost',
  required_townhall: 'Town Hall',
  duration: 'Duration',
  damage_radius: 'Damage radius',
  attack_range: 'Range',
  chain_targets: 'Chain targets',
  damage_loss_per_chain_percent: 'Damage lost per chain',
  attack_speed: 'Attack interval',
  attack_speed_increase_percent: 'Attack speed increase',
  movement_speed_increase: 'Movement speed bonus',
  spawned_unit: 'Summoned troop',
  spawned_unit_duration: 'Summon duration',
  spawned_unit_damage_increase_percent: 'Summon damage bonus',
  spawned_unit_movement_speed_increase: 'Summon movement speed bonus',
  stage_2_after: 'Time to stage 2',
  stage_3_after: 'Time to stage 3',
  HealthOverride: 'Hitpoints',
};

export const label = (key) => names[key] || key.replace(/([a-z])([A-Z])/g, '$1 $2')
  .replaceAll('_', ' ').replace(/^./, (first) => first.toUpperCase());
const formatter = new Intl.NumberFormat('en-US', { maximumFractionDigits: 3 });
export const number = (value) => typeof value === 'number' ? formatter.format(value) : '—';
const compactFormatter = new Intl.NumberFormat('en-US', { notation: 'compact', maximumFractionDigits: 1 });
export const compactNumber = (value) => compactFormatter.format(value).replace(/([KMBT])$/, '\u202f$1');
export const titleCase = (value) => value.replaceAll('_', ' ').replaceAll('-', ' ').replace(/\b\w/g, (c) => c.toUpperCase());
export const stem = (value) => value.toLowerCase().trim().replace(/[.?’]/g, '').replace(/\s+/g, '_');
const slug = (value) => value.toLowerCase().normalize('NFKD').replace(/['’.]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');

export const resourceArt = {
  Gold: 'resources/gold.webp',
  Elixir: 'resources/elixir.webp',
  'Dark Elixir': 'resources/dark_elixir.webp',
  'Builder Elixir': 'resources/builder_elixir.webp',
  'Builder Gold': 'resources/builder_gold.webp',
  Gems: 'resources/gems.webp',
  'Shiny Ore': 'resources/shiny_ore.webp',
  'Glowy Ore': 'resources/glowy_ore.webp',
  'Starry Ore': 'resources/starry_ore.webp',
};

export function duration(seconds) {
  if (seconds == null) return '—';
  if (seconds === 0) return 'Instant';
  const parts = [[86400, 'd'], [3600, 'h'], [60, 'm'], [1, 's']];
  return parts.flatMap(([unit, suffix]) => {
    const count = Math.floor(seconds / unit);
    seconds %= unit;
    return count ? [count + suffix] : [];
  }).join(' ');
}

export function costParts(cost, resource) {
  if (cost && typeof cost === 'object') {
    return Object.entries(cost).filter(([, value]) => value > 0).map(([key, value]) => [titleCase(key), value]);
  }
  return Number.isFinite(cost) ? [[resource || '', cost]] : [];
}

export function villageArt(village, level) {
  if (!level) return '';
  return village === 'builder-base'
    ? 'buildings/builder-base/builder_hall/level_' + level + '.webp'
    : 'buildings/home-village/town_hall/level_' + level + '.webp';
}

export function hallLabel(village, level) {
  return level ? (village === 'builder-base' ? 'BH ' : 'TH ') + level : '—';
}

function entityAssets(source, kind, village, byPath, folders) {
  const key = ['spells', 'equipment'].includes(kind)
    ? source.name.toLowerCase().replaceAll(' ', '_').replace(/[.?\u2019]/g, '').replaceAll('\\q', '')
    : stem(source.name);
  const folder = village === 'builder-base' ? 'builder-base' : 'home-village';
  const roots = ['buildings', 'traps', 'decorations', 'obstacles'].includes(kind)
    ? [kind + '/' + folder + '/' + key]
    : [kind + '/' + key];
  const explicit = [source.image, source.icon, source.thumbnail].filter((path) => typeof path === 'string');
  const entries = roots.flatMap((root) => [
    byPath.get(root + '.webp'),
    ...(folders.get(root) || []).filter((asset) => !asset.animated || !asset.path.endsWith('.gif')),
  ]);
  return [...new Map([...explicit.map((path) => byPath.get(path)), ...entries]
    .filter(Boolean).map((asset) => [asset.path, asset])).values()];
}

const cosmeticKinds = ['decorations', 'skins', 'sceneries', 'obstacles'];
const sourceKinds = ['buildings', 'traps', 'troops', 'heroes', 'equipment', 'pets', 'spells', 'guardians', 'helpers', ...cosmeticKinds];

export function entityAssetFiles(entity) {
  return [...new Map([...entity.files, ...entity.soundEffects, entity.track].filter(Boolean).map((file) => [file.path, file])).values()];
}

export function makeCatalogue(data, manifest, abilities, translations, audio = [], soundPaths = {}) {
  const sort = new Intl.Collator('en', { numeric: true });
  const images = Object.entries(manifest.assets).flatMap(([group, items]) => items.map((item) => ({ ...item, group })));
  const tracks = new Map(audio.map((item) => [item.path, item]));
  const files = [...images, ...tracks.values()];
  const byPath = new Map(files.map((file) => [file.path, file]));
  const folders = new Map();
  for (const file of images) {
    const folder = file.path.slice(0, file.path.lastIndexOf('/'));
    if (!folders.has(folder)) folders.set(folder, []);
    folders.get(folder).push(file);
  }
  const abilityById = new Map(abilities.items.map((item) => [item._id, item.abilities]));
  const builderGate = (name, level) => data.buildings.find((building) => building.name === name && building.village === 'builderBase')
    ?.levels.find((entry) => entry.level === level)?.required_townhall;
  const entities = sourceKinds.flatMap((kind) => (data[kind] || []).map((source) => {
    const key = kind + ':' + source._id;
    const village = ['skins', 'sceneries'].includes(kind) ? 'cosmetics' : {
      builderBase: 'builder-base', clanCapital: 'clan-capital',
    }[source.village] || 'home';
    const category = kind === 'buildings' && ['Defense', 'Wall'].includes(source.type) ? 'defenses' : kind;
    const seasonArt = (source.seasonal_defenses || []).flatMap((defense) =>
      folders.get('buildings/seasonal-defense/' + stem(defense.name)) || []);
    const files = [...entityAssets(source, kind, village, byPath, folders), ...seasonArt].sort((a, b) => sort.compare(a.path, b.path));
    const defaultArt = files.find((file) => file.path.endsWith('/icon.webp')) || files[0];
    const baseLevels = source.levels || [];
    const ownerHall = kind === 'equipment' ? data.heroes.find((hero) => hero.name === source.hero)?.levels[0]?.required_townhall || 0 : 0;
    // The exporter numbers partial Builder Base series from 1. Supercell's
    // Builder Base support table lists their live starting levels (cap 20).
    const offset = village === 'builder-base' && kind === 'troops' && baseLevels[0]?.level === 1
      ? Math.max(0, 20 - (baseLevels.at(-1)?.level || 20))
      : source._id === 28000005 && baseLevels[0]?.level === 1 ? 14 : 0;
    const levels = baseLevels.map((row) => {
      const exactArt = files.find((asset) => asset.path.endsWith('/level_' + row.level + '.webp'));
      // Builder troop rows can store their lab gate plus two in required_townhall.
      // Resolve both building gates against the Builder Base tables instead.
      const hall = village === 'builder-base' && kind === 'troops'
        ? Math.max(builderGate(source.production_building, source.production_building_level) || 0,
          row === baseLevels[0] ? 0 : builderGate('Star Laboratory', row.required_lab_level) || 0)
        : ownerHall ? Math.max(row.required_townhall || 0, ownerHall) : row.required_townhall;
      return { ...row, display: row.level + offset, hall, exactArt, image: exactArt || defaultArt };
    });
    return {
      ...source, key, kind, village, category, levels, files,
      assetOnly: cosmeticKinds.includes(kind) || source._id === 1000083,
      isSquad: kind === 'troops' && (Boolean(source.squad) || source.production_building === 'Squad Barracks'),
      sourceName: source.name,
      portrait: files.find(({ path }) => path.endsWith('/portrait.webp')),
      art: levels.at(-1)?.image || defaultArt,
      name: translations[source.TID?.name] || source.name,
      description: (translations[source.TID?.info] || source.info)?.replaceAll('\\n', '\n'),
      track: tracks.get(source.music),
      soundEffects: (soundPaths[key] || []).map((path) => tracks.get(path)).filter(Boolean),
      route: wiki + '/' + village + '/' + category + '/' + source._id + '/' + slug(source.name),
      abilities: (abilityById.get(source._id) || []).map((ability) => ({
        ...ability,
        name: translations[ability.TID?.name] || ability.name,
        info: (translations[ability.TID?.info] || ability.info)?.replaceAll('\\n', '\n'),
      })),
      modules: (source.seasonal_defenses || []).map((defense) => ({
        ...defense,
        image: byPath.get('buildings/seasonal-defense/' + stem(defense.name) + '/level_1.webp'),
      })),
    };
  })).filter((entity) => typeof entity.name === 'string' && entity._id != null);
  const entitiesByKey = new Map(entities.map((entity) => [entity.key, entity]));
  const ownerByPath = new Map();
  const itemSoundPaths = new Set(Object.values(soundPaths).flat());
  const unlocks = new Map();
  for (const entity of entities) {
    for (const file of entity.files) ownerByPath.set(file.path, entity);
    for (const clip of entity.soundEffects) {
      if (!ownerByPath.has(clip.path)) ownerByPath.set(clip.path, entity);
    }
    if (entity.track) ownerByPath.set(entity.track.path, entity);
    // Production gates on Epic gear and Super Troops describe availability, not unlocks.
    if (entity.assetOnly || entity.super_troop || entity.is_seasonal || entity.is_starter || entity.rarity === 'Epic' || !entity.production_building) continue;
    const key = [entity.village, entity.production_building, entity.production_building_level].join(':');
    if (!unlocks.has(key)) unlocks.set(key, []);
    unlocks.get(key).push(entity);
  }
  return {
    files, byPath, entities: entities.filter((entity) => !entity.assetOnly), entitiesByKey, ownerByPath, itemSoundPaths,
    groups: [...Object.keys(manifest.assets), ...new Set(audio.map(({ group }) => group))], abilityById,
    unlocksFor(entity, row) {
      return unlocks.get([entity.village, entity.sourceName, row.level].join(':')) || [];
    },
  };
}

export function rowFor(entity, candidate, fallback = entity.levels[0]) {
  return entity.levels.find((row) => row.display === Number(candidate)) || fallback;
}

export function buildStep(entity, row) {
  if (!row) return null;
  if (entity.kind === 'buildings' || entity.kind === 'traps') {
    return { cost: row.build_cost, seconds: row.build_time, resource: entity.upgrade_resource };
  }
  if (entity.kind === 'helpers') return { cost: row.upgrade_cost, resource: entity.upgrade_resource };
  const previous = entity.levels[entity.levels.indexOf(row) - 1];
  return previous && { cost: previous.upgrade_cost, seconds: previous.upgrade_time, resource: entity.upgrade_resource };
}

export function showsHallRequirement(entity) {
  return entity._id !== 1000001 && entity._id !== 1000034;
}

export function overviewEntries(entity, unlocked) {
  const rows = entity.levels;
  const hallFor = (row) => showsHallRequirement(entity) ? row.hall : undefined;
  const singleLevels = rows.map((row) => {
    const troops = unlocked(row).filter((item) => item.kind === 'troops');
    return {
      key: row.display, row, title: 'Level ' + row.display, art: row.exactArt, hall: hallFor(row), step: buildStep(entity, row),
      unlock: troops.length === 1 ? troops[0] : undefined,
    };
  });
  if (singleLevels.some((entry) => entry.art || entry.unlock)) return singleLevels;

  const groups = [];
  for (const row of rows) {
    const hall = hallFor(row);
    const gate = [hall, row.required_blacksmith_level].join(':');
    // Retain Blacksmith boundaries for ore totals when several tiers share a hall.
    if (groups.at(-1)?.gate !== gate) groups.push({ gate, hall, rows: [] });
    groups.at(-1).rows.push(row);
  }
  if (groups.length === 1) return singleLevels;

  return groups.map((group) => {
    const first = group.rows[0];
    const last = group.rows.at(-1);
    const resources = new Map();
    const steps = group.rows.map((row) => buildStep(entity, row)).filter(Boolean);
    const seconds = steps.length && steps.every((step) => Number.isFinite(step.seconds))
      ? steps.reduce((total, step) => total + step.seconds, 0)
      : undefined;
    for (const step of steps) {
      for (const [name, value] of costParts(step.cost, step.resource)) {
        resources.set(name, (resources.get(name) || 0) + value);
      }
    }
    return {
      key: first.display, row: first === last ? first : null, hall: group.hall,
      title: first === last ? 'Level ' + first.display : 'Levels ' + first.display + '–' + last.display,
      step: { cost: Object.fromEntries(resources), seconds },
    };
  });
}

export function levelMetrics(entity) {
  const keys = ['housing_space', 'spell_capacity', 'siege_capacity', 'production_rate', 'storage_capacity', 'health_recovery_percent', 'capacity', 'hitpoints', 'dps', 'damage', 'duration', 'radius', 'heal_on_activation', 'healing', 'boost_time_seconds', 'damage_boost_percent'];
  return keys.filter((key) => entity.levels.some((row) => typeof row[key] === 'number' && row[key] !== 0));
}

export const formatTiles = (value) => number(value) + (value === 1 ? ' tile' : ' tiles');

export function formatStat(key, value, context = 'level') {
  if (value == null) return '—';
  if (Array.isArray(value)) return value.map((entry) => formatStat(key, entry, context)).join(', ');
  if (typeof value === 'object') return value.name || '—';
  if (typeof value !== 'number') return String(value);
  if (key.endsWith('_percent')) return number(value) + '%';
  if (key === 'attack_speed') return number(value / 1000) + 's';
  if (key === 'production_rate') return number(value) + '/h';
  if (context === 'ability' && /(?:movement_speed|movement_speed_increase)$/.test(key)) return number(value / 100) + ' tiles/s';
  if (key === 'duration' || key.endsWith('_after') || key === 'cooldown' || key === 'spawned_unit_duration') return number(value) + 's';
  if (context === 'ability' && /(?:^|_)(radius|range)$/.test(key)) return formatTiles(value / 100);
  if (key === 'radius') return formatTiles(value);
  return number(value);
}

export function abilityRows(entity, row) {
  const summaries = entity.abilities.flatMap((ability) => {
    const current = ability.levels.find((entry) => entry.level === row?.level);
    return current ? [{ ...ability, current }] : [];
  });
  return summaries.sort((a, b) => (a.current.housing_space_lost_required ?? Infinity) - (b.current.housing_space_lost_required ?? Infinity));
}
