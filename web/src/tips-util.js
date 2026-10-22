// web/src/tips-util.js

export const TIPS_URL = 'https://px0.ai/tips.json';
export const TIPS_STORAGE_KEY_HIDDEN = 'px0.tips.hidden';
export const TIPS_STORAGE_KEY_CACHE = 'px0.tips.cache';
export const TIPS_STORAGE_KEY_INDEX = 'px0.tips.lastIndex';

export const DEFAULT_TIPS = [
  {
    id: "code-folding",
    title: "Focus with Code Folding",
    category: "Navigation",
    tag: "Folding",
    content: "Click a gutter chevron to collapse a code block or HTML/XML element. Use **Fold All** or **Unfold All** in the command palette; searching or jumping to hidden code reveals it automatically."
  },
  {
    id: "file-navigation",
    title: "Quick File Navigation",
    category: "Navigation",
    tag: "Navigation",
    content: "Press **`Cmd/Ctrl + P`** to quickly fuzzy-search and jump to any file in your project without clicking through the folder tree.",
    shortcut: "Mod+P"
  },
  {
    id: "command-palette",
    title: "Command Palette",
    category: "Navigation",
    tag: "Palette",
    content: "Access all px0 commands, switch color themes, configure LSP servers, or trigger actions with **`Cmd/Ctrl + Shift + P`**.",
    shortcut: "Mod+Shift+P"
  },
  {
    id: "workspace-search",
    title: "Instant Workspace Search",
    category: "Search",
    tag: "Search",
    content: "Press **`Cmd/Ctrl + Shift + F`** to search text across every file in milliseconds. Supports regular expressions, case matching, whole words, and file globs like `*.go`.",
    shortcut: "Mod+Shift+F"
  },
  {
    id: "find-in-file",
    title: "Find in Active File",
    category: "Search",
    tag: "Search",
    content: "Press **`Cmd/Ctrl + F`** to search inside the current file with match case (`Aa`), and press **`Enter`** or **`Shift + Enter`** to cycle through matches.",
    shortcut: "Mod+F"
  },
  {
    id: "go-to-line",
    title: "Jump Directly to Line",
    category: "Navigation",
    tag: "Navigation",
    content: "Press **`Cmd/Ctrl + G`** to quickly navigate to a specific line number or line and column in the active file.",
    shortcut: "Mod+G"
  },
  {
    id: "pr-review-mode",
    title: "Zero-Latency Pull Request Review",
    category: "Git",
    tag: "PR Review",
    content: "Run `px0 <pr-url>` from your terminal to review GitHub, GitLab, Gitea/Forgejo, or Bitbucket PRs directly with instant diffs and comments."
  },
  {
    id: "split-unified-diff",
    title: "Split & Unified Diff Views",
    category: "Git",
    tag: "Diff",
    content: "Press **`Cmd/Ctrl + D`** to toggle diff view against HEAD or merge base. You can switch seamlessly between Side-by-Side (Split) and Inline (Unified) modes.",
    shortcut: "Mod+D"
  },
  {
    id: "pr-scope-toggle",
    title: "Scoped Review: PR Changes vs Yours",
    category: "Git",
    tag: "PR Review",
    content: "When reviewing pull requests, use the scope toggle above the file tree to switch between **PR changes** (all files in the PR) and **Yours** (files you edited since checkout)."
  },
  {
    id: "inline-pr-comments",
    title: "Draft Comments & Batch Apply",
    category: "AI",
    tag: "PR Review",
    content: "Click on any line in a diff to write inline review comments. Click **Batch Apply** in the PR bar to dispatch your coding harness to resolve all feedback in one pass."
  },
  {
    id: "unpushed-commits",
    title: "Review Unpushed Commits",
    category: "Git",
    tag: "Git",
    content: "Click the **Git changes** toggle in the sidebar to review the local commits ahead of your upstream branch, inspecting diffs one commit at a time before pushing."
  },
  {
    id: "git-panel-sync",
    title: "Stage, Commit, Push & Pull",
    category: "Git",
    tag: "Git",
    content: "The Git panel lets you stage changes, write commit messages, push upstream, and pull updates. If branches diverge, your coding harness can automatically resolve the rebase."
  },
  {
    id: "ai-commit-messages",
    title: "Stage All & Commit with AI",
    category: "AI",
    tag: "Git",
    content: "In the Git panel, click **Stage all + Commit with AI** to have your local AI coding harness inspect staged changes and generate conventional commit messages."
  },
  {
    id: "quick-file-edit",
    title: "Quick In-Browser Editing",
    category: "Editing",
    tag: "Editor",
    content: "Need to tweak code or fix a comment? Press **`Cmd/Ctrl + E`** to enter direct edit mode, type your edits, and press **`Cmd/Ctrl + S`** to save to disk immediately.",
    shortcut: "Mod+E"
  },
  {
    id: "new-file-folder",
    title: "Create Files & Folders Quickly",
    category: "Editing",
    tag: "Explorer",
    content: "Use the **New File** and **New Folder** buttons at the top of the file explorer (or in the tree context menu) to create nested files without leaving the browser."
  },
  {
    id: "lsp-navigation",
    title: "Go to Definition & References",
    category: "Navigation",
    tag: "LSP",
    content: "Press **`F12`** (or `Cmd/Ctrl + Click`) on any symbol to jump to its definition. Press **`Shift + F12`** to view all symbol references across your project in the inspector.",
    shortcut: "F12"
  },
  {
    id: "call-trails",
    title: "Trace Call Trails",
    category: "Navigation",
    tag: "LSP",
    content: "Select any function or method and press **`Alt + Shift + H`** to explore incoming callers and outgoing callees in the Calls panel.",
    shortcut: "Alt+Shift+H"
  },
  {
    id: "symbol-outline",
    title: "File Symbol Outline",
    category: "Navigation",
    tag: "Symbols",
    content: "Open the **Symbols** tab in the right inspector (or press **`Cmd/Ctrl + Shift + O`**) to view functions, types, and constants in the active file with fast filtering.",
    shortcut: "Mod+Shift+O"
  },
  {
    id: "find-usages",
    title: "Find Usages of Selection",
    category: "Navigation",
    tag: "LSP",
    content: "Select any identifier in code and press **`Alt + U`** to instantly inspect all usages across the workspace in the References pane.",
    shortcut: "Alt+U"
  },
  {
    id: "copy-with-context",
    title: "Copy Code with Context for LLMs",
    category: "Selection",
    tag: "AI",
    content: "Select any lines of code and press **`Alt + A`** to copy the snippet annotated with file paths and line numbers, formatted for AI prompts. Press **`Alt + C`** to copy file and line refs.",
    shortcut: "Alt+A"
  },
  {
    id: "selection-context-menu",
    title: "Selection Actions & Context Menu",
    category: "Selection",
    tag: "Editor",
    content: "Right-click any selected text to reveal the action menu: copy formatted Markdown, copy reference, start an agent thread, or edit inline."
  },
  {
    id: "agent-threads",
    title: "Start an AI Agent Thread",
    category: "AI",
    tag: "Agent",
    content: "Select code and press **`Alt + T`** to start a multi-turn conversation anchored to that code. Changes can be watched live or dispatched to Claude Code, Gemini CLI, Cursor Agent, Antigravity, or Codex.",
    shortcut: "Alt+T"
  },
  {
    id: "markdown-mermaid",
    title: "Rendered Markdown & Mermaid Diagrams",
    category: "Viewer",
    tag: "Markdown",
    content: "Press **`Alt + M`** on Markdown files to toggle rendered previews with GitHub callout alerts, task lists, and full interactive Mermaid flowcharts and sequence diagrams.",
    shortcut: "Alt+M"
  },
  {
    id: "table-viewer",
    title: "Interactive CSV & TSV Table Viewer",
    category: "Viewer",
    tag: "Tables",
    content: "Opening `.csv` or `.tsv` files renders them in an interactive virtualized table with sortable columns, row numbering, and instant search."
  },
  {
    id: "image-inspector",
    title: "Pixel-Perfect Image Inspector",
    category: "Viewer",
    tag: "Assets",
    content: "When viewing images, press **`0`** to fit window, **`1`** for 1:1 actual size, **`b`** to toggle checkerboard transparency, and **`p`** to toggle pixelated smoothing."
  },
  {
    id: "vim-mode",
    title: "Modal Vim Keybindings",
    category: "Editor",
    tag: "Vim",
    content: "Prefer modal editing? Enable Vim mode in **Settings (`Cmd/Ctrl + ,`)** or click the **VIM** chip in the status bar for `hjkl` motions, visual selection, and Vim search commands."
  },
  {
    id: "color-themes",
    title: "14 Built-in Color Themes",
    category: "Interface",
    tag: "Theme",
    content: "Cycle through 14 themes (Catppuccin Mocha/Latte, Tokyo Night, Dracula, Gruvbox, Nord, Rosé Pine, GitHub Dark/Light) using the theme icon in the sidebar or via Command Palette."
  },
  {
    id: "custom-themes",
    title: "Custom CSS Themes",
    category: "Interface",
    tag: "Theme",
    content: "Create custom color themes by placing CSS files in `~/.px0/themes/`. px0 concatenates and serves them alongside built-in themes automatically."
  },
  {
    id: "word-wrap",
    title: "Toggle Word Wrap",
    category: "Editor",
    tag: "Editor",
    content: "Press **`Alt + Z`** to wrap long lines of code or text to fit within your current viewport width without horizontal scrolling.",
    shortcut: "Alt+Z"
  },
  {
    id: "review-workbench",
    title: "Multi-Workspace Workbench",
    category: "Workbench",
    tag: "Workbench",
    content: "Run `px0` with no arguments to launch the Review Workbench. Quickly reopen recent workspaces, pin favorite projects, or open new PR URLs from a single port."
  },
  {
    id: "remote-dev",
    title: "Zero-Setup Remote & Cloud Workspaces",
    category: "Terminal",
    tag: "Remote",
    content: "Run `px0 -host 0.0.0.0` on your remote VM, container, or server to review code locally over Tailscale, SSH port forward, or private LAN without remote agent daemons."
  },
  {
    id: "virtual-rendering",
    title: "Ultra-Fast Virtualized Rendering",
    category: "Performance",
    tag: "Performance",
    content: "px0 smoothly opens files with 400,000+ lines by mounting only visible rows into the DOM and releasing memory back to the OS after 15 seconds of inactivity."
  },
  {
    id: "settings-ui-json",
    title: "Settings UI & Direct JSON Editing",
    category: "Interface",
    tag: "Settings",
    content: "Press **`Cmd/Ctrl + ,`** to open Settings. Toggle between the searchable UI form and the raw `settings.json` editor with live validation and saving.",
    shortcut: "Mod+,"
  },
  {
    id: "all-shortcuts",
    title: "Keyboard Shortcuts Reference",
    category: "Navigation",
    tag: "Shortcuts",
    content: "Press **`?`** anywhere in px0 (when not focused on a text input) to view the cheat sheet of all navigation, editing, and review shortcuts.",
    shortcut: "?"
  }
];

