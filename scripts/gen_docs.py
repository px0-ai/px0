#!/usr/bin/env python3
"""Generates docs/reference.md from the docstrings in the px0 package.

Walks every module under px0/, and for each module, class, and top-level
function emits its signature and docstring as Markdown. Run it after
changing any docstring to keep docs/reference.md in sync:

    python scripts/gen_docs.py
"""

import ast
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
PACKAGE_DIR = ROOT / "px0"
OUTPUT_PATH = ROOT / "docs" / "reference.md"


def format_args(args: ast.arguments) -> str:
    """Renders an ast.arguments node back into a plain parameter list string."""
    parts = []
    defaults = [None] * (len(args.args) - len(args.defaults)) + list(args.defaults)
    for arg, default in zip(args.args, defaults):
        piece = arg.arg
        if arg.annotation is not None:
            piece += f": {ast.unparse(arg.annotation)}"
        if default is not None:
            piece += f" = {ast.unparse(default)}"
        parts.append(piece)
    if args.vararg:
        parts.append(f"*{args.vararg.arg}")
    for arg, default in zip(args.kwonlyargs, args.kw_defaults):
        piece = arg.arg
        if arg.annotation is not None:
            piece += f": {ast.unparse(arg.annotation)}"
        if default is not None:
            piece += f" = {ast.unparse(default)}"
        parts.append(piece)
    if args.kwarg:
        parts.append(f"**{args.kwarg.arg}")
    return ", ".join(parts)


def signature_of(node: ast.FunctionDef | ast.AsyncFunctionDef) -> str:
    """Builds a `name(args) -> return_type` string for a function/method node, omitting the arrow if there's no return annotation."""
    prefix = "async def" if isinstance(node, ast.AsyncFunctionDef) else "def"
    ret = f" -> {ast.unparse(node.returns)}" if node.returns else ""
    return f"{prefix} {node.name}({format_args(node.args)}){ret}"


def render_function(node: ast.FunctionDef | ast.AsyncFunctionDef, heading_level: int) -> list[str]:
    """Renders one function/method as a heading, its signature in a code fence, and its docstring."""
    lines = [f"{'#' * heading_level} `{node.name}`", "", f"```python\n{signature_of(node)}\n```", ""]
    doc = ast.get_docstring(node)
    if doc:
        lines.append(doc.strip())
        lines.append("")
    return lines


def render_module(path: Path) -> list[str]:
    """Parses one .py file and renders its module docstring plus every top-level function and class (with methods) as Markdown."""
    tree = ast.parse(path.read_text(), filename=str(path))
    module_name = f"px0.{path.stem}"
    lines = [f"## `{module_name}`", ""]

    doc = ast.get_docstring(tree)
    if doc:
        lines.append(doc.strip())
        lines.append("")

    # only top-level defs/classes -- nested helpers are treated as implementation detail
    for node in tree.body:
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
            lines += render_function(node, heading_level=3)
        elif isinstance(node, ast.ClassDef):
            lines.append(f"### `class {node.name}`")
            lines.append("")
            class_doc = ast.get_docstring(node)
            if class_doc:
                lines.append(class_doc.strip())
                lines.append("")
            for sub in node.body:
                if isinstance(sub, (ast.FunctionDef, ast.AsyncFunctionDef)):
                    lines += render_function(sub, heading_level=4)

    return lines


def main() -> None:
    """Generates docs/reference.md from every .py module in the px0 package, overwriting any existing file."""
    modules = sorted(p for p in PACKAGE_DIR.glob("*.py") if p.name != "__init__.py")

    out = ["# px0 API reference", "", "Generated from docstrings by `scripts/gen_docs.py`. Do not edit by hand.", ""]
    for path in modules:
        out += render_module(path)

    OUTPUT_PATH.parent.mkdir(parents=True, exist_ok=True)
    OUTPUT_PATH.write_text("\n".join(out).rstrip() + "\n")
    print(f"wrote {OUTPUT_PATH} ({len(modules)} modules)")


if __name__ == "__main__":
    main()
