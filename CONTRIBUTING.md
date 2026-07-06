# Contributing

Thanks for taking the time to improve px0. Keep changes focused, tested, and easy to review.

## Before opening a pull request

- Keep the pull request scoped to one fix or feature.
- Add or update tests for every behavior change.
- Update the OpenAPI files under `docs/openapi/` when API behavior, validation, or response shapes change.
- Regenerate the bundled OpenAPI file after spec changes:

```bash
make spec-bundle
```

- Run the project checks before pushing:

```bash
make test
make check
```

Integration tests skip automatically when the local test database is unavailable.

## Commit messages

Use the `ape-commit` skill from [arpitbbhayani/ape-skills](https://github.com/arpitbbhayani/ape-skills/tree/master/ape-commit) before pushing commits.

Install it with:

```bash
npx skills add https://github.com/arpitbbhayani/ape-skills/tree/master/ape-commit
```

Commit messages should follow this format:

```text
<short imperative summary>

- <imperative change>
- <imperative change>
- <imperative change>
```

Guidelines:

- Keep the summary short, specific, and under 72 characters.
- Use imperative mood, such as `Fix API key validation`, not `Fixed` or `Fixes`.
- Leave one blank line between the summary and bullets.
- Keep each bullet to one concrete change.
- Do not include generated-by or co-author attribution lines.
