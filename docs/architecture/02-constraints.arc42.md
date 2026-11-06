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
features of the client.

```arc42
:::constraint
id: con-turn-based-agents
title: Agents work in turns; MCP push is rarely supported
category: technical
source: Behaviour of common agent clients (2026)
:::
```

## No intelligence in the application

The application contains no LLM and makes no judgements about content. It stores, calculates,
renders, transcribes and checks against rules. The agent does the judging and the wording.
Speech-to-text counts as conversion, not as judgement.

```arc42
:::constraint
id: con-no-intelligence
title: No intelligence of its own in the application
category: convention
source: Product vision
:::
```

## Headset required for the ears

The first stage has no echo cancellation and no voice print. Only a headset makes sure that the
ears hear nothing but their owner. Use with loudspeakers is not supported. Issue #1 describes how
to lift this constraint.

```arc42
:::constraint
id: con-headset-only
title: Ears only with a headset (no echo cancellation, no voice print)
category: technical
source: Scope of the first stage; to be lifted by issue #1
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
