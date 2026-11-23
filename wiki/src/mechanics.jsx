import { Art, Cost, Hall } from './ui.jsx';
import { abilityRows, duration, formatTiles, number } from './catalog.js';

export function LinkedItems({ items }) {
  return <ul className="link-list">{items.map((item) => <li key={item.key || item.route}>
    <a href={item.route}>
      {item.art && <Art file={item.art} />}
      <span>{item.name}</span>
      {item.badge && <small>{item.badge}</small>}
    </a>
  </li>)}</ul>;
}

function Spec({ rows }) {
  return <dl className="spec">{rows.map(([title, value]) => <div key={title}><dt>{title}</dt><dd>{value}</dd></div>)}</dl>;
}

function Range({ entity, row }) {
  const spell = entity.kind === 'spells';
  const trap = entity.kind === 'traps';
  const attack = (row?.attack_range ?? entity.attack_range ?? 0) / 100;
  const trigger = (entity.trigger_radius || 0) / 100;
  const damage = (entity.damage_radius || 0) / 100;
  const effect = (row?.effect_range || 0) / 100;
  const ability = entity.kind === 'equipment' && abilityRows(entity, row).map(({ current }) => current.aura_radius || current.damage_radius).find((radius) => radius > 0);
  const primary = spell ? row?.radius : ability ? ability / 100 : attack || effect;
  const range = trap ? Math.max(trigger, damage) : Math.max(primary || 0, effect);
  if (!(range > 0) || !(spell || trap || ability || entity.category === 'defenses' || entity._id === 1000014)) return null;
  const blind = (row?.min_range ?? entity.min_range ?? 0) / 100;
  const inner = trap ? Math.min(trigger, damage) : blind || (attack && effect && attack !== effect ? Math.min(attack, effect) : 0);
  const cone = entity.cone_angle > 0 && entity.cone_angle < 360 && !blind ? entity.cone_angle : 0;
  const size = Math.ceil(range) * 2 + 2;
  const kind = spell ? 'Spell radius' : trap ? 'Trap radius' : ability ? 'Ability radius'
    : entity._id === 1000014 ? 'Activation radius' : attack ? 'Attack range' : 'Effect radius';
  const facts = (trap ? [['Trigger radius', trigger], ['Damage radius', damage]] : [[kind, primary], ['Blind spot', blind], ['Effect radius', attack ? effect : 0]])
    .filter(([, value]) => value > 0).map(([title, value]) => [title, formatTiles(value)]);
  if (cone) facts.push(['Arc', number(cone) + '°']);
  return <section className="block range-block" aria-labelledby="entity-range-title">
    <div className="block-head"><h2 id="entity-range-title">{kind}</h2></div>
    <div className="range-layout">
      <div className={'range-map' + (cone ? ' with-cone' : '')} aria-hidden="true" style={{ '--grid': size, '--outer': (2 * range / size * 100) + '%', '--inner': (2 * inner / size * 100) + '%', '--blind': (blind / range * 100) + '%', '--cone': cone + 'deg' }}>
        <span className="range-outer" />{inner > 0 && <span className="range-inner" />}
        <Art file={row?.image || entity.art} />
      </div>
      <Spec rows={facts} />
    </div>
  </section>;
}

function WallAvailability({ entity, catalogue }) {
  const hall = catalogue.entitiesByKey.get(entity.village === 'builder-base' ? 'buildings:1000034' : 'buildings:1000001');
  let total = 0;
  const rows = (hall?.levels || []).flatMap(({ level, unlocks }) => {
    const delta = unlocks?.find((item) => item._id === entity._id)?.quantity || 0;
    total += delta;
    return delta ? [{ level, delta, total }] : [];
  });
  if (!rows.length) return null;
  return <section className="block">
    <div className="block-head"><h2>Wall pieces</h2></div>
    <div className="table-scroll"><table className="data-table"><thead><tr>
      <th scope="col">{hall.name}</th><th scope="col" className="num">New pieces</th><th scope="col" className="num">Total pieces</th>
    </tr></thead><tbody>{rows.map(({ level, delta, total }) => <tr key={level}><th scope="row"><Hall level={level} village={entity.village} /></th><td className="num">+{number(delta)}</td><td className="num">{number(total)}</td></tr>)}</tbody></table></div>
  </section>;
}

function Supercharges({ entity }) {
  const base = entity.levels.find((row) => row.supercharge?.levels?.length);
  if (!base) return null;
  const charge = base.supercharge;
  return <section className="block">
    <div className="block-head"><h2>Supercharges<span className="block-meta">From level {base.display} · TH {Math.max(base.hall || 0, charge.required_townhall || 0)}</span></h2></div>
    <div className="table-scroll"><table className="data-table"><thead><tr>
      <th scope="col">Level</th><th scope="col">Bonus</th><th scope="col" className="num">Cost</th><th scope="col" className="num">Time</th>
    </tr></thead><tbody>{charge.levels.map((step) => <tr key={step.level}>
      <th scope="row">{step.level}</th><td>{[step.hitpoints_buff > 0 && '+' + number(step.hitpoints_buff) + ' HP', step.dps_buff > 0 && '+' + number(step.dps_buff) + ' DPS'].filter(Boolean).join(' · ') || 'Unavailable'}</td>
      <td className="num"><Cost step={{ cost: step.build_cost, resource: charge.upgrade_resource }} /></td><td className="num">{duration(step.build_time)}</td>
    </tr>)}</tbody></table></div>
  </section>;
}

