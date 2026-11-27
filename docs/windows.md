# Windows: Installation, Test, Entfernen

## Was auf dem PC liegt

| Was | Wo |
|---|---|
| Programm | `C:\Program Files\Rosenweg\Agent\rosenweg-agent.exe` |
| Netzstart-Skript (os-stationen) | `C:\Program Files\Rosenweg\Agent\netzstart-einmal.ps1` |
| Einstellungen (ohne Geheimnisse) | `C:\ProgramData\Rosenweg\Agent\agent.json` |
| Protokolle, Sperrzustand | `C:\ProgramData\Rosenweg\Agent\` (nur SYSTEM und Administratoren) |
| Dienst | `RosenwegAgent` („Rosenweg-Agent"), LocalSystem, automatisch (verzögert), Neustart bei Absturz |
| Aufgabe | „Rosenweg-Agent aktualisieren", 03:30 und 12:30, SYSTEM |
| Ereignisanzeige | Anwendung, Quelle „Rosenweg-Agent" |

Kein Token, kein Passwort, kein Schlüssel auf der Platte.

## Von Hand (Administrator-PowerShell)

```powershell
.\rosenweg-agent.exe inventar       # was er melden würde, ohne Netz
.\rosenweg-agent.exe probe          # anmelden + ein Lebenszeichen, ohne Befehle/Sperre
.\rosenweg-agent.exe installieren   # Dienst anlegen/anpassen und starten
.\rosenweg-agent.exe entfernen      # Sperre aufheben, Dienst + Ereignisquelle entfernen
```

`probe` und `inventar` als Administrator laufen unter *dessen* Konto, nicht
als SYSTEM: Kerberos meldet dann die Person an (die API lehnt ab, „Nur
Computerkonten"). Als SYSTEM prüfen:

```powershell
psexec -s -i powershell     # Sysinternals; oder eine geplante Aufgabe als SYSTEM
& 'C:\Program Files\Rosenweg\Agent\rosenweg-agent.exe' probe
```

## Testplan für win11-1 (VM 107 auf pve3)

Nur mit Stefans OK. Vorher in Proxmox einen Snapshot von VM 107 anlegen
(„vor-rosenweg-agent"). Alles ist rückgängig zu machen
(`rosenweg-agent entfernen`, Snapshot).

**Vorbereitung (einmal, ohne win11-1 zu berühren)**

1. Website-PR deployen (API mit `kerberos-pruefer` im Image).
2. Auf dc1: `ad-dc/gpo-rosenweg-agent/kerberos-dienstkonto.sh`; Keytab als
   Secret in die API, `STATION_KRB5_KEYTAB` setzen, API neu.
3. Probe ohne PC, auf dc1 oder einer Linux-Station mit Kerberos:
   `kinit -k 'DC1$'` und `curl --negotiate -u : -X POST
   https://www.rosenweg4303.ch/api/stations/v2/anmelden -H 'Content-Type: application/json'
   -d '{"agent":{"plattform":"windows"}}'` → erwartet **404** oder **409**
   („steht nicht auf /stationen" bzw. Linux-Station) — beweist: Ticket kommt
   an, Keytab passt. 401 mit „NTLM" → SPN fehlt; 401 „abgelehnt" → Keytab
   passt nicht (kvno, Verschlüsselung).

**Auf win11-1, von Hand (noch keine GPO)**

4. `rosenweg-agent.exe` und `netzstart-einmal.ps1` nach
   `C:\Program Files\Rosenweg\Agent\` kopieren.
5. `rosenweg-agent.exe inventar` — prüfen: Windows 11 + Build, Adapter
   „Ethernet" mit der MAC aus Proxmox (`bc:24:11:…`, Art `kabel`), C: frei,
   BitLocker, Updates, `secure_boot`.
6. Als SYSTEM `rosenweg-agent.exe probe` — Antwort mit `station_id: win11-1`.
   Auf /stationen dieselbe Zeile wie bisher (aus dem AD, nicht doppelt),
   darunter „🧪 Rosenweg-Agent 0.1.0 zur Probe" (probe meldet im Modus
   `parallel`: keine Befehle, keine Sperre).
7. `rosenweg-agent.exe installieren` → Dienst läuft; Ereignisanzeige
   „Angemeldet als win11-1"; auf /stationen „zuletzt gesehen: gerade eben",
   Gerät-Spalte mit System, Person, Platte, BitLocker.
8. Befehle von /stationen, je einer, Ergebnis in „Befehle und Verlauf":
   - Nachricht → Fenster in der RDP-/Konsolensitzung.
   - Neu starten → Windows-Warnung mit 5 min; mit `shutdown /a` abbrechen
     (Quittung bleibt „ok: Neustart in 5 min").
   - Abmelden → Meldung, nach 60 s abgemeldet.
9. Sperren mit Grund „Test" → innert 2 min Meldung, nach 1 min
   Sperrbildschirm; Entsperren mit Domänenkonto geht **nicht** (prüfen!),
   Anmeldehinweis zeigt den Grund; lokaler Administrator kommt herein.
   Neustart ohne Netzkabel (Proxmox: Netz trennen) → nach dem Start weiter
   gesperrt. Netz wieder an, Entsperren → Anmeldung geht, Hinweis wie vorher.
10. Anschluss-Wächter: nach dem ersten Lauf steht „🔌 Anschluss … Port …"
    (pve3); kein Sperren, solange die VM auf pve3 bleibt. **Achtung**: eine
    Migration auf einen anderen Knoten sieht für den Wächter wie ein Umzug
    aus → vorher „Anschluss neu lernen (Umzug)".
11. Netzstart einmal: nur, wenn die Sicherung von win11-1 ohnehin ansteht
    (Secure Boot muss aus sein; die Seite verweigert sonst). Erwartet:
    Quittung „Gewaehlt: {…} … Neustart vom Netz in 2 min", danach
    Sicherungs-System.
12. Entfernen: `rosenweg-agent.exe entfernen` → Dienst weg, Sperre (falls
    an) aufgehoben, Ereignisquelle weg.

**Dann über die Gruppenrichtlinie**

13. Auf dc1 `einrichten.sh --version 0.1.0 --netzstart …` (OU=Windows; heute
    nur win11-1 und ggf. R9-PC1 darin — vorher prüfen, wer in der OU liegt).
14. win11-1 neu starten → `installieren.log`, Dienst, Aufgabe da.
15. Neue Fassung ins SYSVOL, Aufgabe von Hand starten → ersetzt, Dienst
    läuft wieder.
16. `einrichten.sh --aus`, Aufgabe starten → alles weg; `--an` → wieder da.

## Bekannte Grenzen

- Eine Gruppenrichtlinie, die „Lokal anmelden verweigern" selbst setzt,
  überschreibt die Sperre bei jedem gpupdate; der Agent setzt sie beim
  nächsten Lebenszeichen wieder. Heute setzt keine GPO im Haus das Recht.
- Domänen-Admins sind auch Domänen-Benutzer: an einem gesperrten PC kommt
  nur der lokale Administrator herein.
- BitLocker/Updates werden alle 30 min gelesen (oder Befehl „Inventar").
- WLAN-PCs, die zwischen APs wandern, würde der Anschluss-Wächter sperren —
  heute hat kein Domänen-PC WLAN. Für einen Laptop: Rolle anpassen oder
  `ANSCHLUSS_OHNE` erweitern.
