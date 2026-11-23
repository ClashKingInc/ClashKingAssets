import { useMemo, useState } from 'react';
import { ArrowDownToLine, BarChart3, Check, ChevronDown, Clock3, Copy, FolderOpen, Image } from 'lucide-react';
import { abilityRows, buildStep, categories, duration, formatStat, label, levelMetrics, number, overviewEntries, rowFor, showsHallRequirement, titleCase, villages, wiki } from './catalog.js';
import { copyImage, progressionPoster, saveBlob } from './exports.js';
import { ActionMenu, ActionStatus, Art, Cost, Crumbs, Hall, MenuAction, Toggle, WikiSelect } from './ui.jsx';
import { chartSeries, EntityCharts } from './charts.jsx';
import { EntityMechanics, LinkedItems } from './mechanics.jsx';
import { useWikiText } from './locale.js';

function Section({ title, meta, actions, children, id, className = '' }) {
  const t = useWikiText();
  return <section id={id} className={'block ' + className}>
    <div className="block-head">
      <h2>{t(title)}{meta && <span className="block-meta">{meta}</span>}</h2>
      {actions}
    </div>
    {children}
  </section>;
}

function LevelPicker({ entity, row, onSelect }) {
  const t = useWikiText();
  const level = t('Level');
  if (entity.levels.length === 1) return <span className="level-static">{level} {row.display}</span>;
  return <WikiSelect className="level-select" label={entity.name + ' level'} hideLabel value={row.display}
    selectedText={level + ' ' + row.display + (level === 'Level' ? ' of ' : ' / ') + entity.levels.at(-1).display} onChange={(value) => onSelect(rowFor(entity, value))}
    options={entity.levels.map(({ display }) => [display, level + ' ' + display])} />;
}

const squadAbilityTitles = {
  SquadSplashCapAbility: 'Splash Resistance', SquadInvisibilityAbility: 'Stealthy Start',
  SquadTrapShieldAbility: 'Trap Sense', SquadDeployShieldAbility: 'Shielded Start',
  SquadSharedTargetAbility: 'Focus Attack', SquadBabyDragonRageAbility: 'Tantrum',
};

