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

- All components are written in **Rust** and live in one Cargo workspace in this repository
  (see `dec-languages-rust` in `docs/architecture/09-decisions.arc42.md`).
- Shared types and the protocol (Protobuf, Connect RPC) belong in the shared core, not in a
  single component.

## Architecture documentation

- The architecture lives in `docs/architecture/` as arc42 chapters (`*.arc42.md`).
- Use the arc42 CLI (`npx @doctc/arc42`). Before you write or change a chapter, run
  `arc42 guide chapter <n>` and `arc42 explain <type>`. Do not rely on remembered syntax.
- Run `npx @doctc/arc42 --dir docs/architecture validate` before you commit. Fix all errors and
  warnings. Explain any remaining hints.
- Do not invent facts. Mark open questions with `OPEN:`. Record where each fact comes from in
  `docs/architecture/architecture-evidence.md`.
- Architecture is about decisions and trade-offs. Align with the maintainer before you record a
  decision as `accepted`. Your own proposals start as `proposed`.
