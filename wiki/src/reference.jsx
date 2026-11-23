import { useState } from 'react';
import { ChevronRight } from 'lucide-react';
import { compactNumber, duration, number, resourceArt, titleCase } from './catalog.js';
import { referenceEntryId, referenceGroups, referencePath } from './reference-data.js';
import { Art, AutoLoadResults, Crumbs, Empty, Toggle, TownHallSelect, WikiSelect } from './ui.jsx';
import { TownHallUpgrades } from './upgrades.jsx';

const loot = [['gold', 'Gold'], ['elixir', 'Elixir'], ['dark_elixir', 'Dark Elixir']];
const ore = [['shiny_ore', 'Shiny Ore'], ['glowy_ore', 'Glowy Ore'], ['starry_ore', 'Starry Ore']];
// The exporter writes league and Town Hall rows in progression order.
const highestFirst = (entries) => [...entries].reverse();
const percent = (value) => Number.isFinite(value) ? number(value) + '%' : '—';
const roman = { I: '1', II: '2', III: '3', IV: '4', V: '5' };
const leagueTitle = (title) => title.replace(/\s+League(?=\s|$)/g, '').replace(/\b(I|II|III|IV|V)$/, (part) => roman[part]);

function topicCount(topic) {
  if (!topic) return '';
  if (topic.id === 'upgrades') return 'TH 2–19';
  const count = topic.entries?.length || 0;
  if (!count) return '';
  return count + ' ' + ({ 'ranked-leagues': 'tiers', 'star-bonus': 'halls', 'battle-modes': 'modes', obstacles: 'obstacles', 'magic-items': 'items', 'magic-snacks': 'snacks' }[topic.id] || 'leagues');
}

export function ReferenceLinks({ links, className = '', showDetail = false, showCount = false }) {
  return <ul className={'guide-list ' + className}>{links.map((item) => {
    const { key, art, title, topic, detail, route } = item;
    const count = showCount ? topicCount(item) : '';
    return <li key={key || route}><a href={route}>
      {art && <Art file={art} />}
      <span className="guide-text">
        <strong>{title}</strong>
        {(topic || (showDetail && detail)) && <small>{topic || detail}</small>}
      </span>
      {count && <span className="guide-count">{count}</span>}
      <ChevronRight size={15} className="guide-chevron" aria-hidden="true" />
    </a></li>;
  })}</ul>;
}

function amount(value) {
  return Number.isFinite(value) ? <span title={number(value)}><span aria-hidden="true">{compactNumber(value)}</span><span className="sr-only">{number(value)}</span></span> : '—';
}

function resourceColumns(resources, valueFor) {
  return resources.map(([key, title]) => ({ key, title, art: resourceArt[title], value: (entry) => amount(valueFor(entry)?.[key]) }));
}

function ReferenceTable({ entries, columns, identity = 'League', label, wide = false }) {
  if (!entries.length) return <Empty>No data for this selection.</Empty>;
  return <div className={'table-scroll' + (columns.length <= 2 ? ' narrow' : '')} tabIndex={wide || columns.length > 4 ? 0 : undefined} aria-label={label + ' table'}>
    <table className={'data-table' + (wide ? ' stack-mobile' : '')} aria-label={label} role={wide ? 'table' : undefined}>
      <thead role={wide ? 'rowgroup' : undefined}><tr role={wide ? 'row' : undefined}>
        <th scope="col" role={wide ? 'columnheader' : undefined}>{identity}</th>{columns.map(({ key, title, art, desktopOnly }) => <th key={key} className={'num' + (desktopOnly ? ' desktop-only' : '')} scope="col" role={wide ? 'columnheader' : undefined}>
          <span className="col-head">{art && <Art file={art} />}{title}</span>
        </th>)}</tr></thead>
      <tbody role={wide ? 'rowgroup' : undefined}>{entries.map((entry) => <tr key={entry.key} id={referenceEntryId(entry)} tabIndex={-1} role={wide ? 'row' : undefined}>
        <th scope="row" role={wide ? 'rowheader' : undefined}><span className="row-head">{entry.art && <Art file={entry.art} />}{identity === 'League' ? leagueTitle(entry.title) : entry.title}</span></th>
        {columns.map(({ key, title, value, desktopOnly }) => <td key={key} className={'num' + (desktopOnly ? ' desktop-only' : '')} role={wide ? 'cell' : undefined}>
          {wide && <span className="cell-label" aria-hidden="true">{title}</span>}
          <span className="cell-value">{value(entry)}</span>
        </td>)}
      </tr>)}</tbody>
    </table>
  </div>;
}