function LevelSummary({ entity, row, onSelect, metrics, unlocked }) {
  const t = useWikiText();
  const next = entity.levels[entity.levels.indexOf(row) + 1];
  const nextStep = buildStep(entity, next);
  const currentUnlocks = unlocked(row);
  const justUnlocked = entity.village === 'builder-base' && entity.kind === 'troops' && row === entity.levels[0];
  const hallName = entity.village === 'builder-base' ? 'Builder Hall' : 'Town Hall';
  const summaryMetrics = metrics.filter((key) => row[key] != null
    && ((entity._id !== 4000106 && entity._id !== 1000001) || key !== 'dps')
    && (entity.kind !== 'equipment' || key !== 'heal_on_activation'));
  const bars = [];
  const plain = [];
  for (const key of summaryMetrics.slice(0, 6)) {
    const isCombat = ['hitpoints', 'dps'].includes(key);
    const values = entity.levels.map((level) => level[key] || 0);
    const maximum = isCombat && entity.kind !== 'equipment' ? Math.max(...values) : 0;
    if (maximum > 0 && Math.min(...values) !== maximum) bars.push({ key, maximum });
    else plain.push({ key, isCombat });
  }
  const squadSize = entity.squad?.members?.reduce((sum, member) => sum + (member.count || 0), 0) || 0;
  const squadAbility = entity.squad?.levels?.find((step) => step.level === row.level && step.ability);
  const squadUnlock = entity.squad?.levels?.find((step) => step.ability);
  const facts = [
    showsHallRequirement(entity) && !!row.hall && [t(hallName), <Hall village={entity.village} level={row.hall} />],
    ...plain.map(({ key, isCombat }) => [
      entity.kind === 'equipment' && isCombat ? 'Hero ' + label(key).toLowerCase() : t(label(key)),
      (entity.kind === 'equipment' && row[key] > 0 ? '+' : '') + formatStat(key, row[key]),
    ]),
    !bars.length && !plain.length && entity.housing_space > 0 && ['Housing space', number(entity.housing_space)],
    squadSize > 0 && ['Squad size', squadSize + ' troops'],
    (squadAbility || squadUnlock) && ['Ability', squadAbility ? squadAbilityTitles[squadAbility.ability] || label(squadAbility.ability) : 'From level ' + squadUnlock.level],
    row.required_blacksmith_level != null && ['Blacksmith', 'Level ' + row.required_blacksmith_level],
    row.required_hero_tavern_level > 0 && ['Hero Hall', 'Level ' + row.required_hero_tavern_level],
    row.required_pet_house_level > 0 && ['Pet House', 'Level ' + row.required_pet_house_level],
    !justUnlocked && row.required_lab_level != null && [entity.village === 'builder-base' ? 'Star Laboratory' : 'Laboratory', 'Level ' + row.required_lab_level],
    justUnlocked && entity.production_building && [entity.production_building, 'Level ' + entity.production_building_level],
    currentUnlocks.length === 1 && [t('Unlocks'), <a href={currentUnlocks[0].route}><Art file={currentUnlocks[0].art} />{currentUnlocks[0].name}</a>],
  ].filter(Boolean);

  return <aside className="infobox" aria-label="Selected level">
    <LevelPicker entity={entity} row={row} onSelect={onSelect} />
    {bars.length > 0 && <div className="meters">
      {bars.map(({ key, maximum }) => <div className={'meter ' + key} key={key}>
        <div className="meter-row">
          <span>{t(label(key))}</span>
          <span><strong>{formatStat(key, row[key])}</strong><small> / {formatStat(key, maximum)}</small></span>
        </div>
        <div className="meter-track" aria-hidden="true"><i style={{ width: Math.max(2, Math.max(0, row[key]) / maximum * 100) + '%' }} /></div>
        <span className="sr-only">{formatStat(key, row[key])} of {formatStat(key, maximum)} at max level</span>
      </div>)}
    </div>}
    {facts.length > 0 && <dl className="facts">{facts.map(([title, value]) => <div key={title}><dt>{title}</dt><dd>{value}</dd></div>)}</dl>}
    {nextStep ? <div className="next-up">
      <span className="next-up-label">{t('Next upgrade')} · Level {next.display}</span>
      <div className="next-up-values">
        <Cost step={nextStep} />
        {nextStep?.seconds != null && <span className="time"><Clock3 size={13} aria-hidden="true" />{duration(nextStep.seconds)}</span>}
      </div>
    </div> : entity.levels.length > 1 && <p className="next-up maxed">Max level</p>}
  </aside>;
}

function hasCost(step) {
  if (!step || step.cost == null) return false;
  return typeof step.cost === 'object' ? Object.values(step.cost).some((value) => value > 0) : Number.isFinite(step.cost);
}

