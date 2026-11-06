# Qualitätsanforderungen

Die Ziele leiten sich aus der Konzeptdiskussion ab. Prioritäten und Metriken sind Vorschläge
(OPEN: bestätigen).

## Interoperabilität

Jeder Agent mit Shell oder MCP soll ohne spezielle Client-Features teilnehmen können.

```arc42
:::quality-goal
id: qg-interoperability
title: Interoperabilität mit beliebigen Agenten
priority: high
:::
```

## Vertraulichkeit

Gesprochenes ist besonders schützenswert. Rohaudio darf das Gerät des Sprechers nicht verlassen,
erfasst wird nur, wer eingewilligt hat.

```arc42
:::quality-goal
id: qg-privacy
title: Vertraulichkeit des gesprochenen Wortes
priority: high
:::
```

## Transparenz

Jeder Teilnehmer sieht, was von ihm erfasst wurde und was die Agenten daraus machen.

```arc42
:::quality-goal
id: qg-transparency
title: Transparenz der Erfassung
priority: high
:::
```

## Reaktionsfähigkeit

Ein Agent soll im Gespräch rechtzeitig reagieren können, also innerhalb der natürlichen Pause
nach einem Beitrag.

```arc42
:::quality-goal
id: qg-responsiveness
title: Reaktionsfähigkeit im Gespräch
priority: medium
:::
```

## Neuer Agent dockt an

Ein Agent, der das System nicht kennt, soll nur mit CLI-Hilfe teilnehmen können.

```arc42
:::quality-scenario
id: qs-new-agent
title: Neuer Agent nimmt ohne Spezial-Client teil
quality: qg-interoperability
stimulus: Ein Agent mit Shell erhält nur Session-Code und office --help
response: Er tritt bei, verfolgt das Log und meldet sich auf der Bühne
metric: Gelingt ohne MCP-Push und ohne Code-Änderung am Agenten (OPEN: Zeitbudget)
:::
```

## Kein Rohaudio im Netz

Während eines Meetings mit eingeschalteten Ohren wird der Netzverkehr des Clients geprüft.

```arc42
:::quality-scenario
id: qs-no-raw-audio
title: Kein Rohaudio verlässt das Gerät
quality: qg-privacy
stimulus: Mensch spricht 30 Minuten mit --mic
response: Nur Text-Events werden gesendet
metric: 0 Byte Audiodaten im ausgehenden Verkehr
:::
```

## Sprecher sieht, was gesendet wurde

Nach jedem Satz sieht der Sprecher den Status im Terminal.

```arc42
:::quality-scenario
id: qs-sender-sees-status
title: Status eigener Sätze ist sichtbar
quality: qg-transparency
stimulus: Ein Satz wird gesendet oder zurückgezogen
response: Das Terminal zeigt den Status, die Bühne den Erfassungsstatus
metric: Status sichtbar ≤ 1 s nach Bestätigung durch den Server (OPEN)
:::
```

## Satz erreicht den Agenten rechtzeitig

Ein gesprochener Satz soll den Agenten erreichen, bevor die Pause nach dem Beitrag vorbei ist.

```arc42
:::quality-scenario
id: qs-sentence-latency
title: Satzende bis Zeile in der Agenten-Datei
quality: qg-responsiveness
stimulus: Ein Mensch beendet einen Satz
response: utterance.final steht in der lokalen Log-Datei des Agenten
metric: p95 ≤ 2 s ab Satzende (OPEN: abhängig von Whisper-Hardware)
:::
```
