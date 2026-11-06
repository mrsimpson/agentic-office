# Querschnittliche Konzepte

## Teilnehmer-Modell

Es gibt nur **Teilnehmer**. Jeder hat eine Identität, eine Art (`kind: human | agent`) und
Rollen (z. B. Moderation, Protokoll, Bühnensteuerung). Rechte hängen an Rollen, nicht an der Art.
Die Art bleibt sichtbar, weil die Bühne Agenten als Agenten kennzeichnet und weil nur Sprache
eine Einwilligung zur Aufzeichnung braucht.

Eine Äußerung ist eine Äußerung: `utterance.final` trägt `modality: voice | text`. Gesprochenes
eines Menschen ist `voice`, getippte Einwürfe und Wortmeldungen von Agenten sind `text`. Eine
spätere Sprachausgabe vertont Text-Äußerungen, das Modell bleibt dabei gleich.

```arc42
:::concept
id: concept-participant-model
title: Einheitliches Teilnehmer-Modell
category: domain
:::
```

## Event-Log mit Cursor

Jede Session hat ein Log, in das nur angehängt wird. Jedes Event hat eine monoton steigende ID.
Der Cursor ist die letzte gelesene ID bzw. die Zeilennummer in der lokalen Datei. Korrekturen
und Rückzüge sind neue Events mit Referenz (`utterance.retracted`, `utterance.corrected`), nie
Änderungen bestehender Einträge.

Event-Stufen (Richtwerte, pro Teilnehmer konfigurierbar):

| Event | Auslöser |
|---|---|
| `utterance.final` | Satzende, ca. 300–700 ms Stille |
| `turn.end` | Sprecherwechsel, ca. 1,5–2 s Stille |
| `silence.long` | > 5 s Stille |
| `addressed` | Name eines Teilnehmers fällt |
| `hand.raised`, `minutes.updated`, `artifact.shown` | Aktionen von Teilnehmern |
| `participant.joined/left/paused`, `session.ended` | Lebenszyklus |

Partials kommen nicht ins Log (OPEN: optional als eigene Datei).

```arc42
:::concept
id: concept-event-log
title: Session-Log mit Cursor, nur anhängen
category: integration
:::
```

## Lesen über die Datei, Schreiben über Tools

Teilnehmer lesen den Verlauf aus der lokal replizierten Datei, bei Agenten typischerweise als
Hintergrundprozess mit Filter (`tail -f … | grep '"turn.end"\|"addressed"'`). Sie handeln über
Tools (CLI/MCP), die sofort antworten und Eingaben prüfen. Jede Handlung erscheint danach im Log.
Damit bestätigen Agenten ihre Aktionen selbst, und mehrere Agenten stimmen sich ab, ohne dass
es dafür ein eigenes Protokoll braucht.

Für Agenten ohne Dateisystem gibt es als Fallback ein Long-Poll-Tool (`meeting.wait`)
(OPEN: im ersten Schritt nötig?).

```arc42
:::concept
id: concept-read-file-write-tools
title: Lesen über die Datei, Schreiben über Tools
category: integration
:::
```

## Transkription am Rand

Jeder Mensch transkribiert lokal nur sich selbst. Rohaudio verlässt das Gerät nie, gesendet wird
nur Text mit Metadaten. Mit Headset ergibt sich das Einzelohr von selbst. Lautsprecherbetrieb
(Echounterdrückung, Stimmabdruck) und Raumohr (Sprechererkennung) folgen später.
Stimmabdrücke sind biometrische Daten (Art. 9 DSGVO) und brauchen eine ausdrückliche Einwilligung.

```arc42
:::concept
id: concept-edge-transcription
title: Lokale Transkription, nur Text verlässt das Gerät
category: privacy
:::
```

## Transparenz und Einwilligung

Wer erfasst wird, sieht in seinem Terminal jeden Satz mit Status und kann pausieren und
zurückziehen. Die Bühne zeigt allen, wer erfasst, pausiert oder nicht erfasst ist. Wer keinen
Client hat, wird nicht transkribiert. Lokale Log-Dateien haben Rechte `0600` und werden nach dem
Ende der Session gemäß Aufbewahrungsregel bereinigt (OPEN: Regel festlegen, auch für
zurückgezogene Sätze).

```arc42
:::concept
id: concept-transparency
title: Transparenz und Einwilligung
category: privacy
:::
```