function Progression({ entity, selected, onSelect, unlocked, children }) {
  const rows = entity.levels;
  const hasArt = rows.some((row) => row.exactArt);
  const showHall = showsHallRequirement(entity);
  const stats = levelMetrics(entity).filter((key) => ((entity._id !== 4000106 && entity._id !== 1000001) || key !== 'dps')
    && new Set(rows.map((row) => row[key]).filter(Number.isFinite)).size > 1).slice(0, 3);
  const showTime = entity.type !== 'Wall' && !['equipment', 'helpers'].includes(entity.kind);
  const entries = overviewEntries(entity, unlocked);
  const table = <div className="table-scroll" tabIndex={0} aria-label="Level data">
    <table className="data-table progression-table">
      <thead><tr><th scope="col">Level</th>
        {showHall && <th scope="col" aria-label={entity.village === 'builder-base' ? 'Builder Hall' : 'Town Hall'}>Hall</th>}
        {stats.map((key) => <th scope="col" className="num" key={key} aria-label={label(key)}>{({ dps: 'DPS', hitpoints: 'HP', housing_space: 'Space', spell_capacity: 'Spells', siege_capacity: 'Siege' })[key] || label(key)}</th>)}
        <th scope="col" className="num" aria-label="Cost to reach">Cost</th>
        {showTime && <th scope="col" className="num">Time</th>}</tr>
      </thead>
      <tbody>{rows.map((row) => {
        const step = buildStep(entity, row);
        return <tr key={row.display} className={row === selected ? 'selected' : undefined}>
          <th scope="row"><button type="button" onClick={() => onSelect(row)} aria-pressed={row === selected}>{row.display}</button></th>
          {showHall && <td><Hall village={entity.village} level={row.hall} /></td>}
          {stats.map((metric) => <td className="num" key={metric}>{formatStat(metric, row[metric])}</td>)}
          <td className="num"><Cost step={step} /></td>{showTime && <td className="num">{duration(step?.seconds)}</td>}
        </tr>;
      })}</tbody>
    </table>
  </div>;
  const overview = hasArt
    ? <div className="level-strip">{entries.map((entry) => {
      const { row } = entry;
      const content = <>
        <span className="level-art">{entry.art ? <Art file={entry.art} /> : null}</span>
        <span className="level-name">{entry.title.replace('Level ', 'Lv ')}{!!entry.hall && <span className="level-hall"><Hall village={entity.village} level={entry.hall} /></span>}</span>
        <span className="level-cost">{hasCost(entry.step) ? <Cost step={entry.step} /> : <span className="dim">Base</span>}</span>
        {entry.unlock && <span className="level-unlock">{entry.unlock.art && <Art file={entry.unlock.art} />}{entry.unlock.name}</span>}
      </>;
      return row
        ? <button type="button" className="level-cell" key={entry.key} aria-label={entry.title + (entry.unlock ? ', unlocks ' + entry.unlock.name : '')} aria-pressed={row === selected} onClick={() => onSelect(row)}>{content}</button>
        : <div className="level-cell" key={entry.key}>{content}</div>;
    })}</div>
    : <div className={'tier-list' + (entries.length <= 4 ? ' few' : '')}>{entries.map((entry) => {
      const { row } = entry;
      const content = <>
        <span className="tier-name">{entry.title}</span>
        <span className="tier-hall">{!!entry.hall && <Hall village={entity.village} level={entry.hall} />}</span>
        <span className="tier-cost">{hasCost(entry.step) ? <Cost step={entry.step} /> : <span className="dim">Base</span>}</span>
        <span className="tier-time">{Number.isFinite(entry.step?.seconds) && entry.step.seconds > 0 ? duration(entry.step.seconds) : ''}</span>
      </>;
      return row
        ? <button type="button" className="tier-row" key={entry.key} aria-pressed={row === selected} onClick={() => onSelect(row)}>{content}</button>
        : <div className="tier-row" key={entry.key}>{content}</div>;
    })}</div>;
  return <>
    <Section id="progression" title="Level overview" meta={rows.length + ' levels'} actions={<OverviewActions entity={entity} entries={entries} />}>
      {overview}
    </Section>
    {children}
    <Section title="Stats & upgrades">{table}</Section>
  </>;
}

