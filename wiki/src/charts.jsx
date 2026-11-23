import { useEffect, useRef, useState } from 'react';
import { abilityRows, buildStep, compactNumber, costParts, duration, formatStat, label, levelMetrics, number } from './catalog.js';
import { WikiSelect } from './ui.jsx';

export function chartSeries(entity) {
  const series = new Map();
  const add = (key, name, stat, row, value, context = 'level') => {
    if (!Number.isFinite(value)) return;
    if (!series.has(key)) series.set(key, { name, stat, context, points: [] });
    series.get(key).points.push({ level: row.display, value });
  };
  const metrics = levelMetrics(entity);
  for (const row of entity.levels) {
    for (const key of metrics) {
      if ((entity._id !== 4000106 && entity._id !== 1000001) || key !== 'dps') add(key, label(key), key, row, row[key]);
    }
    const step = buildStep(entity, row);
    for (const [resource, value] of costParts(step?.cost, step?.resource)) {
      add('cost:' + resource, resource ? resource + ' to reach' : 'Cost to reach', 'cost', row, value);
    }
    if (step?.seconds > 0) add('upgrade-time', 'Time to reach', 'upgrade-time', row, step.seconds);
    for (const { name, current, trigger } of abilityRows(entity, row)) {
      const mode = entity._id === 4000106 && trigger === 'default_mode' ? 'Inferno mode' : name;
      const prefix = mode + (current.housing_space_lost_required ? ' (' + number(current.housing_space_lost_required) + ')' : '');
      for (const [key, value] of Object.entries(current)) {
        if (key !== 'level' && key !== 'housing_space_lost_required') add(prefix + ':' + key, prefix + ' · ' + label(key), key, row, value, 'ability');
      }
    }
  }
  return new Map([...series].filter(([, entry]) => entry.points.length > 1 && entry.points.every((point) => point.value >= 0)));
}

const chartValue = (series, value) => series.stat === 'upgrade-time' ? duration(value) : formatStat(series.stat, value, series.context);

function Plot({ series, level, onSelect }) {
  const { points } = series;
  const selected = Math.max(0, points.findIndex((point) => point.level === level));
  const [preview, setPreview] = useState(null);
  const [width, setWidth] = useState(720);
  const frame = useRef(null);
  useEffect(() => setPreview(null), [level, series]);
  useEffect(() => {
    if (typeof ResizeObserver !== 'function') return;
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(260, Math.round(entry.contentRect.width))));
    observer.observe(frame.current);
    return () => observer.disconnect();
  }, []);
  const active = preview ?? selected;
  const minLevel = points[0].level;
  const maxLevel = points.at(-1).level;
  const top = 10;
  const right = width - 4;
  const baseline = width < 500 ? 170 : 220;
  const min = Math.min(...points.map((point) => point.value));
  const max = Math.max(...points.map((point) => point.value));
  const span = max - min || Math.max(1, Math.abs(max) * 0.1);
  const tickSize = 10 ** Math.floor(Math.log10(span / 4));
  const tick = [1, 2, 2.5, 5, 10].find((multiple) => multiple * tickSize >= span / 4) * tickSize;
  const low = Math.max(0, Math.floor((min - span * 0.05) / tick) * tick);
  const high = Math.ceil((max + span * 0.05) / tick) * tick;
  const tickCount = Math.round((high - low) / tick);
  const yTicks = Array.from({ length: tickCount + 1 }, (_, index) => {
    const value = low + tick * index;
    const text = series.stat === 'upgrade-time' ? compactNumber(value / 3600) + 'h'
      : ['attack_speed', 'duration'].includes(series.stat) || series.stat.endsWith('_percent')
        ? chartValue(series, value) : compactNumber(value);
    return { value, text };
  });
  const left = Math.max(34, ...yTicks.map(({ text }) => text.length * 7 + 10));
  const x = (point) => left + (point.level - minLevel) / (maxLevel - minLevel || 1) * (right - left);
  const y = (value) => baseline - (value - low) / (high - low || 1) * (baseline - top);
  const current = points[active] || points[0];
  const line = points.map((point) => [x(point), y(point.value)].join(',')).join(' ');
  const ticks = width < 500 ? [0, 0.5, 1] : [0, 0.25, 0.5, 0.75, 1];
  const levelTicks = [...new Set(maxLevel - minLevel <= 6
    ? points.map(({ level }) => level)
    : ticks.map((part) => Math.round(minLevel + (maxLevel - minLevel) * part)))];
  const selectPoint = (event) => {
    if (event.pointerType === 'touch' && event.type === 'pointermove') return;
    const rect = event.currentTarget.getBoundingClientRect();
    const local = (event.clientX - rect.left) * width / rect.width;
    const index = points.reduce((closest, point, index) =>
      Math.abs(x(point) - local) < Math.abs(x(points[closest]) - local) ? index : closest, 0);
    if (event.type === 'click') {
      setPreview(null);
      onSelect?.(points[index].level);
    } else setPreview(index);
  };
  const selectWithKeyboard = (event) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      onSelect?.(points[active].level);
      return;
    }
    const levels = { ArrowLeft: Math.max(0, active - 1), ArrowRight: Math.min(points.length - 1, active + 1), Home: 0, End: points.length - 1 };
    if (!Object.hasOwn(levels, event.key)) return;
    event.preventDefault();
    setPreview(levels[event.key]);
  };
  return <div className="chart">
    <p className="chart-readout" aria-live="polite">
      <strong>{chartValue(series, current.value)}</strong>
      <span>Level {current.level}</span>
    </p>
    <div ref={frame} className="chart-frame" role="group" tabIndex={0}
      aria-label="Explore chart levels with the arrow keys. Press Enter to select a level."
      onPointerMove={selectPoint} onPointerLeave={() => setPreview(null)} onBlur={() => setPreview(null)}
      onClick={selectPoint} onKeyDown={selectWithKeyboard}>
      <svg viewBox={`0 0 ${width} ${baseline + 28}`} role="img" aria-label={series.name + ' by level'} fontSize="11.5">
        <title>{series.name + ' by level'}</title>
        {yTicks.map(({ value, text }) => <g key={value}>
          <line x1={left} x2={right} y1={y(value)} y2={y(value)} className={value === low ? 'axis' : 'grid'} />
          <text x={left - 10} y={y(value) + 4} textAnchor="end" className="tick">{text}</text>
        </g>)}
        <line x1={x(current)} x2={x(current)} y1={top} y2={baseline} className="cursor" />
        <polyline points={line} className="line" fill="none" />
        {levelTicks.map((tickLevel, index) => <text x={x({ level: tickLevel })} y={baseline + 20} textAnchor={index === 0 ? 'start' : index === levelTicks.length - 1 ? 'end' : 'middle'} className="tick" key={tickLevel}>{tickLevel}</text>)}
        <circle cx={x(current)} cy={y(current.value)} r="4.5" className="dot" />
      </svg>
    </div>
  </div>;
}

export function EntityCharts({ series, level, initialMetric, onSelect }) {
  const [metric, setMetric] = useState(initialMetric);
  const selected = series.has(metric) ? metric : series.keys().next().value;
  if (!selected) return null;
  return <section className="block" id="charts" tabIndex={-1}>
    <div className="block-head">
      <h2>Charts</h2>
      <WikiSelect className="chart-stat" label="Chart stat" hideLabel value={selected} onChange={setMetric}
        options={[...series].map(([key, entry]) => [key, entry.name])} />
    </div>
    <Plot key={selected} series={series.get(selected)} level={level} onSelect={onSelect} />
  </section>;
}
