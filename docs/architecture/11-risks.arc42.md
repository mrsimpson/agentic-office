# Risiken und technische Schulden

## Zurückgezogenes bleibt beim Agenten

Ein zurückgezogener Satz steht bereits in den lokalen Dateien und im Kontext der Agenten. Aus dem
Kontext lässt er sich nicht „verlernen“.

```arc42
:::risk
id: risk-retraction-leak
title: Zurückgezogene Sätze bleiben in Dateien und Agenten-Kontext
severity: high
mitigation: Optionale Haltefrist vor Veröffentlichung; Bereinigung lokaler Dateien bei Session-Ende; Agenten-Vertrag, retracted zu befolgen
:::
```

## Rechtliche Unsicherheit beim Mithören

Einwilligung, Aufbewahrung und der Umgang mit Personen ohne Client sind noch nicht geklärt.

```arc42
:::risk
id: risk-consent
title: Einwilligung und Aufbewahrung ungeklärt
severity: high
mitigation: Nur Teilnehmer mit Client erfassen; Erfassungsstatus auf der Bühne; Prüfung durch Datenschutz vor dem ersten Einsatz
:::
```

## Whisper zu langsam auf Laptops

Lokales Whisper ohne GPU kann zu langsam für Echtzeit sein.

```arc42
:::risk
id: risk-whisper-latency
title: Lokale Transkription zu langsam
severity: medium
mitigation: Kleinere Modelle, whisper.cpp mit Quantisierung; Messung vor Festlegung der Hardware
:::
```

## Agenten ohne Dateisystem

Reine API-Agenten können keine lokale Datei verfolgen.

```arc42
:::risk
id: risk-no-filesystem
title: Agenten ohne gemeinsames Dateisystem
severity: medium
mitigation: Long-Poll-Tool meeting.wait als Fallback
:::
```

## Lautsprecher statt Headset

Nutzt jemand Lautsprecher, landen die Stimmen anderer im eigenen Transkript, und Sätze erscheinen doppelt.

```arc42
:::risk
id: risk-speaker-mode
title: Echo und Dopplungen im Lautsprecherbetrieb
severity: medium
mitigation: Headset als Voraussetzung im ersten Schritt; später Echounterdrückung, Stimmabdruck, Deduplizierung
:::
```