function Abilities({ entity, row }) {
  const abilities = abilityRows(entity, row);
  const toggle = abilities.filter((ability) => ['toggle', 'default_mode'].includes(ability.trigger));
  const [mode, setMode] = useState('default_mode');
  if (!abilities.length) return null;
  const effects = toggle.length > 1
    ? [...toggle.filter((ability) => ability.trigger === mode), ...abilities.filter((ability) => !toggle.includes(ability))]
    : abilities;
  const tiers = effects.filter((ability) => ability.current.housing_space_lost_required != null);
  const rest = effects.filter((ability) => !tiers.includes(ability));
  const tierStats = [...new Set(tiers.flatMap((ability) => Object.keys(ability.current)))]
    .filter((key) => key !== 'level' && key !== 'housing_space_lost_required');
  return <Section title="Abilities" id="abilities" actions={toggle.length > 1 ? <Toggle label="Attack mode" value={mode} onChange={setMode} items={[
    ['default_mode', entity._id === 4000106 ? 'Inferno mode' : 'Default mode'], ['toggle', entity._id === 4000106 ? 'Electro mode' : toggle.find((ability) => ability.trigger === 'toggle')?.name || 'Alternate mode'],
  ]} /> : null}>
    <div className="ability-list">
      {tiers.length > 0 && <article className="ability">
        <h3>Ability tiers <span className="dim">· when allies are defeated</span></h3>
        <div className="table-scroll" tabIndex={0} aria-label="Defeat thresholds">
          <table className="data-table"><thead><tr><th scope="col">Tier</th><th scope="col" className="num">Housing lost</th>
            {tierStats.map((key) => <th scope="col" className="num" key={key}>{label(key)}</th>)}
          </tr></thead><tbody>{tiers.map(({ current }, index) => <tr key={current.housing_space_lost_required}>
            <th scope="row">{index + 1}</th><td className="num">{number(current.housing_space_lost_required)}</td>
            {tierStats.map((key) => <td className="num" key={key}>{formatStat(key, current[key], 'ability')}</td>)}
          </tr>)}</tbody></table>
        </div>
      </article>}
      {rest.map((ability, index) => {
        const trigger = ({ activated: 'On activation', on_deploy: 'On deployment', default_mode: 'Default attack', toggle: 'Alternate attack' })[ability.trigger]
          || (ability.trigger ? titleCase(ability.trigger) : 'Passive');
        const name = ability.trigger === 'default_mode' && entity._id === 4000106 ? 'Inferno mode' : ability.name;
        const stats = Object.entries(ability.current).filter(([key]) => !['level', 'housing_space_lost_required', 'heal_on_activation'].includes(key));
        return <article className="ability" key={ability.name + index}>
          <h3>{name} <span className="dim">· {trigger}</span></h3>
          {ability.info && ability.info !== entity.description && <p>{ability.info}</p>}
          {stats.length > 0 && <dl className="spec">{stats.map(([key, value]) =>
            <div key={key}><dt>{label(key)}</dt><dd>{formatStat(key, value, 'ability')}</dd></div>)}</dl>}
        </article>;
      })}
    </div>
  </Section>;
}

function SeasonalDefenses({ defenses }) {
  const [index, setIndex] = useState(0);
  const defense = defenses[index];
  return <Section title="Crafted defenses">
    <Toggle items={defenses.map((item, key) => [key, item.name, item.image?.path])} value={index} label="Crafted defense" onChange={setIndex} />
    <div className="media-row"><Art file={defense.image} /><div><h3>{defense.name}</h3><p>{defense.info}</p></div></div>
    <div className="module-list">{defense.modules.map((module) =>
      <details key={module._id}><summary><span>{module.name}</span><ChevronDown size={16} /></summary>
        <div className="table-scroll" tabIndex={0} aria-label={module.name}>
          <table className="data-table"><thead><tr><th scope="col">Level</th><th scope="col">Town Hall</th><th scope="col" className="num">Cost</th><th scope="col" className="num">Time</th><th scope="col">Effect</th></tr></thead>
            <tbody>{module.levels.map((level) => <tr key={level.level}><th scope="row">{level.level}</th><td><Hall village="home" level={level.required_townhall} /></td>
              <td className="num"><Cost step={{ cost: level.build_cost, resource: module.upgrade_resource }} /></td><td className="num">{duration(level.build_time)}</td>
              <td>{Object.entries(level.ability_data || {}).map(([key, value]) => label(key) + ' ' + formatStat(key, value, 'ability')).join(' · ') || 'Unavailable'}</td></tr>)}</tbody>
          </table>
        </div>
      </details>)}</div>
  </Section>;
}

