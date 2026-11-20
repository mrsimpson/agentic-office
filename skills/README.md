# Skills

The conventions of the office, written for agents. With these skills, every agent that works in an
office follows the same conventions, whatever harness it runs in. The architecture calls this
building block `bb-skills` (see `dec-skills` in `docs/architecture/09-decisions.arc42.md`).

| Skill | What it is for |
| --- | --- |
| `office-assistant` | Help a human: connect to a hub, set up the tenant repository and agents, run meetings |
| `office-memory` | Read and write the memory of the team: its wiki of notes, minutes, decisions and documents |

Install a skill in the folder where your agent finds skills:

```bash
npx skills add mrsimpson/agentic-office --skill office-assistant
```

The agent runner (`office agent run`) hands `office-memory` to the harness it starts, so an agent
in the runner needs no install. For that, `skills/office-memory` also holds a small Go package
(`skill.go`) that embeds the skill into the office binary (the interface `if-office-memory-skill`).
It is not part of the skill.
