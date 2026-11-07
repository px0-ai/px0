# Code Folding

Click a gutter chevron to collapse a function, class, conditional, loop, or other
multiline block. The opening line stays visible with a hidden-line count. Click
the chevron or count to expand it. Nested blocks remember their collapse state
when their parent is reopened.

The command palette (`Cmd/Ctrl+Shift+P`) offers **Fold Block**, **Unfold Block**,
**Fold All**, and **Unfold All**. In Vim mode, use `zc`, `zo`, `za`, `zM`, and `zR`.
Arrow/Vim movement skips hidden lines. Find results, definitions, outline entries,
history, and Go to Line reveal their target before navigating.

Folding affects only the source viewer. Diffs, rendered previews, and the direct
editing textarea keep their existing behavior. Source bytes and source line
numbers never change. Copying a selection across a fold includes the hidden
source, and Select All includes the complete file.

Supported language families are Go, JavaScript/TypeScript, Python, C/C++, Java,
JSON, CSS/SCSS, HTML, and XML. Detection uses bounded lexical scanning rather than a full
language parser; unsupported or malformed constructs can have fewer fold points.
Language servers are not required. JavaScript regexes and template interpolation,
C++ raw strings, Java text blocks, and Python multiline strings/signatures are
handled separately from structural delimiters.
Go's multiline `import (...)`, `const (...)`, `var (...)`, and `type (...)`
declaration groups are also foldable.

HTML (`.html`, `.htm`) and XML elements fold from their opening tag through their
matching closing tag. Multiline comments and CDATA sections also fold. HTML tag
names ignore ASCII case; void elements such as `img`, `br`, and `input` do not
open blocks. Tags inside `script`, `style`, `textarea`, and other raw-text
elements are ignored until the matching closing tag. Quoted attributes can span
lines and contain angle brackets. A closing line shared with another tag or text
stays visible.

XML matching preserves case and namespace prefixes. XML-family extensions include
`.xml`, `.xhtml`, `.xht`, `.svg`, `.mathml`, `.mml`, `.xsl`, `.xslt`, `.xaml`,
`.axml`, `.fxml`, `.plist`, `.resx`, `.xsd`, `.wsdl`, `.rss`, `.atom`, `.xlf`, and
`.xliff`. HTML containing inline SVG/MathML preserves foreign-element case and
recognizes HTML children inside SVG `foreignObject`.
Standalone `.svg` files currently open in the image preview, which has no source
fold controls; inline SVG within HTML and SVG markup in XML source use tag folding.

This scanner uses explicit closing tags; it does not reconstruct omitted HTML
end tags or expand XML entities. Self-closing elements have no body to fold.
Vue, Svelte, Astro, JSX/TSX tags, and MDX need additional template syntax support;
their tag folding is not included (JSX/TSX still have JavaScript block folding).

Fold controls load after the source opens. Detection is limited to files up to
8 MiB, 8,192 ranges, and 256 levels of lexical nesting. Files exceeding the byte
or depth limit remain readable without folding. A range limit may yield a subset
of blocks. These limits protect responsiveness and memory usage.

Collapse state belongs to an open tab. Closing the tab or changing its source
discards the state. Switching tabs and refreshing unchanged source retain it.
