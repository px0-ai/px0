# Syntax Highlighting & Language Support

px0 delivers fast, accurate syntax highlighting across roughly 280 programming languages, markup formats, and configuration files. Powered by an optimized Chroma lexing engine and viewport windowing, it renders highlighted code with zero typing lag or scrolling stutters.

---

## Overview & Core Purpose

Reading code is the predominant activity during code reviews, security audits, and agent pairing sessions. Inaccurate or plain-text rendering increases eye strain and makes understanding complex control flow significantly harder. At the same time, traditional client-side syntax highlighters often block the browser main thread or consume hundreds of megabytes of RAM when tokenizing large files.

px0 provides full native syntax highlighting for almost every language in modern use. By utilizing an intelligent viewport windowing strategy, px0 only highlights lines as they scroll near the visible viewport. This enables instant rendering and silky smooth 60fps scrolling whether viewing a 50-line shell script or a 300,000-line generated database migration.

---

## Supported Languages & Formats

With built-in support for approximately 280 languages, px0 highlights virtually every language, format, and dialect out of the box with zero plugins required:

- **Systems & Backend**: Go, Rust, C, C++, Zig, C#, Java, Kotlin, Swift, Scala, D, Nim, Fortran, Assembly.
- **Web & Scripting**: TypeScript, JavaScript, Python, Ruby, PHP, Lua, Perl, Shell (Bash/Zsh/Fish), PowerShell.
- **Functional Languages**: Haskell, OCaml, Elixir, Erlang, Clojure, F#, Common Lisp, Scheme, Racket.
- **Data & Configuration**: JSON, YAML, TOML, XML, CSV, INI, Dockerfile, Makefile, HCL/Terraform, Protobuf.
- **Web Templates & Styling**: HTML, CSS, SCSS, Sass, Less, GraphQL, Vue, Svelte, JSX, TSX, Jinja.
- **Scientific & Hardware**: Julia, R, MATLAB, SQL, Verilog, VHDL, CUDA.
- **Documentation & Notes**: Markdown, LaTeX, AsciiDoc, ReStructuredText, Diff/Patch.

---

## Key Capabilities

- **Viewport Windowing**: Files are tokenized in small viewport-sized chunks on demand. Memory footprint remains virtually flat regardless of file length.
- **Dual-Tier Highlighting**: Lines entering the viewport receive instantaneous structural tokenization followed by asynchronous lexical refinement, ensuring scrolling never waits on token computation.
- **Consistent Visual Design**: The same color token classes apply consistently across the code viewer, the side-by-side git diff viewer, and Markdown fenced code blocks.
- **Bracket Pair Colorization**: Nested parentheses, brackets, and curly braces (`()`, `[]`, `{}`) are color-coded in alternating rainbow hues, making deeply nested closures and argument lists effortless to balance visually.
- **Word Occurrence Highlighting**: Selecting or placing the caret on an identifier automatically highlights all other occurrences of that symbol in the visible buffer, helping you trace variable usage without manual searching.
- **Active Line Highlighting**: Subtly highlights the background of the row containing the active cursor position to keep your eyes oriented during reading.

---

## Developer Workflows & Practical Value

### Reading Multi-Language Repositories
Polyglot repositories combining Go services, React/TypeScript frontends, Python analytics scripts, Terraform infrastructure, and Docker configurations render with rich native highlighting instantly without installing language packs or extensions.

### Instant Opening of Massive Files
Files that would crash or freeze conventional web-based editors (such as huge SQL dumps or multi-megabyte JSON fixtures) open in px0 in milliseconds because only the visible lines are highlighted.

---

## Configuration & Tuning

Configure syntax highlighting features in Settings (`Cmd/Ctrl+,`):

- **Editor: Bracket Pair Colorization** (`editor.bracketPairColorization`): Enable or disable rainbow bracket pairing (defaults to `true`).
- **Editor: Occurrences Highlight** (`editor.occurrencesHighlight`): Automatically highlight matching words across the file (defaults to `true`).
- **Editor: Render Line Highlight** (`editor.renderLineHighlight`): Choose `"line"` highlight or `"none"`.

---

## Technical Architecture Deep Dive

For an explanation of the Chroma lexer integration, LRU token caching, viewport windowing (`hlChunk = 1000`), and panic recovery fallbacks, see [Windowed Syntax Highlighting Internals](../internals/syntax-highlighting.md).
