# Deployment View

## Deployment overview

The first stage has one environment. It contains one office hub, the computers of the human
participants and the runtimes of the agents. An agent and its participant client must share a
file system. The CLI and the log replicator run on both client nodes. The diagram shows them only
once (see the `hosts` of each node). OPEN: is the hub self-hosted or in the cloud?

```arc42
:::diagram
id: deployment-overview
view: deployment
notation: mermaid-architecture
:::
```

```mermaid
architecture-beta
    group env-mvp(cloud)[First stage]
    group dn-participant-laptop(server)[Participant computer] in env-mvp
    group dn-agent-runtime(server)[Agent runtime] in env-mvp
    group dn-office-hub(server)[Office hub] in env-mvp
    service bb-ears(disk)[Ears] in dn-participant-laptop
    service bb-cli(server)[office CLI] in dn-participant-laptop
    service bb-replicator(disk)[Log replicator] in dn-participant-laptop
    service bb-mcp(server)[MCP server] in dn-agent-runtime
    service bb-session-service(server)[Meeting session] in dn-office-hub
    service bb-records(database)[Records] in dn-office-hub
    service bb-artifact-service(server)[Artifact tools] in dn-office-hub
    service bb-stage(internet)[Stage] in dn-office-hub
    service bb-identity(server)[Identity] in dn-office-hub
    service bb-archive(disk)[Archive] in dn-office-hub
    bb-ears:R --> L:bb-session-service
    bb-replicator:R --> L:bb-session-service
    bb-mcp:L --> R:bb-session-service
```

## First stage

The environment in which the system first runs in our own team.

```arc42
:::deployment-node
id: env-mvp
title: First stage
type: environment
:::
```

## Participant computer

The laptop of a human with a headset. It runs the CLI, the log replicator and the ears with local
Whisper. OPEN: minimum hardware for real-time Whisper (CPU or GPU, model size).

```arc42
:::deployment-node
id: dn-participant-laptop
title: Participant computer
type: device
parent: env-mvp
hosts: bb-cli, bb-client-core, bb-replicator, bb-ears
:::
```

## Agent runtime

The computer or container in which the agent runs, for example Claude Code on a laptop or in a
cloud session. It runs the MCP server, the CLI and the log replicator in the same file system as
the agent.

```arc42
:::deployment-node
id: dn-agent-runtime
title: Agent runtime
type: container
parent: env-mvp
hosts: bb-mcp, bb-cli, bb-client-core, bb-replicator
:::
```

## Office hub

One server or container that runs the hub: the meeting session, the records, the artifact tools,
the stage and the archive. The archive stores sessions, decisions, minutes and notes in a local Git
repository on this host (`dec-yaml-git-storage`); the push to a remote is mocked for now. The
host needs the Git command line tool. It keeps its own
state permanently: the participants and their keys (identity) and its signing key for the tree
heads (`dec-merkle-log`).

```arc42
:::deployment-node
id: dn-office-hub
title: Office hub
type: server
parent: env-mvp
hosts: bb-session-service, bb-records, bb-artifact-service, bb-stage, bb-identity, bb-archive
:::
```
