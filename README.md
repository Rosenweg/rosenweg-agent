<p align="center"><img src="assets/logo/rosenweg-agent-512.png" width="96" alt="Logo"></p>
<h1 align="center">Rosenweg-Agent</h1>
<p align="center">Ein Agent für alle Rechner im Rosenweg: meldet Windows-PCs und Linux-Stationen an /stationen und führt aus, was die Technik dort auslöst.</p>
<p align="center"><a href="https://www.rosenweg4303.ch/stationen.html">Stationen</a> · <a href="docs/architektur.md">Architektur</a> · <a href="docs/protokoll-v2.md">Protokoll</a> · <a href="docs/windows.md">Windows</a> · <a href="docs/umstellung.md">Umstellung</a></p>

## Inhalt

- [Überblick](#überblick)
- [Läuft auf](#läuft-auf)
- [Was er meldet](#was-er-meldet)
- [Was die Technik auslösen kann](#was-die-technik-auslösen-kann)
- [Anmeldung ohne Geheimnis](#anmeldung-ohne-geheimnis)
- [Verteilung](#verteilung)
- [Bedienung](#bedienung)
- [Sicherheit](#sicherheit)
- [Aufbau](#aufbau)
- [Bauen und Prüfen](#bauen-und-prüfen)
- [Zusammenhang mit der übrigen Infrastruktur](#zusammenhang-mit-der-übrigen-infrastruktur)
- [Lizenz](#lizenz)

## Überblick

Auf [/stationen](https://www.rosenweg4303.ch/stationen.html) verwaltet die
Technik alle Rechner im Haus. Bis November 2026 sprach dort jede Geräteart
ihre eigene Sprache: die Linux-Stationen ein Lebenszeichen aus einem
Bash-Skript, die Windows-PCs der Domäne gar keine — sie standen nur aus dem
AD-Abgleich in der Liste, ohne Zustand, ohne Sperre, ohne Anschluss-Wächter.

Der Rosenweg-Agent ist ein Programm für alle Desktop-Systeme. Er spricht das
**Stationen-Protokoll v2** der Rosenweg-API: ein Lebenszeichen mit demselben
Inventar auf jeder Plattform, Befehle, die er dabei abholt und quittiert, und
die Sperre als Zustand in jeder Antwort. Auf dem Gerät ist kein Port offen.

| Gerät | Agent |
|---|---|
| Windows-PC der Domäne (win11-1, R9-PC1 …) | dieses Programm als Windows-Dienst, verteilt per Gruppenrichtlinie |
| Linux-Station (os-stationen) | dieses Programm als systemd-Dienst — löst `station-agent.sh` ab, sobald gleichwertig ([Umstellung](docs/umstellung.md)) |
| Kiosk-Fernseher (Android TV) | dasselbe Protokoll aus der App ([Vorschlag](docs/umstellung.md#kiosk-fernseher)) |
| Chromecast | kein Agent — die API fragt ihn selbst ab |

## Läuft auf

| | Windows | Linux |
|---|---|---|
| Version | Windows 10/11, x64 und ARM64 | Debian 13 (os-stationen), amd64 und arm64 (Raspberry Pi) |
| Läuft als | Dienst `RosenwegAgent` unter LocalSystem | `rosenweg-agent.service` als root |
| Braucht | nichts ausser Windows (PowerShell 5.1 ist dabei) | systemd, loginctl |
| Datei | eine `.exe`, ~7 MB, statisch | eine Datei, ~6 MB, statisch |

Macs gibt es im Haus nicht; das Programm lässt sich dort nur zum Entwickeln
bauen.

## Was er meldet

Alle 120 Sekunden (die API kann 30–900 s vorgeben):

| | Windows | Linux |
|---|---|---|
| System | Name, Version (24H2), Build mit UBR | os-release, Kernel |
| Laufzeit | seit dem Start | /proc/uptime |
| Angemeldet | Konsole und RDP, aktiv/getrennt | loginctl |
| Netz | Adapter mit MAC, Kabel/WLAN/virtuell, IPv4/IPv6, Standardroute | dasselbe aus /sys/class/net |
| Platten | feste Laufwerke, frei/gesamt | /, /persist, /home |
| Verschlüsselung | BitLocker je Laufwerk | – |
| Updates | letzter Erfolg, die letzten zehn, Neustart ausstehend | – |
| Zusatz | Secure Boot, Domäne | Rolle, OS- und Konfig-Stand von os-stationen |

Die MACs von Kabel und WLAN bindet die API an den **Anschluss-Wächter**:
Steckt jemand den PC um (oder ein fremdes Gerät mit seiner MAC an), wird er
gesperrt, bis die Technik den neuen Anschluss übernimmt — wie bei den
Linux-Stationen seit jeher.

## Was die Technik auslösen kann

Nur die Gruppe *technik*, jeder Befehl mit Namen protokolliert, höchstens
einmal ausgeführt, immer quittiert, nach 15 Minuten verfallen (Nachricht: 60):

| | |
|---|---|
| **Nachricht** | Fenster bei allen, die angemeldet sind, Absender „Technik Rosenweg" |
| **Abmelden** | Meldung, eine Minute später abgemeldet |
| **Neu starten** | Windows-Warnung, Neustart nach fünf Minuten (vor Ort mit `shutdown /a` abbrechbar) |
| **Einmal vom Netz starten** | für die Sicherung: setzt PXE als einmaliges Startziel mit `netzstart-einmal.ps1` aus os-stationen, startet nach zwei Minuten neu (nur Windows, nicht bei Secure Boot) |
| **Sperren** | ein Zustand, kein Befehl: Domänen-Konten können sich nicht mehr anmelden, Angemeldete bekommen nach einer Minute den Sperrbildschirm, der Grund steht vor der Anmeldung. Übersteht Neustart und Netzausfall; aufgehoben nur durch die Verwaltung |

## Anmeldung ohne Geheimnis

Ein Windows-PC weist sich mit seinem **Computerkonto** aus: der Dienst läuft
als SYSTEM, holt über Kerberos ein Ticket für `HTTP/www.rosenweg4303.ch` und
tauscht es bei der API gegen ein Stationstoken. Das Token lebt nur im
Speicher; startet der Dienst neu, gibt es ein neues. Auf der Platte liegt
nichts, was jemand kopieren könnte, und im SYSVOL steht kein Geheimnis. Die
Gründe und was die API alles prüft: [Architektur](docs/architektur.md#anmeldung).

Eine Linux-Station nimmt das Token, das sie bei der Einrichtung bekommen hat
(`/etc/rosenweg-station/token`).

## Verteilung

- **Windows**: Gruppenrichtlinie „Rosenweg-Agent" an OU=Windows. Das
  Startskript prüft die Prüfsumme, installiert den Dienst und legt eine
  Aufgabe an, die zweimal täglich neue Fassungen aus dem SYSVOL holt. Eine
  Datei `AUS` im SYSVOL entfernt den Agenten überall wieder. Skripte und
  Anleitung: [Website `ad-dc/gpo-rosenweg-agent/`](https://github.com/Rosenweg/Website/tree/main/ad-dc/gpo-rosenweg-agent).
- **Linux**: Paket in os-stationen `pakete/` mit
  [`packaging/linux/rosenweg-agent.service`](packaging/linux/rosenweg-agent.service).
- **Releases**: ein Tag `vX.Y.Z` baut alle Plattformen und hängt sie samt
  `SHA256SUMS` an das Release.

## Bedienung

```
rosenweg-agent inventar      was er melden würde, ohne Netz
rosenweg-agent probe         einmal anmelden und melden, ohne Befehle und Sperre
rosenweg-agent lauf          im Vordergrund
rosenweg-agent installieren  Windows: Dienst anlegen/anpassen und starten
rosenweg-agent entfernen     Windows: Sperre aufheben, Dienst entfernen
rosenweg-agent version
```

Einstellungen braucht es normalerweise keine. Für Abweichungen:
`%ProgramData%\Rosenweg\Agent\agent.json` bzw. `/etc/rosenweg-agent/agent.json`
mit `api`, `spn`, `modus` (`voll` oder `parallel`), `netzstart_skript`.

Protokoll: Windows in der Ereignisanzeige (Anwendung, Quelle
„Rosenweg-Agent") und in `%ProgramData%\Rosenweg\Agent\agent.log`; Linux im
Journal (`journalctl -u rosenweg-agent`). Test und Fehlersuche unter Windows:
[docs/windows.md](docs/windows.md).

## Sicherheit

- Nur ausgehende HTTPS-Verbindungen zur API; kein offener Port.
- Kein gespeichertes Geheimnis auf Windows; die Anmeldung prüft die API mit
  gokrb5 gegen die Keytab ihres Dienstkontos (nur AES, nur dieser
  Dienstname, nur Computerkonten, nur aus dem Hausnetz, keine Wiederholung).
- Befehle sind eine feste Liste mit geprüften Parametern — kein „führe
  diesen Befehl aus".
- Zustand und Protokolle unter ProgramData nur für SYSTEM und Administratoren.
- Ein Netzausfall hebt keine Sperre auf; wird ein PC auf /stationen
  entfernt, hebt der Agent eine Sperre auf, statt den PC für immer zu sperren.

## Aufbau

```
cmd/rosenweg-agent/           Befehlszeile, Dienststart
internal/agent/               die Schleife: anmelden, melden, sperren, Befehle (für alle gleich)
internal/protokoll/           Protokoll v2: Typen und HTTP-Client
internal/plattform/           was Windows und Linux verschieden machen
  windows.go, *_windows.go    SSPI-Anmeldung, Inventar (Win32), Sperre (LSA), Befehle (WTS, shutdown)
  langsam_windows.ps1         BitLocker, Update-Verlauf, Secure Boot (eingebettet)
  linux.go                    Token aus os-stationen, Inventar, station-sperre, loginctl
  *_rein.go                   Textverarbeitung ohne Systemaufrufe (überall getestet)
internal/dienst/              Windows-Dienst (svc, mgr, Ereignisanzeige)
internal/logbuch/             Datei + Ereignisanzeige bzw. Journal
internal/konfig/              agent.json
packaging/linux/              systemd-Unit
docs/                         Architektur, Protokoll, Windows, Umstellung
assets/logo/                  Logo (Baukasten aus rosenweg-basis, Zeichen: Puls)
```

## Bauen und Prüfen

```sh
make pruefen   # gofmt, go vet für Windows und Linux
make test      # Tests, u. a. die Schleife gegen eine Attrappe der API
make alle      # dist/: Windows und Linux, amd64 und arm64, SHA256SUMS
```

Go 1.26. Die CI prüft zusätzlich auf einem Windows-Läufer (Tests, Inventar,
PSScriptAnalyzer).

## Zusammenhang mit der übrigen Infrastruktur

| Repo | Was dort |
|---|---|
| [Website](https://github.com/Rosenweg/Website) | API (`api/routes/stationen-v2.js`, `api/lib/stationprotokoll.js`, `api/kerberos-pruefer/`), Seite `stationen.html`, Gruppenrichtlinie `ad-dc/gpo-rosenweg-agent/` |
| os-stationen | Linux-Stationen; `agent/` (bisheriger Agent, `station-sperre`), `sicherung/windows/netzstart-einmal.ps1` |
| rosenweg-basis | Logo-Baukasten |

## Lizenz

GPL-3.0-or-later, siehe [LICENSE](LICENSE).
