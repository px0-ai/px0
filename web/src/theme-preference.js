export const DEFAULT_THEME = 'github-dark';

export function chooseThemePreference(savedTheme, configuredTheme, fallbackTheme = DEFAULT_THEME) {
  const explicit = configuredTheme && configuredTheme !== fallbackTheme ? configuredTheme : null;
  const saved = savedTheme && savedTheme !== fallbackTheme ? savedTheme : null;
  if (explicit) return explicit;
  if (saved) return saved;
  return fallbackTheme;
}