export function EntityMechanics({ entity, catalogue, row, unlocks }) {
  const building = entity.kind === 'buildings' || entity.kind === 'traps';
  const targets = [[row?.is_air_targeting ?? entity.is_air_targeting, 'Air'], [row?.is_ground_targeting ?? entity.is_ground_targeting, 'Ground']].flatMap(([enabled, title]) => enabled ? [title] : []);
  const cadence = row?.attack_speed ?? entity.attack_speed;
  const facts = [
    ['Housing space', !building && entity.housing_space > 0 ? number(entity.housing_space) : null],
    ['Targets', targets.length ? targets.join(' & ') : null],
    ['Attack interval', cadence > 0 ? number(cadence / 1000) + 's' : null],
    ['Attack range', !building && !['spells', 'equipment'].includes(entity.kind) && entity.attack_range > 0 ? formatTiles(entity.attack_range / 100) : null],
    ['Movement speed', entity.movement_speed > 0 ? number(entity.movement_speed / 100) + ' tiles/s' : null],
    ['Footprint', entity.width > 0 ? entity.width + ' × ' + entity.width : null],
    ['Warden weight', entity.warden_weight != null ? number(entity.warden_weight) : null],
    ['Healer weight', entity.healer_weight != null ? number(entity.healer_weight) : null],
    ['Town Hall', !row && entity.required_townhall > 0 ? <Hall village={entity.village} level={entity.required_townhall} /> : null],
    ['Gold Pass', entity.requires_gold_pass ? 'Required' : null],
    ['Boost speed', entity.clock_boost_multiplier > 0 ? number(entity.clock_boost_multiplier) + '×' : null],
    ['Boost cooldown', entity.clock_boost_cooldown_seconds > 0 ? duration(entity.clock_boost_cooldown_seconds) : null],
    ...Object.entries(row?.stored_resources || entity.stored_resources || {}).filter(() => !row?.storage_capacity).map(([resource, amount]) => [resource + ' storage', number(amount)]),
  ].filter(([, value]) => value != null);
  const related = entity.kind === 'heroes' ? catalogue.entities.filter((item) => item.kind === 'equipment' && item.hero === entity.sourceName)
    : entity._id === 1000093 ? catalogue.entities.filter((item) => item.kind === 'helpers')
      : entity._id === 1000105 ? catalogue.entities.filter((item) => item.kind === 'guardians') : [];
  const hallUnlocks = (row?.unlocks || []).flatMap(({ _id, quantity }) => {
    const item = catalogue.entitiesByKey.get('buildings:' + _id) || catalogue.entitiesByKey.get('traps:' + _id);
    return item && !item.assetOnly ? [{ ...item, badge: quantity > 1 ? '×' + quantity : '' }] : [];
  });
  const list = hallUnlocks.length ? hallUnlocks : unlocks.length > 1 ? unlocks : [];
  const defenders = (entity.defending_troops || []).map((id) => catalogue.entitiesByKey.get('troops:' + id)).filter(Boolean);
  const merge = [...new Map((row?.merge_requirement || []).map((required) => [required._id, required])).values()].flatMap((required) => {
    const item = catalogue.entitiesByKey.get('buildings:' + required._id);
    const count = row.merge_requirement.filter((part) => part._id === required._id).length;
    return item ? [{ ...item, badge: (count > 1 ? '×' + count + ' · ' : '') + 'Lv ' + required.level + (required.geared_up ? ' · geared up' : '') }] : [];
  });
  const alternate = row?.alt;
  const gearedBuilding = entity.gear_up && catalogue.entitiesByKey.get('buildings:' + entity.gear_up.building_id);
  const alternateRows = alternate ? [
    ['Attack range', alternate.attack_range != null ? formatTiles(alternate.attack_range / 100) : null],
    ['Damage per second', alternate.dps > 0 ? number(alternate.dps) : null],
    ['Attack interval', alternate.attack_speed > 0 ? number(alternate.attack_speed / 1000) + 's' : null],
  ].filter(([, value]) => value != null) : [];
  const details = facts.length > 0 && <section className="block">
    <div className="block-head"><h2>Details</h2></div>
    <Spec rows={facts} />
  </section>;
  const range = <Range entity={entity} row={row} />;

  return <>
    {related.length > 0 && <section className="block">
      <div className="block-head"><h2>{entity.kind === 'heroes' ? 'Equipment' : entity._id === 1000093 ? 'Helpers' : 'Guardians'}</h2></div>
      {entity.kind === 'heroes' ? <div className="equipment-groups">{['Common', 'Epic'].map((rarity) => {
        const gear = related.filter((item) => item.rarity === rarity);
        return gear.length > 0 && <div key={rarity}>
          <h3 className={rarity === 'Epic' ? 'epic-text' : undefined}>{rarity}</h3>
          <LinkedItems items={gear.map((item) => item.is_starter ? { ...item, badge: 'Starter' } : item)} />
        </div>;
      })}</div> : <LinkedItems items={related} />}
    </section>}
    <div className="pair">
      {details}
      {range}
    </div>
    {alternate && <section className="block">
      <div className="block-head"><h2>Alternate mode</h2></div>
      <Spec rows={alternateRows} />
      {gearedBuilding && <p className="note">Unlocked by gearing up <a href={gearedBuilding.route}>{gearedBuilding.name}</a>.</p>}
    </section>}
    {list.length > 0 && <section className="block">
      <div className="block-head"><h2>{entity._id === 1000070 ? 'Common equipment' : 'Unlocks'}<span className="block-meta">{list.length} at level {row.display}</span></h2></div>
      <LinkedItems items={list} />
    </section>}
    {merge.length > 0 && <section className="block"><div className="block-head"><h2>Merge requirements</h2></div><LinkedItems items={merge} /></section>}
    {defenders.length > 0 && <section className="block"><div className="block-head"><h2>Defending troops</h2></div><LinkedItems items={defenders} /></section>}
    {entity.type === 'Wall' && <WallAvailability entity={entity} catalogue={catalogue} />}
    <Supercharges entity={entity} />
  </>;
}