function Variants({ catalogue, row }) {
  const [selected, setSelected] = useState(row.variants[0]._id);
  const current = row.variants.find((variant) => variant._id === selected);
  const ability = catalogue.abilityById.get(current._id)?.[0];
  const variantArt = {
    119000002: 'buildings/home-village/town_hall/level_19_barbarian.webp',
    119000001: 'buildings/home-village/town_hall/level_19_archer.webp',
    119000000: 'buildings/home-village/town_hall/level_19_wizard.webp',
  };
  return <Section title="Town Hall variants">
    <Toggle value={selected} items={row.variants.map((variant) => [variant._id, variant.name])} label="Town Hall variant" onChange={setSelected} />
    <div className="media-row">{variantArt[selected] && <Art file={variantArt[selected]} />}<div><h3>{current.name}</h3><p>{current.info}</p>
      {current.required_troop && <p>{catalogue.entitiesByKey.get('troops:' + current.required_troop._id)?.name} level {current.required_troop.level} required</p>}
      {ability && <p>{ability.name}{ability.info ? ' · ' + ability.info : ''}</p>}
    </div></div>
  </Section>;
}

function OverviewActions({ entity, entries }) {
  const [status, setStatus] = useState('');
  const [pending, setPending] = useState(false);
  const copied = status === 'Image copied';
  const filename = entity.route.split('/').at(-1) + '-levels.png';
  const exportGraphic = async (copy) => {
    setPending(true);
    setStatus('');
    try {
      const image = () => progressionPoster(entity, entries);
      if (copy) {
        await copyImage(image);
        setStatus('Image copied');
      } else {
        saveBlob(await image(), filename);
      }
    } catch {
      setStatus(copy ? 'Could not copy the image' : 'Could not create the graphic');
    } finally {
      setPending(false);
    }
  };
  return <div className="overview-actions">
    <ActionMenu label={copied ? 'Copied' : 'Graphic'} icon={copied ? Check : Image} busy={pending}>
      <MenuAction disabled={pending} onSelect={() => exportGraphic(true)}><Copy size={16} />Copy image</MenuAction>
      <MenuAction disabled={pending} onSelect={() => exportGraphic(false)}><ArrowDownToLine size={16} />Download PNG</MenuAction>
    </ActionMenu>
    <span className="sr-only" role="status">{copied ? status : ''}</span>
    <ActionStatus>{copied ? '' : status}</ActionStatus>
  </div>;
}

function SquadMembers({ squad, catalogue, row }) {
  const members = squad.members.flatMap(({ troop_id, count }) => {
    const troop = catalogue.entitiesByKey.get('troops:' + troop_id);
    return troop ? [{ ...troop, name: count + ' × ' + troop.name, key: troop.key + '-' + count }] : [];
  });
  const ability = squad.levels.find((step) => step.level === row?.level && step.ability);
  const unlock = squad.levels.find((step) => step.ability);
  const shown = ability || unlock;
  const facts = ability && [
    Number.isFinite(ability.duration_ms) && duration(ability.duration_ms / 1000),
    Number.isFinite(ability.splash_damage_cap) && 'Splash cap ' + number(ability.splash_damage_cap),
    Number.isFinite(ability.damage_reduction_percent) && ability.damage_reduction_percent + '% less damage',
    Number.isFinite(ability.trap_damage_reduction_percent) && ability.trap_damage_reduction_percent + '% less trap damage',
    Number.isFinite(ability.damage_increase_percent) && '+' + ability.damage_increase_percent + '% damage',
    Number.isFinite(ability.attack_speed_increase_percent) && '+' + ability.attack_speed_increase_percent + '% attack speed',
  ].filter(Boolean);
  return <section className="block">
    <div className="block-head"><h2>Squad</h2></div>
    <LinkedItems items={members} />
    {squad.uses_player_troop_levels && <p className="note">Members use your own troop levels.</p>}
    {shown && <p className="squad-ability"><strong>{squadAbilityTitles[shown.ability] || label(shown.ability)}</strong>
      <span className="dim"> · {ability ? (facts.length ? facts.join(' · ') : 'Active at this level') : 'Unlocks at level ' + unlock.level}</span></p>}
  </section>;
}

