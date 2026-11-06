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
| agent inference | Headset als Voraussetzung im ersten Schritt, kein Stimmabdruck, keine Echounterdrückung | ch.5 bb-ears, ch.9 dec-local-single-ear | medium | OPEN: bestätigen |
| Nutzer, Runde 7 | Trennung Live-Interaktion vs. Werkzeuge; Werkzeuge über MCP und CLI | ch.9 dec-cli-and-mcp | high | — |
| Nutzer, Runde 8 | Agent startet Teilnahme; lokaler Socket repliziert Log; Agent erhält Dateipfad und tailt | ch.5 bb-replicator, if-local-log, ch.9 dec-log-as-file | high | — |
| Nutzer, Runde 9 | Agent schreibt über Tools; „listen“ ist der falsche Begriff → join | ch.8 concept-read-file-write-tools | high | — |
| Nutzer, Runde 10 | Menschen und Agenten nutzen gleiche CLI; Menschen werden zusätzlich transkribiert | ch.8 concept-participant-model, ch.9 dec-unified-participant | high | — |
| agent inference | Protokolle HTTPS/JSON und WebSocket | ch.5 if-session-commands, if-session-stream | low | OPEN: Protokolle festlegen |
| agent inference | Keine Partials im Log | ch.9 dec-no-partials-in-log | low | OPEN: bestätigen |
| agent inference | Qualitätsziele, Prioritäten und Metriken | ch.10 | low | OPEN: bestätigen |
| agent inference | Datenschutz-Risiken (Rückzug, Einwilligung) | ch.11 | medium | OPEN: mit Datenschutz klären |
| — | Server-Betrieb (Self-Hosting/Cloud), Persistenz, Programmiersprache | ch.7 | — | OPEN: nicht diskutiert |
