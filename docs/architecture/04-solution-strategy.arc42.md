# Lösungsstrategie

Die Anwendung ist Zustand, Werkzeug und Sinnesorgan, aber kein Gehirn. Agenten und Menschen
nehmen als gleichartige Teilnehmer teil. Sie lesen den Verlauf aus einer lokal replizierten
Datei und handeln über CLI- oder MCP-Tools. Gesprochenes wird am Rand transkribiert, und die
Präsenz im Meeting läuft über eine geteilte Bühne statt über eine Integration ins Meeting-Tool.

| Qualitätsziel | Lösungsansatz |
|---|---|
| Interoperabilität | Bring your own agent; Log als Datei; ein Kern, Adapter für CLI und MCP |
| Vertraulichkeit | Transkription lokal am Rand, nur Text verlässt das Gerät |
| Transparenz | Einheitliches Teilnehmer-Modell; jeder sieht seine eigenen Events; Bühne zeigt, wer erfasst wird |
| Reaktionsfähigkeit | Gestufte Events (Satzende, Sprecherwechsel); der Agent filtert selbst, worauf er reagiert |

```arc42
:::solution-strategy
id: strategy-architecture
title: Headless Office mit dateibasierter Teilnahme und Transkription am Rand
addresses: qg-interoperability, qg-privacy, qg-transparency, qg-responsiveness
:::
```

## Bring your own agent

Jede inhaltliche Bewertung liegt beim Agenten. Die Anwendung speichert, rechnet, rendert,
transkribiert und prüft gegen Regeln. So kann jeder Agent andocken, und die Anwendung bleibt
deterministisch und testbar (siehe `dec-headless-byoa`).

## Lesen über die Datei, Schreiben über Tools

`join` liefert einen Pfad zu einer lokal replizierten JSONL-Datei. Der Agent liest sie mit
Standardwerkzeugen und bestimmt seinen Takt über Filter. Handlungen laufen über Tools mit
sofortiger Rückmeldung und erscheinen anschließend selbst im Log (siehe `dec-log-as-file`,
`concept-read-file-write-tools`).

## Ein Kern, zwei Adapter

Dieselbe Funktionalität steht über CLI und MCP bereit. Die CLI ist gleichrangig, denn Agenten mit
Shell und Menschen nutzen sie gleichermaßen (siehe `dec-cli-and-mcp`).

## Transkription am Rand

Jeder Mensch transkribiert lokal nur sich selbst (Headset). Wer spricht, ergibt sich aus dem
Aufbau, ohne Sprechererkennung. Rohaudio verlässt das Gerät nicht (siehe `dec-local-single-ear`).

## Bühne per Screenshare

Statt Meeting-Tools zu integrieren, rendert der Server eine Bühne als Webseite, die jemand im
Meeting teilt. Darüber zeigt sich der Agent: Wortmeldungen, Minutes, Artefakte
(siehe `dec-stage-screenshare`).