function ModeLink({ value, references }) {
  if (!value) return '—';
  const section = references.get('battle-modes');
  const mode = section.entries.find((entry) => entry.name === value);
  return mode ? <a className="inline-link" href={section.route + '#' + referenceEntryId(mode)}>{modeTitle(mode, references)}</a> : value;
}

function referenceView(tabs, url, onUrlChange) {
  const requested = url.searchParams.get('view');
  const view = tabs.some(([id]) => id === requested) ? requested : tabs[0][0];
  const selectView = (value) => {
    const next = new URL(location.href);
    // Returning from a linked battle mode should restore the prior view.
    if (value === tabs[0][0]) next.searchParams.delete('view');
    else next.searchParams.set('view', value);
    next.hash = '';
    history.replaceState(null, '', next);
    onUrlChange(next.pathname + next.search);
  };
  return [view, selectView];
}

function CWL({ entries, references, url, onUrlChange }) {
  const tabs = [['medals', 'Medals'], ['rules', 'Rules']];
  const [view, setView] = referenceView(tabs, url, onUrlChange);
  const [placement, setPlacement] = useState('1');
  const positions = ['1st', '2nd', '3rd', '4th', '5th', '6th', '7th', '8th'];
  const columns = view === 'medals' ? [
    ...positions.map((position, index) => ({
      key: position, title: position, desktopOnly: index + 1 !== Number(placement), value: ({ cwl_medals: medals }) =>
        number(Number.isFinite(medals?.first_place) && Number.isFinite(medals.position_medal_diff) ? medals.first_place - index * medals.position_medal_diff : null),
    })),
    { key: 'bonus', title: 'Bonus', value: ({ cwl_medals }) => number(cwl_medals?.bonus_reward) },
  ] : [
    { key: 'up', title: 'Promoted', value: (entry) => number(entry.promotions) },
    { key: 'down', title: 'Demoted', value: (entry) => number(entry.demotions) },
    { key: 'minimum', title: 'Min. bonuses', value: ({ cwl_medals }) => number(cwl_medals?.minimum_bonus_amount) },
    { key: 'war-size', title: 'War size', value: (entry) => typeof entry['15v15_only'] === 'boolean' ? (entry['15v15_only'] ? '15v15' : '15v15 / 30v30') : '—' },
    { key: 'mode', title: 'Battle mode', value: (entry) => <ModeLink value={entry.battle_modifier} references={references} /> },
  ];
  return <>
    <div className="controls">
      <Toggle label="CWL view" items={tabs} value={view} onChange={setView} />
      {view === 'medals' && <WikiSelect className="cwl-placement" label="Placement" hideLabel value={placement} options={positions.map((position, index) => [index + 1, position + ' place'])} onChange={setPlacement} />}
    </div>
    {view === 'medals' && <p className="note">Placement medals before participation adjustments. Bonus medals are awarded separately.</p>}
    <ReferenceTable entries={highestFirst(entries)} columns={columns} label="Clan War Leagues" wide={view === 'rules'} />
  </>;
}

function RankedPlacement({ rewards }) {
  const ordered = [...rewards].sort((a, b) => a.minimum_rank - b.minimum_rank);
  return <details className="placement">
    <summary>By placement</summary>
    <dl>{ordered.map((reward, index) => {
      const end = ordered[index + 1]?.minimum_rank - 1;
      const start = number(reward.minimum_rank);
      return <div key={reward.minimum_rank}><dt>{Number.isFinite(end) ? (end === reward.minimum_rank ? start : start + '–' + number(end)) : start + '+'}</dt><dd>{number(reward.medals)}</dd></div>;
    })}</dl>
  </details>;
}

