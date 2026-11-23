import { useMemo } from 'react';
import { ChevronDown } from 'lucide-react';
import { Art, Cost, Empty, TownHallSelect } from './ui.jsx';
import { duration, number } from './catalog.js';
import { townHallUpgradeData } from './upgrade-data.js';

const categoryArt = {
  buildings: 'buildings/home-village/town_hall/level_12.webp',
  traps: 'traps/home-village/bomb/level_3.webp',
  walls: 'buildings/home-village/wall/level_17.webp',
  heroes: 'heroes/archer_queen/icon.webp',
  troops: 'troops/barbarian/icon.webp',
  spells: 'spells/rage_spell/icon.webp',
  squads: 'troops/barch_squad/icon.webp',
  pets: 'pets/unicorn/icon.webp',
  equipment: 'equipment/giant_gauntlet/icon.webp',
  guardians: 'guardians/longshot/icon.webp',
  helpers: "helpers/builder's_apprentice.webp",
};

function hallWork({ categories }, ids) {
  return categories.reduce((total, category) => ids.includes(category.id) ? total + category.seconds : total, 0);
}

const builderWork = (tier) => hallWork(tier, ['buildings', 'traps', 'heroes']);
const labWork = (tier) => hallWork(tier, ['troops', 'spells', 'squads']);
// Dividing total work gives a lower bound before uneven queue lengths or boosts.
const sixBuilders = (tier) => Math.ceil(builderWork(tier) / 6);

export function TownHallUpgrades({ catalogue, url, onUrlChange }) {
  const tiers = useMemo(() => townHallUpgradeData(catalogue), [catalogue]);
  const selection = url.searchParams.get('hall');
  const tier = tiers.find(({ hall }) => hall === Number(selection)) || tiers.at(-1);
  if (!tier) return <Empty>Town Hall upgrades are unavailable.</Empty>;
  const { hall, categories, steps, totals, unavailable } = tier;
  const builderSeconds = builderWork(tier);
  const changeHall = (value) => {
    const next = new URL(location.href);
    next.searchParams.set('hall', value);
    history.replaceState(null, '', next);
    onUrlChange(next.pathname + next.search + next.hash);
  };
  return <section className="upgrades">
    <div className="upgrades-toolbar">
      <TownHallSelect levels={tiers.map(({ hall }) => hall).toReversed()} value={hall} onChange={changeHall} />
    </div>

    <dl className="stat-strip">
      <div><dt>Upgrades</dt><dd>{number(steps)}</dd></div>
      <div>
        <dt>Builder work</dt><dd>{duration(builderSeconds)}</dd>
        {builderSeconds > 0 && <small>6 builders · {duration(sixBuilders(tier))} minimum</small>}
      </div>
      <div><dt>Lab work</dt><dd>{duration(labWork(tier))}</dd></div>
    </dl>

    {Object.keys(totals).length > 0 && <div className="totals">
      <span className="totals-label">Total cost</span>
      <Cost step={{ cost: totals }} />
    </div>}

    <div className="accordion">{categories.map(({ id, name, items, costs, seconds, steps: count }) => <details key={id}>
      <summary>
        <span className="acc-title">
          {categoryArt[id] && <Art file={categoryArt[id]} />}
          <span><strong>{name}</strong><small>{number(count)} upgrade{count === 1 ? '' : 's'}</small></span>
        </span>
        <span className="acc-values">
          {seconds > 0 && <span className="time">{duration(seconds)}</span>}
          {Object.keys(costs).length > 0 && <Cost step={{ cost: costs }} />}
        </span>
        <ChevronDown size={16} className="chevron" aria-hidden="true" />
      </summary>
      <div className="table-scroll"><table className="data-table"><thead><tr><th scope="col">Item</th><th scope="col" className="num">Upgrades</th><th scope="col" className="num">Cost</th><th scope="col" className="num">Work</th></tr></thead><tbody>
        {items.map(({ entity, costs: itemCosts, seconds: itemSeconds, steps: itemSteps, levels }) => <tr key={entity.key}>
          <th scope="row"><a className="item-link" href={entity.route}><Art file={entity.art} />{entity.name}</a></th>
          <td className="num">{number(itemSteps)}<small className="sub">Lv {levels.length > 1 ? levels[0] + '–' + levels.at(-1) : levels[0]}</small></td>
          <td className="num"><Cost step={{ cost: itemCosts }} /></td>
          <td className="num">{itemSeconds ? duration(itemSeconds) : '—'}</td>
        </tr>)}
      </tbody></table></div>
    </details>)}</div>
    {unavailable.length > 0 && <p className="note">Costs unavailable for {unavailable.map((entity) => entity.name).join(', ')}.</p>}
  </section>;
}
