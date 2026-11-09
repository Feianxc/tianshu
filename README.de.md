<div align="center">

<img src="docs/images/mark.svg" width="72" alt="Tianshu-Logo: der Große Wagen mit seinem ersten Stern Dubhe hervorgehoben">

# Tianshu 天枢

**Eine selbst gehostete Steuerzentrale für die Server, Geräte, Dienste, Projekte und Repositorys einer Person – mit einem schreibgeschützten MCP-Server, damit KI-Agenten dasselbe sehen wie Sie.**

[English](README.md) · [简体中文](README.zh-CN.md) · [繁體中文](README.zh-TW.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Français](README.fr.md) · [Deutsch](README.de.md) · [Português](README.pt-BR.md) · [Русский](README.ru.md)

</div>

![Tianshu-Übersicht: Live-Sternkarte, Telemetrie der letzten 60 Minuten pro Host und offene Punkte](docs/images/overview-dark.png)

Tianshu (天枢, *Dubhe*, der erste Stern des Großen Wagens) ist eine einzelne Go-Binärdatei mit eingebetteter Weboberfläche. Sie beantwortet auf einen Blick eine Frage: **Braucht gerade etwas meine Aufmerksamkeit?** Tianshu führt einen Katalog Ihrer Bestände, sammelt echte Belege über deren Zustand, verdichtet sie zu einer kurzen Liste von Befunden und stellt dasselbe Lesemodell KI-Agenten über das [Model Context Protocol](https://modelcontextprotocol.io) bereit.

## Funktionen

- Geräte, Dienste, Projekte, Repositorys und **Verweise** auf Zugangsdaten, getrennt nach Arbeitsbereichen (privat, Firma, Kunden).
- Live-Sternkarte: eine Umlaufbahn pro Arbeitsbereich, Form und Farbe zeigen den Zustand; bei jeder Meldung eines Hosts läuft ein Impuls zur Mitte.
- Telemetrieband: die letzten 60 Minuten echter Meldungen pro Host. Fehlende Daten bleiben eine Lücke, nie „OK“.
- Zustand, Aktualität, Lebenszyklus und Abnahme werden getrennt geführt; veraltete Daten gelten nie als gesund.
- Deduplizierte Befunde, die sich von selbst schließen, sobald das Problem behoben ist; Erinnerung um einen Tag, eine Woche oder einen Monat verschieben.
- Agent für Linux und Windows, schreibgeschützte SSH-Abfrage, HTTP-, TCP- und TLS-Prüfungen.
- Überwachung der SSH-Angriffsfläche: Passwort-Login, fehlgeschlagene Anmeldungen, fail2ban, Schlüssel-Fingerabdrücke.
- MCP-Server (Bearer-Tokens und OAuth 2.1), Trennung der Arbeitsbereiche, keine gespeicherten Geheimnisse, Audit jedes Aufrufs.
- Anmeldung per Gerätekopplung, Befehlspalette (`Ctrl K`), dunkles und helles Design, mobiles Layout und Android-Hülle.

## Schnellstart

Benötigt Go 1.27+ und Node.js 20+.

```bash
git clone https://github.com/Feianxc/tianshu.git && cd tianshu
bash scripts/build.sh
export WANGLOU_DATA=./data
.build/wanglou import examples/catalog   # optional: fiktive Demodaten
.build/wanglou init                      # zeigt das Admin-Token einmalig an
.build/wanglou serve                     # http://127.0.0.1:8787
```

Mehr dazu unter [Architektur](docs/ARCHITECTURE.md) und [Bereitstellung](docs/DEPLOYMENT.md) (auf Englisch). Die Oberfläche ist derzeit in vereinfachtem Chinesisch.

## Lizenz

[MIT](LICENSE) © 2026 Feianxc
