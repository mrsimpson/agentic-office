# Verteilungssicht

## Verteilungsüberblick

Im ersten Schritt gibt es eine Umgebung: einen Office-Server, die Rechner der menschlichen
Teilnehmer und die Laufzeitumgebungen der Agenten. Agent und Teilnehmer-Client müssen sich ein
Dateisystem teilen. CLI und Log-Replikator laufen auf beiden Client-Knoten, das Diagramm zeigt
sie nur einmal (siehe `hosts` der Knoten). OPEN: Läuft der Server selbst gehostet oder in der Cloud?

```arc42
:::diagram
id: deployment-overview
view: deployment
notation: mermaid-architecture
:::
```

```mermaid
architecture-beta
    group env-mvp(cloud)[Erste Ausbaustufe]
    group dn-participant-laptop(server)[Teilnehmer-Rechner] in env-mvp
    group dn-agent-runtime(server)[Agent-Laufzeit] in env-mvp
    group dn-office-host(server)[Office-Server] in env-mvp
    service bb-ears(disk)[Ohren] in dn-participant-laptop
    service bb-cli(server)[office CLI] in dn-participant-laptop
    service bb-replicator(disk)[Log-Replikator] in dn-participant-laptop
    service bb-mcp(server)[MCP-Server] in dn-agent-runtime
    service bb-session-service(server)[Meeting-Session] in dn-office-host
    service bb-journal(database)[Journal] in dn-office-host
    service bb-artifact-service(server)[Artefakt-Werkzeuge] in dn-office-host
    service bb-stage(internet)[Bühne] in dn-office-host
    bb-ears:R --> L:bb-session-service
    bb-replicator:R --> L:bb-session-service
    bb-mcp:L --> R:bb-session-service
```

## Erste Ausbaustufe

Die Umgebung, in der das System zuerst im eigenen Team läuft.

```arc42
:::deployment-node
id: env-mvp
title: Erste Ausbaustufe
type: environment
:::
```

## Teilnehmer-Rechner

Laptop eines Menschen mit Headset. Hier laufen CLI, Log-Replikator und Ohren mit lokalem
Whisper. OPEN: Mindest-Hardware für Whisper in Echtzeit (CPU oder GPU, Modellgröße).

```arc42
:::deployment-node
id: dn-participant-laptop
title: Teilnehmer-Rechner
type: device
parent: env-mvp
hosts: bb-cli, bb-replicator, bb-ears
:::
```

## Agent-Laufzeit

Rechner oder Container, in dem der Agent läuft (z. B. Claude Code lokal oder in einer
Cloud-Session). Hier laufen MCP-Server, CLI und Log-Replikator im selben Dateisystem wie der Agent.

```arc42
:::deployment-node
id: dn-agent-runtime
title: Agent-Laufzeit
type: container
parent: env-mvp
hosts: bb-mcp, bb-cli, bb-replicator
:::
```

## Office-Server

Ein Server bzw. Container mit Meeting-Session, Journal, Artefakt-Werkzeugen und Bühne.
OPEN: Persistenz (Datenbank, Git-basierte Ablage oder Hybrid).

```arc42
:::deployment-node
id: dn-office-host
title: Office-Server
type: server
parent: env-mvp
hosts: bb-session-service, bb-journal, bb-artifact-service, bb-stage
:::
```
