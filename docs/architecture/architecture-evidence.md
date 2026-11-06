# Architektur-Evidenz

Quelle aller Fakten ist die Konzeptdiskussion vom 2026-11-06 (noch kein Code). „Nutzer“ heißt:
ausdrücklich vom Initiator festgelegt. `agent inference` heißt: Vorschlag des Agenten, noch zu
bestätigen.

| Quelle | Abgeleiteter Fakt | Verwendet in | Konfidenz | Offene Frage / Entscheidung |
| --- | --- | --- | --- | --- |
| Nutzer, Runde 1 | Headless: Anwendung exponiert nur APIs (MCP, CLI) und Medien-Anschlüsse; Agent bringt die Intelligenz | ch.2 con-no-intelligence, ch.9 dec-headless-byoa | high | — |
| Nutzer, Runde 2 | Whisper selbst anbinden; satzweise Events, Pause löst gründliche Verarbeitung aus | ch.8 concept-event-log, ch.9 dec-own-whisper | high | — |
| agent inference | Schwellwerte 300–700 ms / 1,5–2 s / > 5 s | ch.8 concept-event-log | low | OPEN: Werte messen und festlegen |
| Nutzer, Runde 3 | Mehrsprecher-Problem; bewusste Wahl, wem zugehört wird | ch.9 dec-local-single-ear, ch.11 risk-speaker-mode | high | — |
| Nutzer, Runde 4–5 | Lokales Gateway transkribiert und sendet; Agent verbunden mit Server; Bühne per Screenshare | ch.3, ch.5 bb-ears, bb-stage, ch.9 dec-stage-screenshare | high | — |
| Nutzer, Runde 6 | Zunächst nur Text, TTS später; Gateway als CLI, zeigt Aufgezeichnetes und Gesendetes | ch.9 dec-text-first, dec-gateway-cli | high | — |
| Nutzer, Review 1 | Headset als Voraussetzung, kein Stimmabdruck, keine Echounterdrückung; Aufhebung als Issue #1 | ch.2 con-headset-only, ch.11 risk-speaker-mode | high | — |
| Nutzer, Runde 7 | Trennung Live-Interaktion vs. Werkzeuge; Werkzeuge über MCP und CLI | ch.9 dec-cli-and-mcp | high | — |
| Nutzer, Runde 8 | Agent startet Teilnahme; lokaler Socket repliziert Log; Agent erhält Dateipfad und tailt | ch.5 bb-replicator, if-local-log, ch.9 dec-log-as-file | high | — |
| Nutzer, Runde 9 | Agent schreibt über Tools; „listen“ ist der falsche Begriff → join | ch.8 concept-read-file-write-tools | high | — |
| Nutzer, Runde 10 | Menschen und Agenten nutzen gleiche CLI; Menschen werden zusätzlich transkribiert | ch.8 concept-participant-model, ch.9 dec-unified-participant | high | — |
| agent inference | Protokolle HTTPS/JSON und WebSocket | ch.5 if-session-commands, if-session-stream | low | OPEN: getypte Alternative mit Streaming prüfen (z. B. Connect RPC) |
| Nutzer, Review 1 | Keine Partials im Log | ch.9 dec-no-partials-in-log | high | — |
| Nutzer, Review 1 | Qualitätsziele in Reihenfolge Vertraulichkeit, Performance, Usability; Interoperabilität ist ein Merkmal, kein Qualitätsziel | ch.1, ch.10 | high | — |
| Nutzer, Review 1 | Latenz: Ende der Transkription bis Event im Log < 200 ms | ch.10 qs-transcript-to-log | high | OPEN: p95 oder Maximum; Server-Log oder lokale Replik |
| agent inference | Alle drei Ziele priority high, Reihenfolge steht in der Prosa (arc42 erwartet 3–5 high) | ch.10 | medium | — |
| agent inference | Datenschutz-Risiken (Rückzug, Einwilligung) | ch.11 | medium | OPEN: mit Datenschutz klären |
| — | Server-Betrieb (Self-Hosting/Cloud), Persistenz, Programmiersprache | ch.7 | — | OPEN: nicht diskutiert |
