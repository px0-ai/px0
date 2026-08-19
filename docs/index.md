# px0

A local-first CLI where everything the system does is a workflow.
Everything it knows lives in two folders inside a plain directory
(`~/.px0` by default): `guidelines/` for how you work, `knowledge/` for
what you've read and kept. Workflows are Markdown files you can read,
edit, and run manually or on a schedule. Nothing is versioned by git --
px0 keeps its own history so the store works as a bare directory with
nothing else installed alongside it.

## Hello world

```shell
px0 init
echo "https://example.com/some-post" | px0 run summarize --stdin
```

`px0 init` scaffolds the store with a handful of starter workflows and
guidelines. `summarize` is one of them: it takes a URL, a local file, or
raw pasted text on stdin and summarizes it -- no external connection
required, so it's the fastest way to see px0 do something real.
`pr-precheck` is another: it reads a diff on stdin, checks it against
the code-review guidelines, and prints any violations.

```shell
px0 list workflows
px0 runs list
```

## Model backend

px0 shells out to a coding agent CLI in non-interactive mode as its
model backend, reusing that CLI's own auth, model choice, and rate
limits -- there is no direct-API backend. `claude -p` is the default;
`px0 init --harness <name>` picks the right invocation for `claude`,
`gemini`, `pi`, or `opencode` instead. Any other command works too, set
directly as `model.harness_cmd` in `config.toml`.

## Skills

`px0 skills` manages agent skills and capabilities. It operates in two ways:

1. **Proxy for `npx skills`**: For finding, installing, and managing community skills from the open ecosystem, `px0 skills` acts as a direct proxy for the [`npx skills`](https://github.com/vercel-labs/skills) CLI utility (`skills@latest`). It executes commands in global mode (`-g`) and synchronizes installed skills with your store's `.px0/skills.json` (mirroring `~/.agents/.skill-lock.json`).

   ```shell
   # Search available skills
   px0 skills search "linear"
   
   # Add / install a skill
   px0 skills add composio/github
   
   # List installed skills
   px0 skills list
   
   # Check skill health and compatibility
   px0 skills check
   
   # Update installed skills
   px0 skills update
   
   # Remove a skill
   px0 skills remove <skill-name>
   ```

   *(Note: Node.js and `npx` are prerequisites for running community skills commands).*

2. **Compiling guidelines to skill bundles (`px0 skills build`)**: Compiles your local `guidelines/*.md` into agent-facing skill bundles (`skills/<name>/SKILL.md`) with YAML frontmatter. If the configured model harness is Claude Code, `px0 skills build` also creates symlinks into `~/.claude/skills/px0-<name>` so your guidelines automatically load during interactive coding sessions.

