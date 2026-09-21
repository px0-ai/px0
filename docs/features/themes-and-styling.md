# Themes & Visual Customization

px0 comes pre-packaged with 14 polished color themes spanning popular dark, low-contrast, and light aesthetics. Backed by a strict CSS custom property token architecture, themes provide consistent contrast and visual harmony across code, diff views, Markdown previews, and modals.

---

## Overview & Core Purpose

Visual comfort and ergonomic contrast are essential for developers spending long hours auditing code and reviewing diffs. Lighting conditions change throughout the day, and different engineers prefer different palettes—from deep midnight contrast to muted pastel tones or crisp daylight schemes.

px0 provides instant theme switching with zero browser reloading. Themes are implemented using pure CSS custom variables (`var(--bg)`, `var(--fg)`, `var(--accent)`), ensuring that changing a theme updates every element—editor rows, file trees, diff panels, markdown headers, and status badges—cohesively and instantaneously.

---

## Built-In Themes

px0 ships with 14 curated themes ready for immediate use:

| Theme Name | Identifier | Aesthetic & Style |
| :--- | :--- | :--- |
| **GitHub Dark** | `github-dark` | Classic GitHub Dark theme (Default) |
| **Tokyo Night** | `tokyo-night` | Celebrated deep blue and neon palette |
| **Catppuccin Mocha** | `catppuccin-mocha` | Soothing, low-contrast dark pastel palette |
| **Catppuccin Latte** | `catppuccin-latte` | Warm, low-contrast light pastel palette |
| **Dracula** | `dracula` | Vibrant purple and pink dark theme |
| **Gruvbox Dark** | `gruvbox-dark` | Retro groove warm dark palette |
| **Gruvbox Light** | `gruvbox-light` | Retro groove warm parchment light palette |
| **Nord** | `nord` | Arctic, north-bluish clean palette |
| **Solarized Dark** | `solarized-dark` | Low-contrast teal and yellow solarized palette |
| **Solarized Light** | `solarized-light` | Warm paper solarized light palette |
| **Monokai** | `monokai` | Iconic high-contrast vibrant dark palette |
| **One Dark** | `one-dark` | Atom / VS Code classic dark palette |
| **Rose Pine** | `rose-pine` | Atmospheric dark rose and muted tones |
| **Light Default** | `light` | Crisp, high-contrast daylight theme |

---

## Key Capabilities

- **Instant Live Switching**: Changing your theme in Settings or the Command Palette applies immediately across all tabs and panels without reloading the page.
- **Universal Token Coverage**: Themes govern all UI subsystems uniformly:
  - Source code syntax tokens (keywords, strings, types, functions, comments).
  - Git diff backgrounds and line gutters (added, deleted, modified).
  - Markdown article elements, blockquotes, and alert badges.
  - Search highlighting, minimap ticks, and selection overlays.
- **High-Contrast Readability**: Every theme is tuned to maintain high text-to-background contrast ratios, preventing eye fatigue during extended inspection sessions.
- **Dynamic Server Aggregation**: Themes are served as a single lightweight stylesheet (`/static/themes.css`) with zero runtime overhead.

---

## Developer Workflows & Switching Themes

### Quick Theme Switching via Command Palette
1. Press **`Cmd/Ctrl+Shift+P`** to open the Command Palette.
2. Type `Preferences: Color Theme` (or `theme`) and press `Enter`.
3. Use the arrow keys to preview themes dynamically in real time.
4. Press `Enter` to confirm your selection.

### Configuring via Settings UI
1. Press **`Cmd/Ctrl+,`** to open Settings.
2. Navigate to **Workbench: Color Theme** (`workbench.colorTheme`).
3. Select your preferred theme from the dropdown or click one of the interactive theme pill buttons.

Your choice is saved in `~/.px0/settings.json` and remembered across all future sessions.

---

## Technical Architecture Deep Dive

For the complete CSS custom property token dictionary, naming conventions, and instructions on creating custom themes, see [Theme Architecture & CSS Tokens Internals](../internals/styling-and-themes.md).
