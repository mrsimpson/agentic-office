---
name: office-memory
description: How to read and write the shared memory of a team in the agentic office (its wiki of notes, minutes, decisions and documents). Use it whenever you look up what the team knows, record something for the team, or bring the results of a meeting into the wiki.
---

# The memory of the team

The agentic office keeps the memory of a team as one wiki: every artifact of the tenant, linked
to each other. You help to keep it. This skill is the same for every team and comes with the
office. Read it, but never change it.

For the commands, read `office help` and `office help <command>`. To connect, set up agents or
run meetings, use the skill office-assistant.

## Three kinds of artifacts

- **Sources** are memories of what happened: minutes, decisions and notes. They are written once
  and **never change**. To correct one, write a new note that names the old one.
- **Pages** are documents in Markdown. Humans and agents write and change them. They hold what
  the team knows now, for example everything about one customer, project or topic.
- **This skill** holds the rules. Team-specific rules come from your role and from the pages you
  find.

## Find what the team knows

1. Read in the local clone of the tenant if you have one (`office sync init`, `office sync
   follow`). Search it with your own tools, for example `grep -ril "acme" .`.
2. Without a clone, use `document_list`, `document_get`, `note_list`, `decision_list` and
   `minutes_list` (or the same commands of the `office` CLI).
3. Start at the overview pages of an area and follow the links. Search for the name and for
   other words that mean the same, also in other languages.
4. Look at the front matter of a page before you trust it: a page with `status: reviewed` and a
   recent review is more reliable than a `draft`.

## Record a source

- **Note** (`note_add`): one observation or fact, complete on its own, so that a reader
  understands it without the meeting. Add a note when something is worth remembering and no
  other type fits.
- **Decision** (`decision_record`): only what the humans actually decided, in one clear sentence.
- **Minutes** (`minutes_update`): only if your role asks for it.
- Never put a sentence that is off the record into any artifact, and never repeat it.

## Write a page

Create a page with `document_create` and change it with `document_change`. A change needs the
version that you read (`--base`). If the hub refuses it because someone changed the page
meanwhile, read it again, merge your change, and try again. Never overwrite blindly.

Start every page with front matter:

```markdown
---
title: ACME GmbH
summary: Customer since 2025; contract, contacts and open topics.
tags: [customer]
sources:
  - minutes/000012.yaml
  - notes/000031.yaml
status: draft
owner: <participant ID of the human who owns the page>
reviewed:
  - id: <participant ID of the reviewer>
    date: 2026-11-19
---

# ACME GmbH

The price is 40 EUR per seat ([minutes of 12 November](../../minutes/000012.yaml)).
```

- **title** and **summary**: one line each. The portal lists pages by them.
- **tags**: use the tags that similar pages already use.
- **sources**: the path of every source the page draws on. Also link the source next to the
  statement that comes from it.
- **status**: `draft`, `reviewed` or `outdated`. You write `draft`. Set `outdated` when a newer
  page replaces it, and link the newer page at the top.
- **owner** and **reviewed**: set by humans. Add a review only when a human asks you to record
  their review. Never add yourself.
- **Links**: plain relative Markdown links to other files of the tenant. Check that each target
  exists.

Where pages live: follow the structure you find. If the tenant has none yet, use
`wiki/<area>/<name>.md`, for example `wiki/customers/acme.md`, with one overview page per area,
for example `wiki/customers/README.md`, that links every page of the area.

Before you create a page, search for an existing page about the same thing. Extend it instead of
creating a second one.

**Pages that a human owns:** if a page has an owner and `status: reviewed`, do not change what it
says. Write a note with your proposed change, link the page, and name the owner.

## Bring a meeting into the wiki (ingest)

1. **Claim it.** In a meeting, say with `utterance_say` that you take on the ingest, as soon as
   you take it on. If someone already said so, leave it to them.
2. After the meeting, read the new sources: the minutes, the decisions and the notes of the
   session.
3. For every customer, project, person or topic they touch, find its page. Update it, or create
   it if none exists. One meeting often touches several pages.
4. Cite the sources in each page, link the pages to each other, and update the overview page of
   each area you touched.
5. Do not copy the minutes. Write what the team knows now, in short statements.

## Answer a question (query)

Answer from the pages, and name the pages and sources you used. If a page is a `draft`, is old,
or contradicts a source, say so. If the answer is new and useful, offer to keep it as a page.

## Check the wiki (lint)

Only when a human asks for it: look for contradictions, outdated statements, pages that nothing
links to, links that lead nowhere, and topics that have no page. Fix pages that no human owns.
Report the rest. Never change a source.
