# Architekturentscheidungen

Alle Entscheidungen stammen aus der Konzeptdiskussion vom 2026-11-06. Als `accepted` sind
diejenigen markiert, die dort ausdrücklich getroffen wurden. `proposed` sind Vorschläge, die
noch bestätigt werden müssen.

## Headless, keine Intelligenz in der Anwendung

**Kontext:** Agenten werden schnell besser, und jedes Team nutzt andere. **Entscheidung:** Die
Anwendung exponiert nur APIs (MCP, CLI) und Medien-Anschlüsse. Urteile trifft der Agent.
**Folgen:** Die Anwendung bleibt deterministisch und testbar. Funktionen, die Urteilsvermögen
erfordern, gehören nicht in die Anwendung.

```arc42
:::decision
id: dec-headless-byoa
title: Headless Office, Bring your own agent
status: accepted
date: 2026-11-06
addresses: con-no-intelligence, qg-interoperability
:::
```

## Eigene STT-Pipeline mit Whisper

**Kontext:** Ein fertig transkribierter Strom steht nicht zur Verfügung. **Entscheidung:** Wir
binden Whisper selbst an. **Folgen:** Wir bestimmen Granularität und Zeitpunkt der Events
(Satzende, Sprecherwechsel). Daraus ergibt sich die gestufte Verarbeitung beim Agenten.
**Verworfen:** Transkription des Meeting-Tools, weil sie nicht verfügbar und nicht steuerbar ist.

```arc42
:::decision
id: dec-own-whisper
title: Eigene STT-Pipeline mit Whisper und gestuften Events
status: accepted
date: 2026-11-06
addresses: qg-responsiveness
:::
```

## Lokales Gateway, Einzelohr mit Headset

**Kontext:** Whisper unterscheidet keine Sprecher. Lautsprecher und Räume verursachen Echo und
Mischsignale. **Entscheidung:** Jeder Mensch transkribiert lokal nur sich selbst, im ersten
Schritt mit Headset. **Folgen:** Wer spricht, ergibt sich aus dem Aufbau. Rohaudio bleibt lokal.
Lautsprecher und Räume sind vorerst nicht abgedeckt.
**Verworfen:** zentrale Sprechererkennung (pyannote) auf einem gemischten Signal.

```arc42
:::decision
id: dec-local-single-ear
title: Lokale Transkription pro Teilnehmer (Einzelohr, Headset)
status: accepted
date: 2026-11-06
addresses: con-privacy-law, qg-privacy
:::
```

## Bühne per Screenshare statt Integration ins Meeting-Tool

**Kontext:** Bots für Zoom, Teams oder Meet sind aufwendig und plattformabhängig.
**Entscheidung:** Der Server rendert eine Bühne als Webseite, die per Screenshare gezeigt wird.
**Folgen:** Das funktioniert mit jedem Tool. Die Präsenz des Agenten ist visuell, und wer keinen
Client hat, sieht trotzdem, was passiert.

```arc42
:::decision
id: dec-stage-screenshare
title: Bühne per Screenshare
status: accepted
date: 2026-11-06
addresses: con-any-meeting-tool, qg-transparency
:::
```

## Zunächst nur Text, Sprachausgabe später

**Entscheidung:** Agenten melden sich zunächst nur per Text auf der Bühne. **Folgen:** Der Mensch
behält die Gesprächsführung. Sprachausgabe lässt sich später über das Audio der
Bildschirmfreigabe ergänzen, ohne die Architektur zu ändern (Echounterdrückung beim Teilenden nötig).

```arc42
:::decision
id: dec-text-first
title: Zunächst nur Text, Sprachausgabe später
status: accepted
date: 2026-11-06
:::
```

## Gateway als CLI

**Entscheidung:** Das Gateway ist zunächst eine CLI, die zeigt, was aufgezeichnet und gesendet
wurde. Sie bietet Pausieren und Zurückziehen. **Folgen:** Es gibt keine eigene Oberfläche. Die
Anzeige ist eine gefilterte Sicht auf das lokale Log.

```arc42
:::decision
id: dec-gateway-cli
title: Gateway als CLI
status: accepted
date: 2026-11-06
addresses: qg-transparency
:::
```

## Session-Log als lokale Datei

**Kontext:** Agenten arbeiten rundenbasiert, und MCP-Push erreicht das Modell kaum.
**Entscheidung:** `join` startet einen lokalen Replikator, der das Log über einen Socket
repliziert, und gibt synchron einen Dateipfad zurück, den der Agent verfolgt.
**Folgen:** Den Takt steuert der Agent über Filter, die Historie ist durchsuchbar, und es gibt
keine Abhängigkeit von Client-Features. Das setzt ein gemeinsames Dateisystem voraus.
**Verworfen:** MCP-Notifications als primärer Weg; Long-Poll als primärer Weg.

```arc42
:::decision
id: dec-log-as-file
title: Session-Log als lokal replizierte Datei
status: accepted
date: 2026-11-06
addresses: con-turn-based-agents, qg-interoperability, qg-responsiveness
:::
```

## Einheitliches Teilnehmer-Modell

**Entscheidung:** Menschen und Agenten sind Teilnehmer mit denselben Werkzeugen. Bei Menschen
mit Mikrofon wird zusätzlich transkribiert. **Folgen:** Das Gateway ist `join --mic`. Das System
lässt sich ganz ohne Agenten testen.

```arc42
:::decision
id: dec-unified-participant
title: Menschen und Agenten als gleichartige Teilnehmer
status: accepted
date: 2026-11-06
addresses: qg-transparency, qg-interoperability
:::
```

## CLI und MCP gleichwertig

**Entscheidung:** Alle Funktionen stehen über CLI und MCP aus einem gemeinsamen Kern zur
Verfügung, Werkzeuge wie Live-Ebene. **Folgen:** Das sind zwei dünne Adapter. Die CLI ist kein
Nebenprodukt.

```arc42
:::decision
id: dec-cli-and-mcp
title: CLI und MCP aus einem gemeinsamen Kern
status: accepted
date: 2026-11-06
addresses: qg-interoperability
:::
```

## Partials nicht ins Log

**Vorschlag:** Vorläufige Transkripte (`partial`) werden nicht repliziert, nur finale
Utterances und Signale. Das reduziert Rauschen und das Volumen der Datei.

```arc42
:::decision
id: dec-no-partials-in-log
title: Keine Partials im Session-Log
status: proposed
date: 2026-11-06
addresses: qg-responsiveness
:::
```
