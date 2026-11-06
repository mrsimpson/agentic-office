# Glossar

## Teilnehmer

Zentrale Rolle im System; Menschen und Agenten werden gleich behandelt.

```arc42
:::glossary-term
id: term-participant
title: Teilnehmer
definition: Mensch oder Agent in einer Session, mit Identität, Art (human/agent) und Rollen
:::
```

## Session

Rahmen eines Meetings im Office.

```arc42
:::glossary-term
id: term-session
title: Session
definition: Ein Meeting im Office, mit eigenem Event-Log, Teilnehmern und Bühne
:::
```

## Session-Log

Die gemeinsame Quelle der Wahrheit einer Session.

```arc42
:::glossary-term
id: term-session-log
title: Session-Log
definition: Event-Log einer Session, in das nur angehängt wird; lokal als JSONL-Datei repliziert
:::
```

## Utterance

Grundeinheit des Gesagten bzw. Geschriebenen im Log.

```arc42
:::glossary-term
id: term-utterance
title: Utterance (Äußerung)
definition: Ein abgeschlossener Beitrag eines Teilnehmers, gesprochen (voice) oder geschrieben (text)
:::
```

## Wortmeldung

Wie sich Teilnehmer, insbesondere Agenten, ins Gespräch bringen.

```arc42
:::glossary-term
id: term-hand-raise
title: Wortmeldung
definition: Signal eines Teilnehmers auf der Bühne, dass er etwas beitragen möchte; die Moderation erteilt das Wort
:::
```

## Bühne

Die sichtbare Präsenz des Office im Meeting.

```arc42
:::glossary-term
id: term-stage
title: Bühne
definition: Web-Ansicht einer Session (Minutes, Wortmeldungen, Artefakte, Erfassungsstatus), per Screenshare gezeigt
:::
```

## Ohren

Der Sinnesanschluss für Audio.

```arc42
:::glossary-term
id: term-ears
title: Ohren
definition: Lokale Audio-Pipeline eines Menschen (VAD, Whisper), die nur Text sendet
:::
```

## Einzelohr

Hör-Modus der ersten Ausbaustufe.

```arc42
:::glossary-term
id: term-single-ear
title: Einzelohr
definition: Hör-Modus, in dem nur der Besitzer des Geräts transkribiert wird
:::
```

## Raumohr

Erweiterung für Besprechungsräume.

```arc42
:::glossary-term
id: term-room-ear
title: Raumohr
definition: Späterer Hör-Modus für mehrere Sprecher an einem Mikrofon, mit Sprechererkennung
:::
```

## Artefakt

Ergebnis der Werkzeug-Ebene.

```arc42
:::glossary-term
id: term-artifact
title: Artefakt
definition: Ein von den Werkzeugen erzeugtes Ergebnis mit ID (z. B. Foliensatz, Plan-Prüfung), das auf der Bühne gezeigt werden kann
:::
```

## Journal

Das System of Record des Teams.

```arc42
:::glossary-term
id: term-journal
title: Journal
definition: Gedächtnis des Teams (Entscheidungen, Aufgaben, Minutes) mit Herkunftsangabe
:::
```
