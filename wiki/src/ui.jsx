import { startTransition, useEffect, useId, useRef, useState } from 'react';
import * as DropdownMenu from '@radix-ui/react-dropdown-menu';
import * as Select from '@radix-ui/react-select';
import { Box, Check, ChevronDown, ChevronRight, ChevronUp, ImageOff, LoaderCircle, Search, X } from 'lucide-react';
import { assetUrl, compactNumber, costParts, hallLabel, number, resourceArt, villageArt } from './catalog.js';

export function WikiSelect({ label, hideLabel = false, value, onChange, options, groups, selectedText, className = '', contentClassName = '' }) {
  const labelId = useId();
  const sections = groups || [{ options }];
  return <div className={'field ' + className}>
    <span id={labelId} className={hideLabel ? 'sr-only' : undefined}>{label}</span>
    <Select.Root value={String(value)} onValueChange={onChange}>
      <Select.Trigger className="select-trigger" aria-labelledby={labelId}>
        <Select.Value>{selectedText}</Select.Value>
        <Select.Icon asChild><ChevronDown size={16} /></Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Content position="popper" sideOffset={6} collisionPadding={12} className={'select-popover ' + contentClassName}>
          <Select.ScrollUpButton className="select-scroll"><ChevronUp size={16} /></Select.ScrollUpButton>
          <Select.Viewport className="select-viewport">
            {sections.map(({ name, options }) => <Select.Group key={name || label}>
              {name && <Select.Label className="select-label">{name}</Select.Label>}
              {options.map(([key, text, art]) => <Select.Item key={key} value={String(key)} className="select-option">
                <Select.ItemText>{art ? <span className="select-art-label"><Art file={art} />{text}</span> : text}</Select.ItemText>
                <Select.ItemIndicator className="select-indicator"><Check size={16} /></Select.ItemIndicator>
              </Select.Item>)}
            </Select.Group>)}
          </Select.Viewport>
          <Select.ScrollDownButton className="select-scroll"><ChevronDown size={16} /></Select.ScrollDownButton>
        </Select.Content>
      </Select.Portal>
    </Select.Root>
  </div>;
}

export function Art({ file, alt = '', eager = false, className = '' }) {
  const path = typeof file === 'string' ? file : file?.path;
  const [failedPath, setFailedPath] = useState();
  if (!path || failedPath === path) return <ImageOff className={'missing-art ' + className} aria-hidden="true" />;
  return <img className={className} src={assetUrl(path)} alt={alt} loading={eager ? 'eager' : 'lazy'} decoding="async" onError={() => setFailedPath(path)} />;
}

export function AudioPlayer({ track, onError }) {
  return <audio aria-label={track.display_name} controls preload="none" src={assetUrl(track.path)} onError={onError}>
    <a href={assetUrl(track.path)}>Download audio</a>
  </audio>;
}

export function Crumbs({ items }) {
  return <nav className="crumbs" aria-label="Breadcrumb">
    {items.map(([name, href], index) => <span key={name + index}>
      {index > 0 && <ChevronRight size={13} />}
      {href ? <a href={href}>{name}</a> : <span aria-current="page">{name}</span>}
    </span>)}
  </nav>;
}

export function Hall({ village, level }) {
  if (!level) return <span>—</span>;
  return <span className="hall"><Art file={villageArt(village, level)} />{hallLabel(village, level)}</span>;
}

export function TownHallSelect({ levels, value, onChange }) {
  return <WikiSelect label="Town Hall" hideLabel value={value} onChange={onChange}
    options={levels.map((level) => [level, 'Town Hall ' + level, villageArt('home', level)])} />;
}

function resolveResourceIcons(resource) {
  if (!resource) return [];
  if (resourceArt[resource]) return [resourceArt[resource]];
  const parts = resource.split(/\s+or\s+/i).map((part) => part.trim());
  if (parts.length === 2 && resourceArt[parts[0]] && resourceArt[parts[1]]) {
    return [resourceArt[parts[0]], resourceArt[parts[1]]];
  }
  return [];
}

export function Cost({ step }) {
  const parts = costParts(step?.cost, step?.resource);
  if (!parts.length) return <span className="dim">—</span>;
  return <span className="cost">
    {parts.map(([resource, value]) => {
      const displayResource = resource.replace(/\bOr\b/g, 'or');
      const icons = resolveResourceIcons(resource);
      const exact = displayResource ? number(value) + ' ' + displayResource : number(value);
      return <span key={resource} className={icons.length > 1 ? 'cost-item cost-item-dual' : 'cost-item'} title={exact}>
        {icons.length === 1 && <Art file={icons[0]} />}
        {icons.length === 2 && <span className="cost-dual-icons" aria-hidden="true"><Art file={icons[0]} /><Art file={icons[1]} /></span>}
        <span aria-hidden="true">{compactNumber(value)}{!icons.length && displayResource ? ' ' + displayResource : ''}</span>
        <span className="sr-only">{exact}</span>
      </span>;
    })}
  </span>;
}

