# Documentation site

The documentation site is built with [Starlight](https://starlight.astro.build). It is live at
<https://mrsimpson.github.io/agentic-office/>. The decision is `dec-docsite`; the architecture
only says what the site is. This file says how it works.

## Commands

| Command | What it does |
|---|---|
| `make docs` | Builds the programs, builds the site into `docs/site/dist` and runs all checks |
| `make docs-serve` | Serves the built site at `http://localhost:4321/agentic-office/` |

## Where the pages come from

- `scripts/prepare.mjs` reads `USER_GUIDE.md`, `README.md`, `tests/README.md` and `AGENTS.md` and
  writes the pages. The user guide becomes one page per chapter with the same anchors as on GitHub.
  No generated page is committed.
- The command reference is made from the output of `office help`, `office help <command>` and
  `office-hub -h`. The list of commands comes from `office help`, so no command can be missing.
  The build of the site therefore builds the programs, and with them whisper.cpp.
- The architecture is the output of `arc42 build`, added to `dist/architecture/` by
  `scripts/architecture.mjs`, with a link back to the site.
- `agent/llms.txt` and `agent/agent-setup.md` are the index and the setup guide for agents. The
  build writes them to the root of the site.

## Checks

- A broken link or anchor fails the build (`starlight-links-validator`).
- `scripts/check-agent-docs.mjs` fails when the setup guide names a command or flag that the
  programs do not have, when a link in `llms.txt` or in the guide leads nowhere, or when
  `llms.txt` has no title, no summary or does not name the guide first.
- The CI job "Docsite" runs `make docs` when the docs, the help or the site change. It reuses
  the cached whisper.cpp build of the test jobs.

## Publishing

`.github/workflows/docs.yml` publishes on every push to `main` that changes what the site shows.
`scripts/publish-docs.sh` writes the site to the root of the branch `gh-pages`. It keeps
`.nojekyll` and every folder `pr-*/`, because the architecture review of a pull request
(`.github/workflows/architecture-review.yml`) publishes its review site there. The script refuses a
site that contains a `pr-*` folder and retries when a push is rejected.

## Versions

The versions of Astro, Starlight and the link validator are pinned in `package.json` and
`package-lock.json`. The version of the arc42 CLI is pinned in four places: the `Makefile`,
`tests/architecture/check.go`, `.github/workflows/architecture-review.yml` and `site-config.mjs`.
Change them together.