function Ranked({ entries, references, url, onUrlChange }) {
  const hasMedals = entries.some(({ ranked_medals }) => Number.isFinite(ranked_medals?.maximum_per_week) || ranked_medals?.placement_rewards?.length > 0);
  const tabs = [...(hasMedals ? [['medals', 'Medals']] : []), ['rules', 'Rules'], ['loot', 'League bonus']];
  const halls = [...new Set(entries.flatMap(({ rewards }) => (rewards || []).map((reward) => reward.townhall_level)))].sort((a, b) => b - a);
  const [view, setView] = referenceView(tabs, url, onUrlChange);
  const [hall, setHall] = useState(String(halls[0] ?? ''));
  const rows = highestFirst(entries);
  const rewardFor = ({ rewards }) => (rewards || []).find((reward) => reward.townhall_level === Number(hall));
  const columns = view === 'medals' ? [
    { key: 'medals', title: 'Weekly medals', art: 'resources/ranked_medals.webp', value: ({ ranked_medals }) =>
      ranked_medals?.placement_rewards?.length ? <RankedPlacement rewards={ranked_medals.placement_rewards} /> : number(ranked_medals?.maximum_per_week) },
  ] : view === 'loot' ? resourceColumns(loot, (entry) => rewardFor(entry)?.resources) : [
    { key: 'battles', title: 'Battles', value: ({ battle_count }) => number(battle_count) },
    { key: 'group', title: 'Group size', value: ({ group_size }) => number(group_size) },
    { key: 'promotion', title: 'Promoted', value: ({ promote_percentage }) => percent(promote_percentage) },
    { key: 'demotion', title: 'Demoted', value: ({ demote_percentage }) => percent(demote_percentage) },
    // The first loot row is usually TH7. The exported cap is the tier-specific limit.
    { key: 'hall', title: 'Max TH', value: ({ townhall_cap }) => number(townhall_cap) },
    { key: 'mode', title: 'Battle mode', value: ({ battle_modifier }) => <ModeLink value={battle_modifier} references={references} /> },
  ];
  return <>
    <div className="controls">
      <Toggle label="Ranked League view" items={tabs} value={view} onChange={setView} />
      {view === 'loot' && halls.length > 0 && <TownHallSelect levels={halls} value={hall} onChange={setHall} />}
    </div>
    {view === 'medals' && <p className="note">Weekly maximums. Legend League 1 medals depend on placement.</p>}
    <ReferenceTable entries={view === 'loot' ? rows.filter(rewardFor) : rows} columns={columns} label="Ranked Leagues" wide={view === 'rules'} />
  </>;
}

function TrophyLeagues({ topic }) {
  const columns = [{ key: 'trophies', title: 'Trophies', value: (entry) => number(entry.trophy_start) }];
  if (topic.source === 'capital_leagues') columns.push({ key: 'xp', title: 'Clan XP', value: (entry) => number(entry.clan_xp) });
  return <ReferenceTable entries={highestFirst(topic.entries)} columns={columns} label={topic.title} />;
}

function StarBonus({ entries }) {
  const [kind, setKind] = useState('loot');
  return <>
    <div className="controls"><Toggle label="Star Bonus reward" items={[['loot', 'Loot'], ['ore', 'Ore']]} value={kind} onChange={setKind} /></div>
    <ReferenceTable entries={highestFirst(entries).map((entry) => ({ ...entry, title: 'TH ' + entry.townhall_level }))} columns={resourceColumns(kind === 'loot' ? loot : ore, (entry) => entry.resources)} identity="Town Hall" label="Star Bonus rewards" />
  </>;
}

const itemTypes = { book: 'Books', hammer: 'Hammers', potion: 'Potions', rune: 'Runes', other: 'Other' };
const getItemType = (entry) => entry.type || Object.keys(itemTypes).find((type) => entry.name.toLowerCase().includes(type)) || 'other';

function ResourceValue({ resource, value }) {
  const art = resourceArt[resource];
  return <span className="resource">{art && <Art file={art} />}{amount(value)}{resource && <span className={art ? 'sr-only' : undefined}>{resource}</span>}</span>;
}

function EntryList({ entries, children }) {
  if (!entries.length) return <Empty>No entries match this filter.</Empty>;
  return <ul className="entry-list">{entries.map((entry) => <li id={referenceEntryId(entry)} key={entry.key} tabIndex={-1}>{children(entry)}</li>)}</ul>;
}

function Items({ entries, snacks }) {
  const [kind, setKind] = useState('all');
  const options = [['all', 'All'], ...[...new Set(entries.map(getItemType))].sort().map((type) => [type, itemTypes[type] || titleCase(type)])];
  const shown = entries.filter((entry) => kind === 'all' || getItemType(entry) === kind).sort((a, b) => a.title.localeCompare(b.title));
  return <>
    {!snacks && <div className="tabs" role="group" aria-label="Item type">
      {options.map(([key, text]) => <button key={key} type="button" aria-pressed={kind === key} onClick={() => setKind(key)}>{text}</button>)}
    </div>}
    <EntryList entries={shown}>{(entry) => {
      const facts = [
        entry.max_inventory != null && ['Holds', number(entry.max_inventory)],
        Number.isFinite(entry.gem_value) && ['Sells for', <ResourceValue resource="Gems" value={entry.gem_value} />],
        entry.duration > 0 && ['Lasts', duration(entry.duration)],
        entry.battle_uses != null && ['Battles', number(entry.battle_uses)],
        Number.isFinite(entry.boost_multiplier) && ['Boost', entry.boost_multiplier + '×'],
        entry.activation_expiry > 0 && ['Use within', duration(entry.activation_expiry)],
      ].filter(Boolean);
      return <div className="entry">
        <span className="entry-art">{entry.art && <Art file={entry.art} />}</span>
        <div className="entry-main">
          <h2>{entry.title}</h2>
          {entry.info && <p>{entry.info}</p>}
          {facts.length > 0 && <dl className="inline-facts">{facts.map(([title, value]) => <div key={title}><dt>{title}</dt><dd>{value}</dd></div>)}</dl>}
        </div>
      </div>;
    }}</EntryList>
  </>;
}