export function SearchBox({ value, onChange, placeholder, type = 'search', className = '', shortcut = false }) {
  const input = useRef(null);
  const platform = globalThis.navigator?.userAgentData?.platform || globalThis.navigator?.platform || '';
  useEffect(() => {
    if (!shortcut) return;
    const focusSearch = (event) => {
      if (event.defaultPrevented || !(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== 'k') return;
      if (document.querySelector('dialog[open]')) return;
      event.preventDefault();
      input.current?.focus();
    };
    window.addEventListener('keydown', focusSearch);
    return () => window.removeEventListener('keydown', focusSearch);
  }, [shortcut]);
  return <label className={'search-box' + (className ? ' ' + className : '')}>
    <Search size={18} aria-hidden="true" />
    <input ref={input} type={type} aria-label={placeholder} aria-keyshortcuts={shortcut ? 'Control+K Meta+K' : undefined}
      placeholder={placeholder} value={value} onChange={(event) => onChange(event.target.value)} />
    {value
      ? <button className="clear-search" type="button" aria-label="Clear search" onClick={() => { onChange(''); input.current?.focus(); }}><X size={16} /></button>
      : shortcut && <kbd className="search-shortcut" aria-hidden="true">{/Mac/i.test(platform) ? '⌘ K' : 'Ctrl K'}</kbd>}
  </label>;
}

export function Modal({ title, onClose, children, className = '' }) {
  const ref = useRef(null);
  const titleId = useId();
  useEffect(() => {
    const dialog = ref.current;
    const trigger = document.activeElement;
    dialog.showModal();
    return () => {
      dialog.close();
      if (trigger instanceof HTMLElement && trigger.isConnected) trigger.focus();
    };
  }, []);
  return <dialog ref={ref} aria-labelledby={titleId} className={'modal ' + className}
    onCancel={(event) => { event.preventDefault(); onClose(); }}
    onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}>
    <div className="modal-inner">
      <header><h2 id={titleId}>{title}</h2><button className="icon-button" aria-label="Close" onClick={onClose}><X size={20} /></button></header>
      {children}
    </div>
  </dialog>;
}

export function Empty({ children }) {
  return <div className="empty"><Box size={24} strokeWidth={1.5} /><p>{children}</p></div>;
}

export const resultsBatchSize = 36;
const resultsPrefetchDistance = 480;

export function AutoLoadResults({ count, resetKey, label, minimum = 0, children }) {
  const [batch, setBatch] = useState({ resetKey, limit: resultsBatchSize, announcement: '' });
  if (batch.resetKey !== resetKey) {
    // Reset the batch without remounting cards that match both queries.
    setBatch({ resetKey, limit: resultsBatchSize, announcement: '' });
  }
  const limit = Math.min(count, Math.max(batch.limit, minimum));
  const end = useRef(null);
  const hasMore = limit < count;
  useEffect(() => {
    const marker = end.current;
    if (!hasMore || !marker) return;
    let active = true;
    function append() {
      if (!active || document.activeElement?.closest('#site-footer')) return false;
      const { top, bottom } = marker.getBoundingClientRect();
      // A jump can move the marker out of view before an observer callback runs.
      if (bottom < 0 || top > window.innerHeight + resultsPrefetchDistance) return false;
      active = false;
      const next = Math.min(count, limit + resultsBatchSize);
      const announce = window.scrollY > 0 && top <= window.innerHeight;
      startTransition(() => setBatch((current) => current.resetKey !== resetKey ? current : {
        resetKey,
        limit: next,
        announcement: announce ? next + ' of ' + count + ' results shown' + (label ? ' in ' + label : '') : current.announcement,
      }));
      return true;
    }
    if (typeof IntersectionObserver === 'function') {
      const observer = new IntersectionObserver((entries) => {
        if (entries.some((entry) => entry.isIntersecting) && append()) observer.disconnect();
      }, { rootMargin: `0px 0px ${resultsPrefetchDistance}px` });
      // Observe again after each append in case the new end is still inside the margin.
      observer.observe(marker);
      return () => {
        active = false;
        observer.disconnect();
      };
    }
    let frame;
    function schedule() {
      if (!active || frame !== undefined) return;
      frame = requestAnimationFrame(() => {
        frame = undefined;
        append();
      });
    }
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', schedule);
    schedule();
    return () => {
      active = false;
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
      if (frame !== undefined) cancelAnimationFrame(frame);
    };
  }, [count, hasMore, label, limit, resetKey]);
  return <>
    {children(limit)}
    {hasMore && <div ref={end} className="results-sentinel" aria-hidden="true" />}
    <span className="sr-only" role="status">{batch.announcement}</span>
  </>;
}

export function ActionStatus({ children }) {
  return <span className="action-status" role="status">{children}</span>;
}

export function Toggle({ items, value, onChange, label }) {
  return <div className="toggle" role="group" aria-label={label}>
    {items.map(([key, text, icon]) => <button key={key} type="button" className="toggle-button" aria-pressed={value === key} onClick={() => onChange(key)}>
      {icon && <Art file={icon} />}{text}
    </button>)}
  </div>;
}

export function ActionMenu({ label, icon: Icon, busy = false, side = 'bottom', children }) {
  return <DropdownMenu.Root modal={false}>
    <DropdownMenu.Trigger className="button action-menu-trigger" type="button" aria-busy={busy}>
      {busy ? <LoaderCircle className="action-spinner" size={16} aria-hidden="true" /> : Icon && <Icon size={16} aria-hidden="true" />}
      {label}<ChevronDown size={14} aria-hidden="true" />
    </DropdownMenu.Trigger>
    {/* Keep the popup inside a native dialog's top layer when used there. */}
    <DropdownMenu.Content className="select-popover action-menu" side={side} align="end" sideOffset={5} collisionPadding={12}>
      {children}
    </DropdownMenu.Content>
  </DropdownMenu.Root>;
}

export function MenuAction(props) {
  return <DropdownMenu.Item className="menu-action" {...props} />;
}
