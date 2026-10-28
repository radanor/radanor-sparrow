// Appearance handling for the dashboard.
//
// The chosen theme is a persisted preference owned by the core: it is read from
// the settings endpoint at startup and written back through the authenticated
// settings update. This module only reflects it onto the document and lets the
// Settings page and the shell stay in sync.
//
// "system" intentionally follows the operating system's preference so a user who
// sets their desktop to light is not forced into a dark dashboard.

export type Theme = "light" | "dark" | "system";

const listeners = new Set<(theme: Theme) => void>();
let current: Theme = "system";

export function parseTheme(value: unknown): Theme {
  return value === "light" || value === "dark" || value === "system" ? value : "system";
}

export function themeOptions(): Array<{ value: Theme; label: string; description: string }> {
  return [
    { value: "system", label: "Match system", description: "Follow the operating system's light or dark setting." },
    { value: "light", label: "Light", description: "Always use the light palette." },
    { value: "dark", label: "Dark", description: "Always use the dark palette." }
  ];
}

// applyTheme records the preference and reflects it onto the document.
export function applyTheme(theme: Theme): void {
  current = theme;
  const root = document.documentElement;
  if (theme === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", theme);
  for (const listener of listeners) listener(theme);
}

export function currentTheme(): Theme {
  return current;
}

export function subscribeTheme(listener: (theme: Theme) => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
