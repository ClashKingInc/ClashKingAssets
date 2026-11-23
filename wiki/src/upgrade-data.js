import { buildStep, costParts } from './catalog.js';

const groups = new Map([
  ['buildings', 'Buildings'], ['traps', 'Traps'], ['walls', 'Walls'], ['heroes', 'Heroes'], ['troops', 'Troops'],
  ['spells', 'Spells'], ['squads', 'Squads'], ['pets', 'Pets'], ['equipment', 'Equipment'], ['guardians', 'Guardians'], ['helpers', 'Helpers'],
]);

function wallCap(entity, level, hall, quantity) {
  if (entity.type !== 'Wall') return quantity;
  const explicit = level.upgrade_limit;
  if (Number.isFinite(explicit)) return Math.min(quantity, explicit);
  // Supercell's Town Hall 19 launch caps level 20 to 150 wall pieces.
  if (entity._id === 1000010 && level.level === 20 && hall === 19) return Math.min(quantity, 150);
  return quantity;
}

export function townHallUpgradeData(catalogue) {
  const halls = catalogue.entitiesByKey.get('buildings:1000001')?.levels || [];
  let previous = new Map();
  const firstGrant = new Map();
  const home = catalogue.entities.filter((item) => item.village === 'home' && item._id !== 1000001 && !item.is_seasonal && !item.super_troop);
  const structures = new Map(home.filter((item) => ['buildings', 'traps'].includes(item.kind)).map((item) => [item._id, item]));
  return halls.map(({ level: hall, unlocks = [], merge_requirement: hallMerge = [] }) => {
    const active = new Map(previous);
    const merged = new Map();
    for (const { _id, quantity } of unlocks) {
      if (!_id || !Number.isFinite(quantity)) continue;
      if (!firstGrant.has(_id)) firstGrant.set(_id, hall);
      active.set(_id, (active.get(_id) || 0) + quantity);
    }
    const mergePlans = [{ quantity: 1, requirements: hallMerge }, ...unlocks.map(({ _id, quantity }) => ({ quantity, requirements: structures.get(_id)?.levels[0]?.merge_requirement || [] }))];
    for (const { quantity, requirements } of mergePlans) {
      for (const source of requirements) {
        const rows = merged.get(source._id) || [];
        rows.push({ level: source.level, quantity });
        merged.set(source._id, rows);
        active.set(source._id, Math.max(0, (active.get(source._id) || 0) - quantity));
      }
    }
    const categories = new Map();
    const totals = Object.create(null);
    let steps = 0;
    const unavailable = [];
    for (const entity of home) {
      const isStructure = structures.has(entity._id);
      const type = entity.type === 'Wall' ? 'walls' : entity.isSquad ? 'squads' : entity.kind;
      const newlyGranted = (unlock) => unlock._id === entity._id && unlock.quantity > 0;
      if (isStructure && !entity.levels.length && unlocks.some(newlyGranted)) unavailable.push(entity);
      const item = { entity, costs: Object.create(null), seconds: 0, steps: 0, levels: [] };
      let gate = isStructure ? firstGrant.get(entity._id) || 0 : 0;
      for (const row of entity.levels) {
        gate = Math.max(gate, row.hall || 0);
        if (!gate || gate > hall) continue;
        const step = buildStep(entity, row);
        if (!step) continue;
        let count = gate === hall ? 1 : 0;
        if (isStructure) {
          // Parts combined this Town Hall still need to reach their merge level.
          // Count this work once, then stop counting the consumed structures.
          const inputs = (merged.get(entity._id) || []).filter((piece) => piece.level >= row.level)
            .reduce((sum, piece) => sum + piece.quantity, 0);
          const copies = wallCap(entity, row, hall, active.get(entity._id) || 0) + inputs;
          const before = gate < hall ? wallCap(entity, row, hall - 1, previous.get(entity._id) || 0) : 0;
          count = Math.max(0, copies - before);
        }
        if (!count) continue;
        const priced = costParts(step.cost, step.resource);
        if (!priced.length && !Number.isFinite(step.seconds)) continue;
        item.steps += count;
        item.levels.push(row.display);
        item.seconds += (step.seconds || 0) * count;
        for (const [resource, value] of priced) {
          if (!value || !resource) continue;
          const name = entity.type === 'Wall' && row.alt_upgrade_resource ? resource + ' or ' + row.alt_upgrade_resource : resource;
          item.costs[name] = (item.costs[name] || 0) + value * count;
          totals[name] = (totals[name] || 0) + value * count;
        }
      }
      if (!item.steps) continue;
      steps += item.steps;
      const category = categories.get(type) || { id: type, name: groups.get(type), items: [], costs: Object.create(null), steps: 0, seconds: 0 };
      category.items.push(item);
      category.steps += item.steps;
      category.seconds += item.seconds;
      for (const [resource, amount] of Object.entries(item.costs)) category.costs[resource] = (category.costs[resource] || 0) + amount;
      categories.set(type, category);
    }
    previous = active;
    return { hall, steps, totals, unavailable, categories: [...groups.keys()].flatMap((key) => categories.has(key) ? [categories.get(key)] : []) };
  });
}