export function escapeTipHtml(s) {
  if (!s) return '';
  return s.replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));
}

/**
 * Render lightweight, secure markdown for small tips.
 * Safely parses bold, italic, code spans, links, and <kbd> tags without escaping the <kbd> elements.
 */
export function renderTipMarkdown(src) {
  if (!src) return '';
  const codes = [];
  const kbds = [];

  // 1. Extract <kbd>...</kbd> tags (case-insensitive) before HTML escaping
  let s = src.replace(/<kbd>([\s\S]*?)<\/kbd>/gi, (_, text) => {
    kbds.push('<kbd class="tip-kbd">' + escapeTipHtml(text.trim()) + '</kbd>');
    return '%%PXKBD' + (kbds.length - 1) + '%%';
  });

  // 2. Extract code blocks / inline code
  s = s.replace(/(`+)([\s\S]*?[^`])\1(?!`)/g, (_, q, code) => {
    codes.push('<code class="tip-code">' + escapeTipHtml(code.trim()) + '</code>');
    return '%%PXCODE' + (codes.length - 1) + '%%';
  });

  // 3. Escape remaining raw HTML
  s = escapeTipHtml(s);

  // 4. Bold, italic, strikethrough
  s = s.replace(/(\*\*\*|___)([^\n]+?)\1/g, '<strong><em>$2</em></strong>');
  s = s.replace(/\*\*([^*\n]+?)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/(^|[^a-zA-Z0-9])__([^\n]+?)__(?![a-zA-Z0-9])/g, '$1<strong>$2</strong>');
  s = s.replace(/\*([^*\n]+?)\*/g, '<em>$1</em>');
  s = s.replace(/(^|[^a-zA-Z0-9])_([^\n]+?)_(?![a-zA-Z0-9])/g, '$1<em>$2</em>');
  s = s.replace(/~~(.+?)~~/g, '<del>$1</del>');

  // 5. Keyboard shortcuts in braces: e.g. {Mod+P}
  s = s.replace(/\{([A-Za-z0-9+]+)\}/g, (_, k) => {
    return `<kbd class="tip-kbd">${escapeTipHtml(k)}</kbd>`;
  });

  // 6. Links: [text](url)
  s = s.replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, (_, text, url) => {
    return `<a href="${escapeTipHtml(url)}" target="_blank" rel="noopener noreferrer" class="tip-link">${text}</a>`;
  });

  // 7. Restore inline codes and kbds
  s = s.replace(/%%PXCODE(\d+)%%/g, (_, idx) => codes[+idx]);
  s = s.replace(/%%PXKBD(\d+)%%/g, (_, idx) => kbds[+idx]);

  return s;
}
