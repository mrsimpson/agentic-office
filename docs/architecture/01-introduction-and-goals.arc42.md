# Einführung und Ziele

## 1.1 Aufgabenstellung

Agentic Office ist ein **headless Office**: eine Anwendung, die ein Unternehmen bei
Verwaltungsfragen unterstützt, ohne selbst „intelligent“ zu sein. Sie hält den Zustand
des Teams fest (Journal, Entscheidungen, Minutes, Artefakte), stellt deterministische
Werkzeuge bereit (Folien rendern, Business-Pläne rechnerisch prüfen) und öffnet Sinne
in die reale Welt (zunächst Audio aus Meetings). Das Denken bringt der Agent mit
(„Bring your own agent“).

Agenten und Menschen sind dabei gleichberechtigte **Teilnehmer**. Beide nutzen dieselbe
CLI bzw. denselben MCP-Server. Der einzige Unterschied: Menschen können zusätzlich ein
Mikrofon einschalten, dann wird lokal transkribiert.

| Anwendungsfall | Kurzbeschreibung |
|---|---|
| An einem Meeting teilnehmen | Mensch oder Agent tritt einer Session bei und erhält eine lokale Log-Datei, die laufend repliziert wird |
| Gesprochenes erfassen | Lokales Whisper transkribiert nur den eigenen Sprecher (Headset) und sendet Text, kein Audio |
| Mitschreiben und sich melden | Ein Agent führt Minutes, meldet sich per Text auf der Bühne und zeigt Artefakte |
| Bühne zeigen | Eine Web-Ansicht des Servers wird per Screenshare in einem beliebigen Meeting-Tool gezeigt |
| Artefakte erzeugen | Folien, Plan-Prüfungen und Journal-Einträge über CLI/MCP, ohne Streaming |
| Transparenz | Jeder Teilnehmer sieht, was von ihm aufgezeichnet und gesendet wurde, und kann pausieren oder zurückziehen |

Ausdrücklich **nicht** im ersten Schritt: Sprachausgabe (TTS), Video, Lautsprecherbetrieb,
Besprechungsräume mit mehreren Sprechern an einem Mikrofon und eine Integration in Meeting-Tools.

## 1.2 Qualitätsziele

Die Qualitätsziele, in dieser Reihenfolge: **Vertraulichkeit** (Rohaudio verlässt das Gerät nie,
erfasst wird nur, wer eingewilligt hat), **Performance** (Gesagtes steht ohne spürbare
Verzögerung im Session-Log) und **Usability** (Teilnehmer sehen, was von ihnen erfasst wurde).

See [10-quality-requirements.arc42.md](10-quality-requirements.arc42.md) for the complete
quality catalog with priorities and measurable scenarios.

## 1.3 Stakeholder

| Rolle | Kontakt | Erwartungen |
|---|---|---|
| Initiator / Produktverantwortung | Repo-Owner (OPEN: Name) | Tragfähiges Gesamtbild, früh benutzbar im eigenen Team |
| Menschliche Meeting-Teilnehmer | Team | Kontrolle darüber, was erfasst wird; keine Reibung im gewohnten Meeting-Tool |
| Agenten (z. B. Claude Code, eigene Agent-SDK-Agenten) | — | Einfache, dateibasierte Schnittstelle; keine Spezial-Features des Clients nötig |
| Agenten-Entwickler | OPEN | Stabile CLI/MCP-Verträge, maschinenlesbares Event-Format |
| Datenschutz (DSGVO, § 201 StGB) | OPEN | Einwilligung, Datensparsamkeit, Löschbarkeit, Nachvollziehbarkeit |
| Betrieb | OPEN | Einfaches Self-Hosting des Servers (OPEN: Self-Hosting oder Cloud?) |
