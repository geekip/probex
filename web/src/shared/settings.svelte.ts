import en from './i18n/en';
import zh from './i18n/zh';

export type Preference = 'auto' | 'light' | 'dark';
export type Language = 'auto' | 'zh' | 'en';
export const preferences = $state({
  theme: (localStorage.getItem('probex.theme') || 'auto') as Preference,
  language: (localStorage.getItem('probex.language') || 'auto') as Language,
  dark: matchMedia('(prefers-color-scheme: dark)').matches,
  browserLanguage: navigator.language,
});
export function language() {
  return preferences.language === 'auto'
    ? preferences.browserLanguage.startsWith('zh')
      ? 'zh'
      : 'en'
    : preferences.language;
}
export function dark() {
  return preferences.theme === 'auto'
    ? preferences.dark
    : preferences.theme === 'dark';
}
const messages = { en, zh };
export type Message = keyof typeof messages.en;
export function t(key: Message) {
  return messages[language()][key];
}
export function date(value?: string, lastReport = false) {
  if (!value || value.startsWith('0001-')) return '—';
  const time = new Date(value);
  const now = new Date();
  const pad = (part: number) => String(part).padStart(2, '0');
  const hours = `${pad(time.getHours())}:${pad(time.getMinutes())}`;
  if (lastReport && time.toDateString() === now.toDateString())
    return `${hours}:${pad(time.getSeconds())}`;
  const day = `${pad(time.getMonth() + 1)}-${pad(time.getDate())}`;
  const year =
    time.getFullYear() === now.getFullYear() ? '' : `${time.getFullYear()}-`;
  return `${year}${day} ${hours}`;
}
export function bytes(value?: number) {
  if (value == null) return t('unavailable');
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value.toFixed(i ? 1 : 0)} ${units[i]}`;
}
