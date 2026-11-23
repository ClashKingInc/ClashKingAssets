import { createContext, useContext } from 'react';
import labels from './locale-labels.json' with { type: 'json' };

export const WikiLocale = createContext('EN');
export const wikiText = (language, text) => labels[language]?.[text] || text;

export function useWikiText() {
  const language = useContext(WikiLocale);
  return (text) => wikiText(language, text);
}