const obstacleVillage = (entry) => ['builder', 'builderBase'].includes(entry.village) ? 'builder' : 'home';

function Obstacles({ entries, target }) {
  const [village, setVillage] = useState('all');
  const options = [['all', 'All villages'], ['home', 'Home Village'], ['builder', 'Builder Base']].filter(([key]) => key === 'all' || entries.some((entry) => obstacleVillage(entry) === key));
  const shown = highestFirst(entries).filter((entry) => village === 'all' || obstacleVillage(entry) === village);
  // Deep links from search must be able to focus an obstacle beyond the first batch.
  const initialLimit = shown.findIndex((entry) => referenceEntryId(entry) === target) + 1;
  return <>
    <div className="controls"><Toggle label="Village" items={options} value={village} onChange={setVillage} /></div>
    <AutoLoadResults count={shown.length} resetKey={village} label="Obstacles" minimum={initialLimit}>{(limit) =>
      <EntryList entries={shown.slice(0, limit)}>{(entry) => <div className="entry compact">
        <span className="entry-art">{entry.art && <Art file={entry.art} />}</span>
        <div className="entry-main">
          <h2>{entry.title}</h2>
          <p>{obstacleVillage(entry) === 'builder' ? 'Builder Base' : 'Home Village'}{entry.width > 0 ? ' · ' + entry.width + ' × ' + entry.width : ''}</p>
        </div>
        <dl className="entry-values">
          {Number.isFinite(entry.clear_cost) && <div><dt>Clear</dt><dd><ResourceValue resource={entry.clear_resource} value={entry.clear_cost} /></dd></div>}
          {Number.isFinite(entry.loot_count) && <div><dt>Loot</dt><dd><ResourceValue resource={entry.loot_resource} value={entry.loot_count} /></dd></div>}
        </dl>
      </div>}</EntryList>
    }</AutoLoadResults>
  </>;
}

function modeTitle(mode, references) {
  if (mode.name === 'Hard Mode') return 'Esports mode';
  const league = references.get('ranked-leagues').entries.find((entry) => entry.battle_modifier === mode.name);
  return league ? leagueTitle(league.title) : mode.title;
}

function Delta({ value }) {
  if (!Number.isFinite(value)) return <span className="delta">—</span>;
  const diff = value - 100;
  return <span className={'delta' + (diff > 0 ? ' up' : diff < 0 ? ' down' : '')}>{diff > 0 ? '+' : diff < 0 ? '−' : ''}{number(Math.abs(diff))}%</span>;
}

