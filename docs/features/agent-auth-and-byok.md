# Harness Auth & Bring Your Own Key

How px0 decides which credentials a coding harness runs with, and how you sign in
or supply your own API key.

- Design, endpoints, and rationale: [`docs/internals/agent-editing.md` §9](../internals/agent-editing.md)
- Runnable proof: [`scripts/poc-agent-auth.sh`](../../scripts/poc-agent-auth.sh)

---

## The short version

px0 does not implement OAuth. Each coding harness already knows how to
authenticate itself, and the credential belongs to that tool — a token refreshed
on its own schedule, stored in its own format, revocable from its own CLI. So
px0 **delegates sign-in**: it runs the harness's real login command and shows you
what it printed.

What px0 adds is the other half, for the harnesses that would rather be handed a
key than run an OAuth dance, plus an honest account of where each harness stands.

---

## Where to find it

Open the harness picker the same way you start any edit — select some code and
press `Alt+E`, or right-click. Every harness now carries a badge:

| Badge | Meaning |
| :--- | :--- |
| **Signed in** | The harness left a credential file behind, so a login succeeded. |
| **API key** | No saved login, but a key px0 can hand it. |
| **Not signed in** | Nothing found, and there is something to do about it. |
| **Unknown** | px0 cannot tell — see below. |
| **Local** | It runs against a local runtime and needs no credential. |

Selecting a harness expands a panel beneath it with the controls for that
harness.

### Unknown is a real answer

Some harnesses (`claude`, `gemini`, `qwen`, `agy`) authenticate on first use and
keep their credential in a format px0 deliberately does not open. For those the
badge says **Unknown** rather than **Not signed in**.

That is not a gap in the reporting — it is a decision. px0 never parses a token
store, so telling you that you are signed out when you are actually signed in
would send you off to authenticate again for a session that already works. When
px0 cannot tell, it says so. The harness itself will complain if it truly is not
signed in, in its own output, where you can act on it.

---

## Signing in

If a harness has a login subcommand, the panel offers **Sign in**. px0 runs it
and streams its output, so you see the real flow — a device code, a browser
handoff, a callback URL — exactly as that tool intends. Harnesses with a `logout`
also get **Sign out**.

Nothing about the token exchange happens inside px0. There is no access token for
px0 to store, refresh, or leak.

If a harness has no login subcommand, the panel says so and points you at a
terminal. There is no button that cannot work.

---

## Bringing your own key

For harnesses built around an API key, the panel offers a key field. Paste a key
and it is stored in:

```
$XDG_CONFIG_HOME/px0/credentials.json     # when XDG_CONFIG_HOME is set
~/.px0/credentials.json                   # otherwise
```

alongside `settings.json` and never inside a workspace. The file is written
`0600`, through a temporary file and a rename, so an interrupted write cannot
leave a half-written secret briefly readable. Clearing the last key removes the
file.

Supported providers, under the environment variable each one already uses:

| Provider | Variable | Also accepted |
| :--- | :--- | :--- |
| Anthropic | `ANTHROPIC_API_KEY` | |
| OpenAI | `OPENAI_API_KEY` | |
| Google | `GEMINI_API_KEY` | `GOOGLE_API_KEY` |
| GitHub | `GITHUB_TOKEN` | `GH_TOKEN` |
| xAI, Groq, OpenRouter, Mistral, DeepSeek | as documented | |
| Ollama | none needed | |

### You do not have to store anything

If you already export one of those variables, px0 picks it up and a stored key is
unnecessary. A key you set yourself in your own environment always wins over one
px0 is holding — you set it deliberately.

If the key is short enough that masking it would reveal most of it, px0 shows
nothing at all rather than a partial value.

### How the key reaches the harness

As an environment variable in the child process, the way a shell would hand one
over. It is never appended to `argv`, where any process listing on the machine
could read it, and never written to a job log.

---

## The one setting worth understanding

Each harness has a **Credentials** choice, and the default is deliberately
cautious:

| Setting | What happens |
| :--- | :--- |
| **Harness default** | A key is injected only for harnesses with no subscription login to fall back on. |
| **Use my sign-in** | No key is ever injected. The harness uses its own login. |
| **Use my API key** | Whatever key px0 can resolve is passed on every run. |

Why not always inject? Because a working Claude subscription that silently starts
billing a different account is a failure you would not notice until an invoice
arrived. So a subscription-driven harness is never handed a key unless you ask
for that harness, explicitly.

The choice is remembered per harness in `settings.json` under `authModes`.

Not every harness offers every choice. A tool with no subscription login has
nothing for "use my sign-in" to override, so the option is simply not shown.

---

## If a command template is your harness

Selecting a custom command template — any `binary … {prompt}` — keeps the
behaviour you would expect: the harness inherits your environment unchanged, px0
holds no credentials for it, and the picker describes nothing on its behalf,
because px0 knows nothing about how an arbitrary command authenticates.

---

## Try it

```bash
scripts/poc-agent-auth.sh
```

Builds px0, runs it against a throwaway workspace and config home, and asserts
each property described above over HTTP. Requires only `go`, `curl` and `grep`.
It never touches your real `~/.px0`.
