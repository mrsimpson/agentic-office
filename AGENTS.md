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
- Shared types and the protocol (Protobuf, Connect RPC) belong in the shared core, not in a
  single component.
- **No unit tests.** Test each building block as a black box through its interfaces (contract
  tests). Use only the provided interfaces; do not reach into the inside of a block. End-to-end
  tests combine real blocks. See `dec-contract-tests`.

## Architecture documentation

- The architecture lives in `docs/architecture/` as arc42 chapters (`*.arc42.md`).
- Use the arc42 CLI (`npx @doctc/arc42`). Before you write or change a chapter, run
  `arc42 guide chapter <n>` and `arc42 explain <type>`. Do not rely on remembered syntax.
- Run `npx @doctc/arc42 --dir docs/architecture validate` before you commit. Fix all errors and
  warnings. Explain any remaining hints.
- Keep implementation details out of the architecture: no file paths, field types, library calls
  or lint rules. Describe responsibilities, contracts and qualities. Implementation details
  belong in the code and its README files.
- Do not invent facts. Mark open questions with `OPEN:`. Record where each fact comes from in
  `docs/architecture/architecture-evidence.md`.
- We are in an early planning phase. Change decisions in place. Do not mark them as `superseded`
  or `deprecated` until the maintainer says the architecture is baselined.
- Architecture is about decisions and trade-offs. Align with the maintainer before you record a
  decision as `accepted`. Your own proposals start as `proposed`.
