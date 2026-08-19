# Skills and agent bundles

Agent skills give your coding agent CLI specialized knowledge, workflows,
and tool integrations. In `px0`, the `px0 skills` command helps you manage
community skills from the open ecosystem and compile your own local
guidelines into agent-facing skill bundles.

## 1. Prerequisites: Node.js and npx

`px0 skills` uses the [`npx skills`](https://github.com/vercel-labs/skills)
utility (`skills@latest`) under the hood to manage community skills. Node.js
(which bundles `npx`) is required for this functionality.

If you don't already have Node.js and `npx` installed:

```shell
# macOS (Homebrew)
brew install node

# Ubuntu / Debian
sudo apt update && sudo apt install -y nodejs npm

# Node Version Manager (nvm)
nvm install --lts
```

You can verify your installation by running:

```shell
npx --version
```

## 2. Managing community skills (proxy to `npx skills`)

`px0 skills` acts as a proxy for the `npx skills` command line tool. Any
subcommand you pass to `px0 skills` (except `build`) is forwarded directly to
`npx --yes skills@latest <args> -g`.

`px0` automatically maintains your installed skill state in your store's
`.px0/skills.json` (synchronized with `~/.agents/.skill-lock.json`).

### Search for skills

To search for community skills matching keywords:

```shell
px0 skills search "linear"
px0 skills search "github"
```

*(This is identical to running `npx skills search <query>`).*

### Install a skill

To add a new skill to your agent's global environment:

```shell
px0 skills add composio/github
px0 skills add vercel/nextjs
```

*(This is identical to running `npx skills add <skill>`).*

### List installed skills

To see all installed skills and their origins:

```shell
px0 skills list
```

*(This is identical to running `npx skills list` or `npx skills ls`).*

### Check skill compatibility and status

To check whether installed skills are valid and properly configured:

```shell
px0 skills check
```

*(This is identical to running `npx skills check`).*

### Update installed skills

To update all installed skills to their latest versions:

```shell
px0 skills update
```

*(This is identical to running `npx skills update`).*

### Remove a skill

To remove a skill that you no longer need:

```shell
px0 skills remove <skill-name>
```

*(This is identical to running `npx skills remove <skill-name>`).*

## 3. Compiling guidelines into skill bundles (`px0 skills build`)

In addition to proxying `npx skills`, `px0` includes a built-in compiler for
your local guidelines:

```shell
px0 skills build
```

When you run `px0 skills build`:

1. **Extracts frontmatter**: For each Markdown file in `guidelines/` (excluding
   any files in `guidelines/work/`), it inspects the section headings and
   generates a `SKILL.md` file containing a `name` and derived `description`.
2. **Builds bundles**: Writes each bundle into `~/.px0/skills/<name>/SKILL.md`.
3. **Claude Code integration**: If your configured model harness is Claude Code
   (`claude -p`), `px0` creates symlinks in `~/.claude/skills/px0-<name>`
   pointing to the compiled bundles. This allows Claude Code to automatically
   discover and load your guidelines during interactive terminal sessions.
4. **Pruning**: Automatically cleans up orphaned skill directories and symlinks
   if their corresponding guideline source file was deleted or renamed.

```shell
$ px0 skills build
built skills/code-review/common.md
built skills/code-review/go.md
built skills/code-review/python.md
built skills/commit-messages.md
```

## Summary

- Use `px0 skills <command>` as a proxy to `npx skills` to discover, install, update, and remove community agent skills.
- Use `px0 skills build` to compile your local `guidelines/` into agent bundles and symlink them for Claude Code.
