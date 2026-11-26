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

- **Sources** are memories of what happened: minutes, decisions and notes. Once released, they
  **never change**. To correct one, write a new note that names the old one.
- **Pages** are documents in Markdown. Humans and agents write and change them. They hold what
  the team knows now, for example everything about one customer, project or topic.
- **This skill** holds the rules. Team-specific rules come from your role and from the pages you
  find.

## Keep no knowledge of your own

A tenant shares all its knowledge, except what is off the record. So what the team should know
belongs in the wiki, not in your own memory: not in the memory files of your harness, not in
local notes. Everyone in the tenant reads the wiki; nobody reads your memory.

- When you learn something worth keeping, write it as a note or into a page.
- If you kept something in your own memory during a meeting, go through it when the meeting has
  ended. Write what is worth keeping into the wiki, naming the meeting, and then delete it from
  your memory. Delete everything that is off the record.

## Find what the team knows

1. Read in the local clone of the tenant if you have one (`office sync init`, `office sync
   follow`). Search it with your own tools, for example `grep -ril "acme" .`.
2. Without a clone, use `document_list`, `document_get`, `note_list`, `decision_list` and
   `minutes_list` (or the same commands of the `office` CLI).
3. Start at the overview pages of an area and follow the links. Search for the name and for
   other words that mean the same, also in other languages.
4. A file that says it waits for a release is a stub: its content comes later. Do not cite, change
   or link it as if it were there, and do not lint it. If you took part in that meeting, read the
   real artifact through the interfaces of the office.
5. Look at the front matter of a page before you trust it: a page with `status: reviewed` and a
   recent review is more reliable than a `draft`.

## Record a source

When something comes from a meeting, always name the meeting (`session`). Then it waits until a
human releases the meeting, and only its participants see it until then. Until the release, you may
correct or remove what you wrote (`note_change`, `note_remove`, `decision_change`,
`decision_remove`, `minutes_change`, `minutes_remove`, with the same `session`); do so at once when
a sentence goes off the record. Do not push
files from a meeting with Git: a push skips the release. Tasks are never held, so keep sentences
from the meeting out of them.

- **Note** (`note_add`): one observation or fact in Markdown, complete on its own, so that a
  reader understands it without the meeting. Add a note when something is worth remembering and
  no other type fits. Give it `tags`, and use the tags that similar notes and pages already use.
  The hub stores it as `notes/NNNNNN.md`, with its author, time, session and tags in the front
  matter.
- **Decision** (`decision_record`): only what the humans actually decided, in one clear sentence.
- **Minutes** (`minutes_update`): only if your role asks for it. Every member reads the minutes
  once a human released them, but only the participants read the meeting. When a sentence goes off the record, take it out of
  the minutes at once.
- Never put a sentence that is off the record into any artifact, and never repeat it.

A page or a slide deck that you make for a meeting names the meeting too (`document_create` or
`slides_render` with `session`). It waits for the release like a source, and only the participants
see it until then. Until the release, correct or remove it with the same `session`
(`document_change`, `document_remove`, `slides_change`, `slides_remove`). A change of a page that
is already released is shared at once, whatever session it names.

## Write a page

Create a page with `document_create` and change it with `document_change`. A change needs the
version that you read (`--base`). If the hub refuses it because someone changed the page
meanwhile, read it again, merge your change, and try again. Never overwrite blindly.

A page is a document: its path is `documents/<name>.md`, with folders if you like. Start every
page with front matter that names the schema of documents. In this example, `ada` owns the page
and `bob` reviewed it (participant IDs):

```markdown
---
schema: office.document.v1
title: ACME GmbH
summary: Customer since 2025; contract, contacts and open topics.
tags: [customer]
sources:
  - minutes/000012.yaml
  - notes/000031.md
status: draft
owner: ada
reviewed:
  - id: bob
    date: 2026-11-19
---

# ACME GmbH

The price is 40 EUR per seat ([minutes of 12 November](../../minutes/000012.yaml)).
```

The hub checks the front matter of every page that names `schema: office.document.v1`, through
`document_create`, `document_change` and a push. It refuses a page with a wrong field and says
why. Every field is optional, and no other field is allowed:

- **schema**: exactly `office.document.v1`.
- **title** and **summary**: one line of text each. The portal lists pages by them. Without a
  title, the first heading is the title.
- **tags**: a list. Each tag is one line, not blank, without spaces around it, and not repeated.
  Use the tags that similar pages already use.
- **sources**: a list of the paths of the sources that the page draws on, from the top of the
  repository (`minutes/000012.yaml`, `notes/000031.md`), each ending in `.md`, `.yaml` or `.yml`,
  none repeated. Also link the source next to the statement that comes from it. Cite records,
  never a meeting or its transcript.
- **status**: `draft`, `reviewed` or `outdated`. You write `draft`. Set `outdated` when a newer
  page replaces it, and link the newer page at the top.
- **owner**: the participant ID of the owner. Set by humans.
- **reviewed**: a list of reviews, each with exactly two fields: `id`, the participant ID of the
  reviewer, and `date`, the day of the review as `YYYY-MM-DD`. Add a review only when a human
  asks you to record their review. Never add yourself.
- **Links**: plain relative Markdown links to other files of the tenant, relative to the page
  (from `documents/customers/acme.md`, the minutes are `../../minutes/000012.yaml`). The hub
  answers every write with `hints`: one for each relative link that leads to no file. A push
  shows the same hints as `remote:` lines. The hub writes the page anyway, so fix every hint
  that it gives, at once.

Where pages live: follow the structure you find. If the tenant has none yet, use
`documents/<area>/<name>.md`, for example `documents/customers/acme.md`, with one overview page
per area, for example `documents/customers/README.md`, that links every page of the area.

Before you create a page, search for an existing page about the same thing. Extend it instead of
creating a second one.

**Pages that a human owns:** if a page has an owner and `status: reviewed`, do not change what it
says. Write a note with your proposed change, link the page, and name the owner.

## Bring a meeting into the wiki (ingest)

1. **Claim it.** In a meeting, say with `utterance_say` that you take on the ingest, as soon as
   you take it on. If someone already said so, leave it to them.
2. After the meeting, wait until a human released its records (or the hub released them after the
   grace period). Then read the new sources: the minutes, the decisions and the notes of the
   session. What is not released yet is only in the staging area of the session, not in your clone; never
   ingest it.
3. For every customer, project, person or topic they touch, find its page. Update it, or create
   it if none exists. One meeting often touches several pages.
4. Cite the sources in each page, link the pages to each other, and update the overview page of
   each area you touched.
5. Do not copy the minutes. Write what the team knows now, in short statements.
6. Until the release, do not put what was said in the meeting into existing pages or into notes
   that do not name the meeting: they would be shared at once.
7. Use only what the minutes, decisions and notes say, not what you heard in the meeting. Only
   the participants of a meeting may read its transcript, but every member reads the wiki. If
   something important is missing from the records, write a note first.

## Answer a question (query)

Answer from the pages, and name the pages and sources you used. If a page is a `draft`, is old,
or contradicts a source, say so. If the answer is new and useful, offer to keep it as a page.

## Check the wiki (lint)

Only when a human asks for it: look for contradictions, outdated statements, pages that nothing
links to, links that lead nowhere, and topics that have no page. Fix pages that no human owns.
Report the rest. Never change a source.
