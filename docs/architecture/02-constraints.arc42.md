# Randbedingungen

## Datenschutz und Vertraulichkeit des Wortes

Das Mithören in Meetings berührt die DSGVO und § 201 StGB (Vertraulichkeit des nicht
öffentlich gesprochenen Wortes). Wer erfasst wird, muss eingewilligt haben und das
erkennen können. Erfasste Inhalte brauchen Aufbewahrungs- und Löschregeln.

```arc42
:::constraint
id: con-privacy-law
title: DSGVO und § 201 StGB
category: organizational
source: Gesetzgebung (DE/EU)
:::
```

## Agenten arbeiten rundenbasiert

Heutige Agenten arbeiten in Runden (Turns). Standard-MCP-Clients reichen
Server-Notifications in der Regel nicht an das Modell weiter, das Modell sieht nur
Tool-Ergebnisse. Die Live-Anbindung darf daher nicht von Push-Fähigkeiten des Clients
abhängen.

```arc42
:::constraint
id: con-turn-based-agents
title: Agenten sind rundenbasiert, MCP-Push wird kaum unterstützt
category: technical
source: Verhalten gängiger Agent-Clients (Stand 2026)
:::
```

## Keine Intelligenz in der Anwendung

Die Anwendung enthält kein LLM und trifft keine inhaltlichen Urteile. Sie speichert,
rechnet, rendert, transkribiert und prüft gegen Regeln. Bewertung und Formulierung
übernimmt der Agent. STT gilt als Umwandlung, nicht als Urteil.

```arc42
:::constraint
id: con-no-intelligence
title: Keine eigene Intelligenz in der Anwendung
category: convention
source: Produktvision
:::
```

## Headset als Voraussetzung für die Ohren

In der ersten Ausbaustufe gibt es weder Echounterdrückung noch Stimmabdruck. Nur mit Headset ist
sichergestellt, dass die Ohren ausschließlich ihren Besitzer hören. Der Betrieb mit Lautsprechern
ist nicht unterstützt. Die Aufhebung ist in Issue #1 beschrieben.

```arc42
:::constraint
id: con-headset-only
title: Ohren nur mit Headset (keine Echounterdrückung, kein Stimmabdruck)
category: technical
source: Festlegung für die erste Ausbaustufe; Aufhebung in Issue #1
:::
```

## Meeting-Tool ist gesetzt und beliebig

Teams nutzen ihr bestehendes Meeting-Tool (Zoom, Teams, Meet, Jitsi …). Das Office
muss ohne Integration in diese Tools auskommen.

```arc42
:::constraint
id: con-any-meeting-tool
title: Beliebiges, unverändertes Meeting-Tool
category: organizational
source: Produktvision
:::
```
