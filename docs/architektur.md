# Architektur: ein Agent für alle Geräte

Stand 27.11.2026. Anlass: die Windows-PCs der Domäne (win11-1, R9-PC1 …)
standen auf [/stationen](https://www.rosenweg4303.ch/stationen.html) nur aus
dem AD-Abgleich oder von Hand eingetragen — ohne Zustand, ohne Sperre, ohne
Anschluss-Wächter. Stefan: „ganz schlecht" und „den Agenten für alle Systeme,
die wir haben, einheitlich machen".

## Was es im Haus gibt

| Gerät | Heute | Mit diesem Agenten |
|---|---|---|
| Linux-Station (os-stationen: desktop, laptop, display) | `station-agent.sh` (Bash, Timer), Protokoll v1 | rosenweg-agent, Protokoll v2 — nach der [Umstellung](umstellung.md) |
| Windows-PC der Domäne | kein Agent | **rosenweg-agent als Dienst**, Protokoll v2 |
| Kiosk-Fernseher (Android TV, rosenweg-tv-kiosk) | eigene Kopplung, Lebenszeichen über `/:id/seen` | dasselbe Protokoll v2 aus der App (Kotlin) — [Umstellung](umstellung.md#kiosk-fernseher) |
| Chromecast | agentenlos, Cast-Wächter der API | bleibt so |
| Mac | — | gibt es im Haus nicht, wird nicht gebaut |

Gemeinsam ist das **Protokoll**, nicht zwingend das Programm: der Fernseher
spricht es aus seiner App, die Desktop-Systeme mit diesem Programm.

## Bausteine

```
 Windows-PC / Linux-Station                              Rosenweg-API (Website)
┌──────────────────────────────┐   HTTPS, nur ausgehend   ┌─────────────────────────────────┐
│ rosenweg-agent               │ ───────────────────────► │ /api/stations/v2/anmelden        │
│  agent:     Schleife         │   Negotiate (Kerberos)   │   kerberos-pruefer (Go, gokrb5)  │
│  plattform: Inventar,        │   bzw. Bearer-Token      │ /api/stations/v2/lebenszeichen   │
│             Sperre, Befehle  │ ◄─────────────────────── │   Sperre, Befehle, Intervall     │
│  protokoll: v2 (JSON)        │   Antwort                │ Tabelle stations (wie v1)        │
└──────────────────────────────┘                          │ Tabelle station_befehle          │
                                                          └───────────────┬─────────────────┘
                                                                          │
                                                         /stationen (Gruppe technik)
```

- **Kein offener Port** auf dem Gerät. Alles, was die Verwaltung will,
  kommt als Antwort auf das Lebenszeichen (alle 120 s, von der API
  einstellbar 30–900 s).
- **Dieselbe Tabelle** `stations` wie bisher: ein Windows-PC aus dem
  AD-Abgleich und derselbe PC mit Agent sind *eine* Zeile. Der Agent
  meldet sich unter dem Namen seines Computerkontos an, die API sucht die
  Zeile mit `role = 'windows'` und diesem Namen (und stösst den
  AD-Abgleich an, wenn es sie noch nicht gibt).
- **Die API spricht beide Protokolle**, solange os-stationen umgestellt
  wird. v1 bleibt unverändert; ein Windows-Token gilt dort nicht.

## Anmeldung

### Windows: Kerberos mit dem Computerkonto, kein gespeichertes Geheimnis

Der Dienst läuft als `LocalSystem`. Im Netz ist das das Computerkonto
(`WIN11-1$`). Über SSPI (`Negotiate`) holt er beim DC ein Dienstticket für
`HTTP/www.rosenweg4303.ch` und schickt es an `POST /api/stations/v2/anmelden`.
Die API prüft es mit der Keytab des Dienstkontos `stationen-api` und gibt ein
Stationstoken `rw_station_…` heraus.

Warum so:

| Weg | Bewertung |
|---|---|
| Token im SYSVOL / in der GPO | Nein. SYSVOL liest jedes Konto der Domäne. |
| Token je PC, von Hand eingetragen | Nein. Niemand richtet einen Domänen-PC „ein" — er bekommt alles per Gruppenrichtlinie. |
| Token je PC, beim ersten Lauf per Kerberos ausgestellt, in DPAPI (LocalMachine) gespeichert | Geht, aber DPAPI LocalMachine entschlüsselt jeder Prozess auf dem PC; der Schutz wäre allein die Dateiberechtigung. |
| **Kerberos bei jedem Dienststart, Token nur im Speicher** | **Gewählt.** Auf der Platte liegt nichts von uns. Den Schlüssel des Computerkontos hat Windows ohnehin, erneuert ihn alle 30 Tage selbst, und er ist nur dem PC und dem DC bekannt. Startet der Dienst neu, gibt es ein neues Token, das alte ist wertlos — zwei Geräte mit demselben Token kann es nicht geben. |
| Kerberos bei jedem Lebenszeichen | Unnötig: der Prüfprozess liefe alle zwei Minuten je PC; das Token tut es auch. |

Was die API prüft (Website: `api/lib/kerberos.js`, `api/kerberos-pruefer/`,
`api/lib/stationprotokoll.js`):

- Ticket mit dem Schlüssel aus der Keytab entschlüsselt, Authenticator
  passend, Uhr höchstens 5 min daneben, Ticket nicht abgelaufen;
- Dienstname genau `STATION_KRB5_SPN` (nicht irgendein Name aus der Keytab);
- Mechanismus Kerberos (NTLM wird abgewiesen, mit Hinweis „SPN im AD?");
- Name endet auf `$` (Computerkonto — Personen bekommen vom KDC auch ein
  Ticket, sind aber kein Rechner) und Realm ist der des AD;
- dasselbe Token nicht zweimal innert 10 min (Wiederholung);
- nur aus dem Hausnetz;
- die Zeile ist `role = 'windows'` und nicht auf /stationen entfernt; eine
  Linux-Station gleichen Namens ergibt 409.

Warum der Prüfer ein eigenes Go-Programm ist: die API läuft auf
`node:22-alpine`. Das npm-Paket `kerberos` brächte libkrb5, Python und einen
Compiler ins Image und C-Code in den API-Prozess. gokrb5 ist reines Go, das
Ergebnis eine statische Datei, die die API wie `openssl` mit `execFile`
aufruft (Token über stdin, ohne Umgebung). Gebraucht wird er selten.

Restrisiko: Mit `ms-DS-MachineAccountQuota` (Vorgabe 10) kann jedes Konto der
Domäne Computerkonten anlegen und sich damit als *neuer* PC melden — nicht
als ein bestehender. Ein neuer PC kommt aber nur über den AD-Abgleich auf
/stationen (Betriebssystem Windows, OU), und Technik sieht ihn dort.
Empfehlung: Quota auf 0 setzen (Website `ad-dc/gpo-rosenweg-agent/README.md`).

### Linux: das Stationstoken aus der Einrichtung

Eine os-stationen-Station hat seit `install.sh` Kennung und Token unter
`/etc/rosenweg-station/`. Der Agent liest sie und braucht keine zweite
Registrierung. Kerberos mit der Maschinenkonto-Keytab (`/var/lib/station/ad/
krb5.keytab`) wäre möglich, bringt hier aber nichts: das Token gibt es schon,
und eine Anzeige (Rolle `display`) ist nicht in der Domäne.

### Geräte ohne Domäne und ohne Einrichtung: Geräte-Code

Für Geräte, die weder Kerberos noch einen Installer mit Anmeldung haben (der
Kiosk-Fernseher): das Gerät erzeugt sein Token selbst, schickt nur den
sha256 und zeigt einen Code; Technik bestätigt ihn auf /stationen. So baut es
der Kiosk gerade (`/api/stations/kiosk/kopplung`). Vorschlag: diese Kopplung
unverändert als `/api/stations/v2/kopplung` für alle Arten öffnen
([Umstellung](umstellung.md#kiosk-fernseher)).

## Lebenszeichen und Inventar

Ein Schema für alle Plattformen ([protokoll-v2.md](protokoll-v2.md)):
Hostname, Betriebssystem (Name, Version, Build, Kernel), Laufzeit, angemeldete
Personen, Netzadapter mit MAC/Art/IPv4/IPv6, Platten mit frei/gesamt,
Verschlüsselung (BitLocker), Updates (zuletzt, Neustart ausstehend, letzte
zehn), plattformeigenes unter `zusatz` (Secure Boot, Domäne, Rolle …).

Die API stutzt alles auf bekannte Felder (`inventarBereinigen`) und leitet
daraus die Felder ab, die /stationen und die Wächter aus v1 kennen
(`ip`, `uptime_seconds`, `os_version`, `disk_percent`). Dadurch gelten für
Windows dieselben Regeln wie für Linux:

- **Doppelte Station**: dieselbe Prüfung wie bei v1 (Quelladresse,
  Startzeit).
- **Anschluss-Wächter**: die MACs von Kabel- und WLAN-Adaptern landen in
  `hardware.macs`; virtuelle Adapter (Hyper-V, VPN, WireGuard) nicht. Windows
  steht nicht mehr in `ANSCHLUSS_OHNE`. Ein PC ohne Agent hat keine MACs und
  wird wie bisher übersprungen.

BitLocker, Update-Verlauf und Secure Boot liest ein eingebettetes
PowerShell-Skript (`internal/plattform/langsam_windows.ps1`) alle 30 Minuten
— dafür gibt es nur WMI/COM. Alles andere kommt aus Win32 (Sitzungen,
Adapter, Platten, Registry).

## Befehle

| Befehl | Windows | Linux | Wirkung |
|---|---|---|---|
| `nachricht` | ✓ | ✓ | Fenster bei allen Angemeldeten (WTSSendMessage / notify-send + wall) |
| `abmelden` | ✓ | ✓ | Meldung, nach 60 s abmelden (WTSLogoffSession / loginctl) |
| `neustart` | ✓ | ✓ | `shutdown /r /t 300` mit Text (vor Ort mit `shutdown /a` abbrechbar) / `shutdown -r +5` |
| `netzstart_einmal` | ✓ | – | `netzstart-einmal.ps1 -Setzen` (os-stationen/sicherung/windows, unverändert wiederverwendet), dann Neustart nach 120 s |
| `inventar` | ✓ | ✓ | volles Inventar mit dem nächsten Lebenszeichen |

- angelegt nur von der Gruppe **technik** (nicht Präsident/Admin), jeder mit
  Ereignis und Name;
- gültig 15 min (Nachricht 60 min) — ein PC, der übers Wochenende aus war,
  startet am Montag nicht als Erstes neu;
- **höchstens einmal** ausgeliefert, immer quittiert (`ok`, `fehler`,
  `abgelehnt`); ohne Quittung steht „abgeholt, Quittung fehlt" — erneut
  geschickt wird nichts;
- zurückziehen, solange er wartet.

## Sperre

Ein **Zustand**, kein Befehl: `gesperrt` und `sperr_grund` stehen in jeder
Antwort, wie bei v1. Der Agent setzt sie bei jedem Lebenszeichen durch,
speichert sie (`sperre.json`) und stellt sie nach einem Neustart ohne Netz
sofort wieder her. Aufgehoben wird nur durch eine echte Antwort — ein
Netzausfall entsperrt nicht.

Windows: „Lokal anmelden verweigern" und „Anmelden über Remotedesktop
verweigern" für Domänen-Benutzer (SID …-513, sprachunabhängig), Grund im
Anmeldehinweis, Angemeldete bekommen eine Meldung und nach einer Minute den
Sperrbildschirm (Sitzung getrennt, nichts geht verloren). Der lokale
Administrator kommt weiter herein — wie root an einer gesperrten Linux-Station.
Linux: `station-sperre an|aus` aus os-stationen.

Wird der PC auf /stationen entfernt, weist die API die Anmeldung endgültig
ab; der Agent hebt dann eine Sperre auf (niemand könnte sie sonst je
aufheben) und fragt nur noch stündlich.

## Verteilung und Aktualisierung

| | Wie | Wo |
|---|---|---|
| Windows | Gruppenrichtlinie „Rosenweg-Agent" an OU=Windows: Startskript `agent-installieren.ps1` aus dem SYSVOL installiert den Dienst; die geplante Aufgabe „Rosenweg-Agent aktualisieren" (03:30, 12:30) holt neue Fassungen aus dem SYSVOL. Prüfsumme neben der exe; `AUS` im SYSVOL entfernt den Agenten überall. | Website `ad-dc/gpo-rosenweg-agent/` |
| Linux | Paket aus `pakete/` von os-stationen, systemd-Unit `packaging/linux/rosenweg-agent.service` | [umstellung.md](umstellung.md) |
| Release | GitHub-Release mit Binaries je Plattform und `SHA256SUMS` (`.github/workflows/release.yml`) | dieses Repo |

## Warum Go

| | Go | .NET | PowerShell | Rust | Python |
|---|---|---|---|---|---|
| Laufzeit auf dem Gerät | keine (statisch) | .NET-Runtime oder 60 MB self-contained | PS 5.1 da, Linux nein | keine | Interpreter + Pakete |
| Windows-Dienst | `x/sys/windows/svc` | gut | nur über Aufgabenplanung | ja, mehr Handarbeit | pywin32 |
| SSPI/Kerberos | `alexbrainman/sspi` | eingebaut | eingebaut | Crates | mühsam |
| Kreuzübersetzen vom Mac | `GOOS=windows go build` | ja | – | Toolchains je Ziel | – |
| ein Code für Linux und Windows | ja | ja | nein | ja | ja |
| im Haus schon da | Traefik-Plugin `echteip`, Kerberos-Prüfer | – | GPO-Skripte | – | Skripte |

PowerShell allein hätte unter Windows gereicht, aber nicht für Linux; .NET
bringt eine Laufzeit mit, die auf Linux-Stationen niemand will; Rust kann
dasselbe wie Go, mit mehr Aufwand und ohne Vorteil hier. Go: eine Datei je
Plattform (~7 MB), keine Abhängigkeiten auf dem Gerät, dieselbe Schleife,
dieselben Tests überall.

## Protokoll und Logs

- Windows: Ereignisanzeige → Anwendung, Quelle **Rosenweg-Agent**; Datei
  `%ProgramData%\Rosenweg\Agent\agent.log` (1 MB, eine Vorgängerdatei);
  Installation in `installieren.log` daneben. Der Ordner ist nur für
  SYSTEM und Administratoren.
- Linux: Journal (`journalctl -u rosenweg-agent`).
- API: `station_events` (auf /stationen unter der Station), `station_befehle`
  (Befehle und Verlauf).
