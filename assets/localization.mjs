export const DEFAULT_LOCALE = "en";
export const LOCALE_PREFERENCE_KEY = "clashking-locale";

export function normalizeLocale(value) {
  if (!value || typeof value !== "string") return DEFAULT_LOCALE;
  try {
    return Intl.getCanonicalLocales(value.replaceAll("_", "-"))[0] || DEFAULT_LOCALE;
  } catch {
    return DEFAULT_LOCALE;
  }
}

export function localeCandidates(value) {
  const normalized = normalizeLocale(value);
  const language = normalized.split("-")[0];
  return [...new Set([normalized, language, DEFAULT_LOCALE])];
}

export function interpolate(template, parameters = {}) {
  return String(template).replace(/\{([A-Za-z0-9_]+)\}/g, (match, name) => (
    Object.hasOwn(parameters, name) ? String(parameters[name]) : match
  ));
}

export function createTranslator(catalog, englishCatalog = catalog) {
  return (key, parameters) => interpolate(catalog[key] ?? englishCatalog[key] ?? key, parameters);
}

export function requestedLocale(explicitLocale) {
  const params = new URLSearchParams(window.location.search);
  if (explicitLocale) return explicitLocale;
  if (params.get("locale") || params.get("lang")) return params.get("locale") || params.get("lang");
  try {
    const stored = localStorage.getItem(LOCALE_PREFERENCE_KEY);
    if (stored) return stored;
  } catch {
    // Browser language remains available when storage is disabled.
  }
  return navigator.languages?.[0] || navigator.language || DEFAULT_LOCALE;
}

async function fetchCatalog(baseURL, locale) {
  const response = await fetch(`${baseURL}/${encodeURIComponent(locale)}.json`);
  if (!response.ok) throw new Error(`locale catalog request failed: ${response.status}`);
  return response.json();
}

export async function loadLocalization({ explicitLocale, baseURL = "./locales" } = {}) {
  const english = await fetchCatalog(baseURL, DEFAULT_LOCALE);
  let locale = DEFAULT_LOCALE;
  let catalog = english;
  for (const candidate of localeCandidates(requestedLocale(explicitLocale))) {
    if (candidate === DEFAULT_LOCALE) break;
    try {
      catalog = { ...english, ...(await fetchCatalog(baseURL, candidate)) };
      locale = candidate;
      break;
    } catch {
      // Try the language-only catalog, then English.
    }
  }
  document.documentElement.lang = locale;
  return { locale, t: createTranslator(catalog, english), english };
}

export function translateEnglishDocument(t, english, root = document) {
  const keysByEnglish = new Map(Object.entries(english).map(([key, value]) => [value, key]));
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const value = node.nodeValue.trim();
    const key = keysByEnglish.get(value);
    if (key) node.nodeValue = node.nodeValue.replace(value, t(key));
  }
  for (const element of root.querySelectorAll("[aria-label], [title], [placeholder]")) {
    for (const attribute of ["aria-label", "title", "placeholder"]) {
      const value = element.getAttribute(attribute);
      const key = keysByEnglish.get(value);
      if (key) element.setAttribute(attribute, t(key));
    }
  }
}

export function translateDocument(t, root = document) {
  for (const element of root.querySelectorAll("[data-i18n]")) element.textContent = t(element.dataset.i18n);
  for (const element of root.querySelectorAll("[data-i18n-placeholder]")) element.placeholder = t(element.dataset.i18nPlaceholder);
  for (const element of root.querySelectorAll("[data-i18n-aria-label]")) element.setAttribute("aria-label", t(element.dataset.i18nAriaLabel));
  for (const element of root.querySelectorAll("[data-i18n-title]")) element.title = t(element.dataset.i18nTitle);
}
