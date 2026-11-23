import { LoaderCircle } from 'lucide-react';
import { useWikiText } from './locale.js';
import { WikiSelect } from './ui.jsx';

// Most game codes double as flag codes. These locales use the same flags as
// the Dashboard where available and distinct flags for the two Chinese packs.
const flagCodes = { AR: 'sa', CNT: 'tw', EN: 'us', FA: 'ir', MS: 'my', VI: 'vn' };

function LanguageLabel({ option, showEnglish = false, pending = false }) {
  const { code, tag, name, english } = option;
  const flag = flagCodes[code] || code.toLowerCase();
  return <span className="language-label">
    <img src={'https://flagcdn.com/w40/' + flag + '.png'} alt="" width="20" height="14" />
    <span className="language-names">
      <bdi lang={tag}>{name}</bdi>
      {showEnglish && name !== english && <small lang="en">{english}</small>}
    </span>
    {pending && <LoaderCircle size={13} className="action-spinner" aria-hidden="true" />}
  </span>;
}

export function LanguagePicker({ languages, value, pending, onChange }) {
  const t = useWikiText();
  const selected = languages.find(({ code }) => code === value);
  return <WikiSelect label={t('Language')} hideLabel className="language-select" contentClassName="language-popover"
    value={value} onChange={onChange} selectedText={<LanguageLabel option={selected} pending={!!pending} />}
    options={languages.map((option) => [option.code, <LanguageLabel option={option} showEnglish />])} />;
}