export function EntityPage({ catalogue, entity, url }) {
  const t = useWikiText();
  const oldLevel = new URLSearchParams(url.hash.slice(1)).get('level');
  const [selection, setSelection] = useState(() => url.searchParams.get('level') || oldLevel);
  const row = rowFor(entity, selection);
  const isPortrait = !row?.exactArt && !!entity.portrait;
  const art = row?.exactArt || entity.portrait || row?.image || entity.art || entity.modules[0]?.image;
  const short = entity._id === 1000106
    ? 'Trains squads that deploy together. Each level unlocks a new squad.'
    : entity._id === 4000106 ? 'Switch between a ramping Inferno beam and a chain lightning attack.' : entity.description;
  const metrics = levelMetrics(entity);
  const series = useMemo(() => chartSeries(entity), [entity]);
  const unlocked = (row) => catalogue.unlocksFor(entity, row);
  const changeLevel = (level) => {
    setSelection(level.display);
    const next = new URL(location.href);
    next.searchParams.set('level', level.display);
    if (next.hash.startsWith('#level=')) next.hash = '';
    history.replaceState(null, '', next);
  };
  const levelQuery = row ? '?level=' + row.display : '';
  const assetsLink = entity.route + '/assets' + levelQuery;
  const assetCount = entity.files.length + entity.soundEffects.length;
  const browseCategory = entity.isSquad ? 'squads' : entity.category;
  const hero = entity.hero && catalogue.entities.find((item) => item.kind === 'heroes' && item.sourceName === entity.hero);
  const facility = entity.kind === 'guardians' ? catalogue.entitiesByKey.get('buildings:1000105')
    : entity.kind === 'helpers' ? catalogue.entitiesByKey.get('buildings:1000093') : null;
  const related = hero || facility;
  return <article className={'entity-page' + (row ? '' : ' no-aside')}>
    <header className="entity-header">
      <Crumbs items={[[t(villages[entity.village]), entity.village === 'home' ? '/' : wiki + '/' + entity.village], [t(categories[browseCategory][0]), wiki + '/' + entity.village + '/' + browseCategory], [entity.name]]} />
      <div className="entity-title">
        <div className={'entity-art' + (isPortrait ? ' portrait' : '')}>
          <Art file={art} alt={entity.name + (row?.exactArt ? ' level ' + row.display : '')} eager />
        </div>
        <div className="entity-heading">
          <h1 dir="auto">{entity.name}</h1>
          {(entity.rarity || related) && <p className="entity-kicker">
            {entity.rarity && <span className={entity.rarity === 'Epic' ? 'epic-text' : undefined}>{entity.rarity}</span>}
            {entity.rarity && related && <span aria-hidden="true"> · </span>}
            {related && <a href={related.route}>{related.name}</a>}
          </p>}
          {short && <p className="entity-desc" dir="auto">{short}</p>}
          {(series.size > 0 || assetCount > 0) && <div className="entity-links">
            {assetCount > 0 && <a href={assetsLink}><FolderOpen size={15} aria-hidden="true" />Assets<span className="dim">{assetCount}</span></a>}
            {series.size > 0 && <a href="#charts"><BarChart3 size={15} aria-hidden="true" />{t('Charts')}</a>}
          </div>}
        </div>
      </div>
    </header>
    {row && <LevelSummary entity={entity} row={row} onSelect={changeLevel} metrics={metrics} unlocked={unlocked} />}
    <div className="entity-body">
      {entity.squad && <SquadMembers squad={entity.squad} catalogue={catalogue} row={row} />}
      {row && !entity.squad && entity.abilities.length > 0 && <Abilities entity={entity} row={row} />}
      {entity.modules.length > 0 && <SeasonalDefenses defenses={entity.modules} />}
      {row?.variants?.length > 0 && <Variants key={row.level} catalogue={catalogue} row={row} />}
      <EntityMechanics entity={entity} catalogue={catalogue} row={row} unlocks={row ? unlocked(row) : []} />
      {row && entity.levels.length > 1 && <Progression entity={entity} selected={row} onSelect={changeLevel} unlocked={unlocked}>
        <EntityCharts series={series} level={row.display} initialMetric={url.searchParams.get('stat')} onSelect={(level) => changeLevel(rowFor(entity, level))} />
      </Progression>}
    </div>
  </article>;
}
