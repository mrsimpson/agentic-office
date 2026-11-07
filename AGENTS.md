# AGENTS.md

Guidance for agents working in this repository.

## Language

- Write all architecture documents and all artifacts in **English**. This covers code, comments,
  commit messages, issues, pull requests, schemas and documentation.
- Write in **plain language** as defined by **ISO 24495-1** (Plain language — Governing principles
  and guidelines):
  - Write for the reader: know who reads it and what they need to do with it.
  - Make it easy to find: clear headings, the most important information first.
  - Make it easy to understand: short sentences, active voice, common words, one idea per sentence.
    Define terms that readers might not know (see the glossary in chapter 12).
  - Make it easy to use: concrete examples, lists for steps and options, no filler.
- Conversations with the maintainer may be in German. The written artifacts stay in English.

## Code

- All components are written in **Go** and live in this monorepo with one toolchain
  (see `dec-languages` in `docs/architecture/09-decisions.arc42.md`). The only extra tool is the
  C/C++ compiler that whisper.cpp needs through cgo.
- The code follows the building blocks of chapter 5 (`concept-module-boundaries`). Every building
  block and every interface has exactly one path. An interface lies inside the path of its
  provider and owns its types, including its Protobuf contract. There is no shared or common
  package.
- A building block may only use interfaces it provides or lists in `requires`. To add a
  dependency, change the model first. `make arch` checks this and fails on every violation.
- **No unit tests.** Test each building block as a black box through its interfaces (contract
  tests). Use only the provided interfaces; do not reach into the inside of a block. End-to-end
  tests combine real blocks. See `dec-contract-tests`.
- End-to-end tests (`make e2e`) check behaviour only and never fail on timing. Timing criteria
  belong in the performance tests (build tag `performance`, `make performance`), which run on
  the reference host. See `dec-performance-tests`.

## Workflow

- All work is tracked in GitHub issues in `mrsimpson/agentic-office`. One issue per work package.
  The first stage is tracked in one epic issue with sub-issues.
- Work on one issue at a time, in the order of the epic. Commit with `Closes #<n>` and push to
  `main` only when the build and all tests pass.
- When you hit a problem during implementation (a bug, a gap in the architecture, a blocked
  dependency), open a new issue yourself: describe the problem, what you tried, and what you need.
  Link it to the issue you were working on. Then continue with other work if you can.
- Work without waiting for a human. If an action needs a permission or a credential you do not
  have, do not wait: mock the external API or service, open an issue with the label `mocked`
  that says what is mocked and what the real thing needs, and continue.
- Install tools only in user space, never with root.
- We are before go-live: no backward compatibility. Do not keep old names, files, flags, formats,
  forwarding packages or migration notes. When you change something, change every user of it and
  remove the old form.
- Do not change accepted architecture decisions on your own. If the code shows that the
  architecture is wrong or incomplete, open an issue with the label `architecture`.

## Architecture documentation

- The architecture lives in `docs/architecture/` as arc42 chapters (`*.arc42.md`).
- Use the arc42 CLI (`npx @doctc/arc42`). Before you write or change a chapter, run
  `arc42 guide chapter <n>` and `arc42 explain <type>`. Do not rely on remembered syntax.
- Run `make arch` before you commit. It validates with `--strict`: fix all errors, warnings and
  hints, or suppress a hint with an ignore directive and a reason that you record in the evidence.
- Keep implementation details out of the architecture: no file paths, field types, library calls
  or lint rules. Describe responsibilities, contracts and qualities. Implementation details
  belong in the code and its README files.
- Do not invent facts. Mark open questions with `OPEN:`. Record where each fact comes from in
  `docs/architecture/architecture-evidence.md`.
- We are in an early planning phase. Change decisions in place. Do not mark them as `superseded`
  or `deprecated` until the maintainer says the architecture is baselined.
- Architecture is about decisions and trade-offs. Align with the maintainer before you record a
  decision as `accepted`. Your own proposals start as `proposed`.