function BattleModes({ entries, references }) {
  const affected = { buildings: 'Buildings', heroes: 'Heroes', guardians: 'Guardians', common_equipment: 'Common', epic_equipment: 'Epic' };
  const ruleKey = (rule) => rule.type + ':' + (rule.attacker ? 'attack' : rule.defender ? 'defense' : '');
  const modes = entries.map((mode) => ({
    mode,
    title: modeTitle(mode, references),
    uses: [['ranked-leagues', 'Ranked'], ['clan-war-leagues', 'CWL']].flatMap(([id, prefix]) => {
      const league = references.get(id);
      return league.entries.filter((entry) => entry.battle_modifier === mode.name).map((entry) => ({
        title: prefix + ' ' + leagueTitle(entry.title),
        route: league.route + '?view=rules#' + referenceEntryId(entry),
      }));
    }),
    combat: mode.modifiers.filter((rule) => Number.isFinite(rule.damage_multiplier) || Number.isFinite(rule.hp_multiplier)),
    equipment: mode.modifiers.filter((rule) => Number.isFinite(rule.minus_levels)),
  }));
  const rows = [...new Map(modes.flatMap(({ combat }) => combat).map((rule) => [ruleKey(rule), rule])).values()];
  const hasEquipment = modes.some(({ equipment }) => equipment.length);
  return <>
    <p className="note">Change from standard values while the mode is active.</p>
    <div className="table-scroll modes-scroll" tabIndex={0} aria-label="Battle mode modifiers">
      <table className="data-table modes-table">
        <thead>
          <tr>
            <th scope="col" rowSpan={2} className="mode-corner">Affects</th>
            {modes.map(({ mode, title, uses }) => <th scope="colgroup" colSpan={2} key={mode.key} id={referenceEntryId(mode)} tabIndex={-1} className="mode-head">
              <span className="mode-title">{title}</span>
              <span className="mode-uses">{uses.length ? uses.map((use, index) => <span key={use.route}>{index > 0 && ', '}<a href={use.route}>{use.title}</a></span>) : 'Tournaments & friendly challenges'}</span>
            </th>)}
          </tr>
          <tr>{modes.map(({ mode }) => [<th scope="col" className="num sub-head" key={mode.key + 'd'}>Damage</th>, <th scope="col" className="num sub-head" key={mode.key + 'h'}>HP</th>])}</tr>
        </thead>
        <tbody>
          {rows.map((rule) => <tr key={ruleKey(rule)}>
            <th scope="row">{affected[rule.type] || titleCase(rule.type)}<span className="dim">{rule.attacker ? ' attacking' : rule.defender ? ' defending' : ''}</span></th>
            {modes.map(({ mode, combat }) => {
              const match = combat.find((item) => ruleKey(item) === ruleKey(rule));
              return [<td className="num group-start" key={mode.key + 'd'}><Delta value={match?.damage_multiplier} /></td>, <td className="num" key={mode.key + 'h'}><Delta value={match?.hp_multiplier} /></td>];
            })}
          </tr>)}
          {hasEquipment && <tr>
            <th scope="row">Equipment levels</th>
            {modes.map(({ mode, equipment }) => <td colSpan={2} className="num group-start" key={mode.key}>
              {equipment.length ? equipment.map((rule) => <span className="cap" key={rule.type}>{affected[rule.type] || titleCase(rule.type)} <span className="delta down">−{number(rule.minus_levels)}</span></span>) : <span className="delta">—</span>}
            </td>)}
          </tr>}
        </tbody>
      </table>
    </div>
  </>;
}

export function ReferencePage({ references, topic, url, catalogue, onUrlChange }) {
  const heading = topic?.title || 'Game reference';
  let body;
  if (!topic) body = referenceGroups.map(([title, topics]) => <section className="block" key={title}>
    <div className="block-head"><h2>{title}</h2></div>
    <ReferenceLinks showDetail showCount className="two-col" links={topics.map(({ id }) => references.get(id))} />
  </section>);
  else if (topic.id === 'clan-war-leagues') body = <CWL entries={topic.entries} references={references} url={url} onUrlChange={onUrlChange} />;
  else if (topic.id === 'ranked-leagues') body = <Ranked entries={topic.entries} references={references} url={url} onUrlChange={onUrlChange} />;
  else if (topic.id === 'star-bonus') body = <StarBonus entries={topic.entries} />;
  else if (topic.id === 'upgrades') body = <TownHallUpgrades catalogue={catalogue} url={url} onUrlChange={onUrlChange} />;
  else if (topic.id === 'battle-modes') body = <BattleModes entries={topic.entries} references={references} />;
  else if (topic.id === 'obstacles') body = <Obstacles entries={topic.entries} target={url.hash.slice(1)} />;
  else if (topic.id === 'magic-items' || topic.id === 'magic-snacks') body = <Items entries={topic.entries} snacks={topic.id === 'magic-snacks'} />;
  else body = <TrophyLeagues topic={topic} />;
  return <div className={'reference-page' + (topic ? '' : ' reference-index')}>
    {topic ? <header className="page-head">
      <Crumbs items={[['Game reference', referencePath], [heading]]} />
      <div className="page-title">
        {topic.art && <Art file={topic.art} eager />}
        <div>
          <h1>{heading}</h1>
          {topic.detail && <p>{topic.detail}{topicCount(topic) ? ' · ' + topicCount(topic) : ''}</p>}
        </div>
      </div>
    </header> : <header className="page-head">
      <h1>Game reference</h1>
      <p className="lede">Leagues, rewards, upgrade totals, items and battle rules from the current game data.</p>
    </header>}
    {body}
  </div>;
}
