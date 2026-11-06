# Qualitätsanforderungen

Drei Qualitätsziele bestimmen die Architektur, in dieser Reihenfolge: Vertraulichkeit vor
Performance vor Usability. Stehen sie im Konflikt, gewinnt das jeweils höher stehende Ziel.
Eigenschaften wie „jeder Agent kann andocken“ sind Produktmerkmale und keine Qualitätsziele. Sie
stehen in Lösungsstrategie und Entscheidungen.

## Vertraulichkeit

Gesprochenes ist besonders schützenswert (ISO 25010: Security / Confidentiality). Rohaudio darf
das Gerät des Sprechers nicht verlassen, und erfasst wird nur, wer eingewilligt hat.

```arc42
:::quality-goal
id: qg-confidentiality
title: Vertraulichkeit
priority: high
:::
```

## Performance

Ein Agent soll im Gespräch innerhalb der natürlichen Pause nach einem Beitrag reagieren können
(ISO 25010: Performance Efficiency / Time Behaviour). Dazu muss Gesagtes ohne spürbare
Verzögerung im Session-Log stehen.

```arc42
:::quality-goal
id: qg-performance
title: Performance (Latenz)
priority: high
:::
```

## Usability

Teilnehmer sehen jederzeit, was von ihnen erfasst und gesendet wurde, und können eingreifen
(ISO 25010: Usability, hier vor allem Transparenz und Steuerbarkeit).

```arc42
:::quality-goal
id: qg-usability
title: Usability (Transparenz der Erfassung)
priority: high
:::
```

## Kein Rohaudio verlässt das Gerät

Das ist die Abnahmeprüfung für die Vertraulichkeit. In einem Testmeeting spricht ein Teilnehmer
mit eingeschalteten Ohren, und der ausgehende Netzverkehr seines Clients wird mitgeschnitten,
etwa über einen Proxy oder einen Paketmitschnitt. Wir prüfen, dass darin nur Text-Events
vorkommen und keine Audiodaten.

```arc42
:::quality-scenario
id: qs-no-raw-audio
title: Kein Rohaudio im ausgehenden Verkehr
quality: qg-confidentiality
stimulus: Ein Teilnehmer spricht 30 Minuten mit --mic, ausgehender Verkehr wird mitgeschnitten
response: Der Client sendet ausschließlich Text-Events und Metadaten
metric: 0 Byte Audiodaten im Mitschnitt
:::
```

## Transkription bis Session-Log

Gemessen wird ab dem Ende der Transkription eines Satzes (Whisper liefert die finale Utterance)
bis zu dem Zeitpunkt, an dem das Event im Session-Log steht. Die Laufzeit von VAD und Whisper ist
darin nicht enthalten. Die hängt von der Hardware ab und wird getrennt betrachtet.

```arc42
:::quality-scenario
id: qs-transcript-to-log
title: Ende der Transkription bis Event im Log
quality: qg-performance
stimulus: Whisper liefert eine finale Utterance
response: utterance.final steht im Session-Log
metric: < 200 ms (OPEN: p95 oder Maximum; Log des Servers oder lokale Replik)
:::
```

## Sprecher sieht, was gesendet wurde

Nach jedem Satz sieht der Sprecher den Status im Terminal.

```arc42
:::quality-scenario
id: qs-sender-sees-status
title: Status eigener Sätze ist sichtbar
quality: qg-usability
stimulus: Ein Satz wird gesendet oder zurückgezogen
response: Das Terminal zeigt den Status, die Bühne den Erfassungsstatus
metric: Status sichtbar ≤ 1 s nach Bestätigung durch den Server (OPEN)
:::
```
