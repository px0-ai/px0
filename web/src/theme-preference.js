export const DEFAULT_THEME = 'github-dark';
export const DEFAULT_LIGHT_THEME = 'light';

export function chooseThemePreference(savedTheme, configuredTheme, fallbackTheme = DEFAULT_THEME) {
  const explicit = configuredTheme && configuredTheme !== fallbackTheme ? configuredTheme : null;
  const saved = savedTheme && savedTheme !== fallbackTheme ? savedTheme : null;
  if (explicit) return explicit;
  if (saved) return saved;
  return fallbackTheme;
}

export function pickAutoTheme(prefersDark, lightTheme, darkTheme) {
  return prefersDark ? (darkTheme || DEFAULT_THEME) : (lightTheme || DEFAULT_LIGHT_THEME);
}
