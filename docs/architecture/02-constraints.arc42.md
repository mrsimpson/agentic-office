# Architecture Constraints

## Data protection and confidentiality of speech

Listening in meetings touches the GDPR and § 201 of the German Criminal Code (confidentiality of
the spoken word). People who are captured must have consented, and they must be able to see that
they are captured. Captured content needs retention and deletion rules.

```arc42
:::constraint
id: con-privacy-law
title: GDPR and § 201 German Criminal Code
category: organizational
source: Legislation (DE/EU)
:::
```

## Agents work in turns

Today's agents work in turns. Standard MCP clients usually do not pass server notifications on to
the model. The model only sees tool results. So the live connection must not depend on push
features of the client. An agent finishes a turn and then waits for a new prompt; something
outside the agent must start its next turn (`dec-agent-runner-acp`).

```arc42
:::constraint
id: con-turn-based-agents
title: Agents work in turns; MCP push is rarely supported
category: technical
source: Behaviour of common agent clients (2026)
:::
```

## No intelligence in the hub

The office hub contains no LLM and makes no judgements about content. It stores, calculates,
renders and checks against rules. The hub holds the state of the team and needs governance:
every result of the hub must be deterministic and traceable. The agent does the judging and the
wording.

The rule applies to the hub only. The participant client runs on the computer of a participant
and may contain models, generative or not: the ears transcribe with a speech model, a model for
named-entity recognition finds names in project documents (`dec-ner-model`), and the agent
runner may ask a forwarding judge or a decision model that the user brings
(`dec-models-in-client`). Speech-to-text
counts as conversion, not as judgement.

```arc42
:::constraint
id: con-no-intelligence
title: No intelligence of its own in the hub
category: convention
source: Product vision; scope limited to the hub by the maintainer on 2026-11-11 (epics #65, #66)
:::
```

## The ears hear only their owner

The ears transcribe one person: the owner of the device. Two things make sure of it. With a
headset, nobody else is near the microphone. Without a headset, the owner needs a voice print,
and the ears drop everything that is not the owner's voice (`dec-voice-print`). There is no echo
cancellation. Without a voice print and without a headset, the ears would transcribe the other
participants too, so the ears do not offer that setup.

```arc42
:::constraint
id: con-own-voice-only
title: The ears transcribe only their owner (headset or voice print, no echo cancellation)
category: technical
source: Scope of the first stage; voice print added by the maintainer on 2026-11-27 (issue #1)
:::
```

## Any meeting tool, unchanged

Teams keep their meeting tool (Zoom, Teams, Meet, Jitsi …). The office must work without any
integration into these tools.

```arc42
:::constraint
id: con-any-meeting-tool
title: Any meeting tool, unchanged
category: organizational
source: Product vision
:::
```

## No unit tests

We do not write unit tests for internal functions or classes. They tie the tests to the inner
structure and make refactoring expensive. Tests check behaviour at the boundaries of building
blocks instead (see the next constraint).

```arc42
:::constraint
id: con-no-unit-tests
title: No unit tests
category: convention
source: Maintainer
:::
```

## Black-box contract tests per building block

Every building block is tested as a black box through its interfaces (chapter 5). A contract test
starts the block, uses only its provided interfaces and checks the results. It knows nothing about
the inside of the block. If a block needs another block, the test uses the real block or a fake
that follows the same contract. End-to-end tests combine several blocks in the same way.

```arc42
:::constraint
id: con-contract-tests
title: Black-box contract tests per building block
category: convention
source: Maintainer
:::
```
