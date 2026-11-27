# Umstellung: von den Einzel-Agenten zu einem Protokoll

Reihenfolge so, dass zu keinem Zeitpunkt etwas fehlt, was heute geht. Die API
spricht während der ganzen Zeit v1 und v2.

## 1. Windows (jetzt)

Windows hat heute nichts — hier geht der neue Agent zuerst und voll in
Betrieb.

1. Website-PR: Protokoll v2, Kerberos-Prüfer, /stationen, GPO-Ordner.
2. Dienstkonto + Keytab (`ad-dc/gpo-rosenweg-agent/kerberos-dienstkonto.sh`),
   Keytab als Secret in die API.
3. Release 0.1.0 dieses Repos, Prüfsumme in `einrichten.sh` eintragen.
4. Erst **win11-1 allein** (Testplan in [windows.md](windows.md)), dann
   GPO für OU=Windows.

## 2. Linux: parallel (zur Probe)

Ziel: der neue Agent läuft auf einer Station *neben* `station-agent.sh`, mit
`"modus": "parallel"` in `/etc/rosenweg-agent/agent.json`. Er meldet sein
Inventar (`agent_inventar`, auf /stationen als „🧪 zur Probe"), führt nichts
aus und sperrt nichts. So lässt sich über Tage vergleichen, ob er dasselbe
sieht.

- os-stationen `pakete/`: Paket `rosenweg-agent` (Binary aus dem Release,
  Prüfsumme fest wie bei den anderen Paketen) + Unit
  `packaging/linux/rosenweg-agent.service`; zuerst nur auf `desktop-test`.
- Kein neues Geheimnis: Token und Kennung kommen aus `/etc/rosenweg-station/`.

## 3. Linux: gleichwertig machen

Was `station-agent.sh` heute zusätzlich tut und der neue Agent vor der
Ablösung können muss:

| Aufgabe heute | im neuen Agenten | API |
|---|---|---|
| Lebenszeichen + Zustand (`/run/station/status.json`, `stationctl status`) | Inventar ✓; `status.json` schreiben fehlt | – |
| `anmeldung` (sssd-Prüfung, 26.11.2026) | als `zusatz.anmeldung_*` | – |
| MQTT retained `stations/<id>/status` | fehlt | – |
| Sperre über `station-sperre` | ✓ (ruft dasselbe Skript) | ✓ |
| SSH-Wartung (`ssh_wartung` in der Antwort) | fehlt | v2-Antwort um `ssh_wartung` ergänzen |
| Update anfordern (`update`) | fehlt → Befehl `update` (startet `station-update.service`) | Befehl statt Flag |
| Zurücksetzen (`zuruecksetzen`) | fehlt → Befehl `zuruecksetzen` (bleibt stehen bis Abmeldung, wie heute) | Zustand, nicht Befehl |
| Logs einsenden (`station-log-push.sh`, v1 `/logs`) | bleibt vorerst eigenständig | später v2 `/logs` |

Die Gesundheitsprüfung, `stationctl`, `station-update`, Persist-Schlüssel und
Konfiguration (`/:id/config`) bleiben, wo sie sind — sie sind keine Aufgaben
des Lebenszeichens.

## 4. Linux: ablösen

Wenn 3. fertig und auf desktop-test eine Woche parallel ohne Abweichung lief:

1. `modus` auf `voll`, `station-agent.timer` auf derselben Station aus.
2. Rolle für Rolle (desktop, laptop, display) über den Update-Kanal `test`,
   dann `stable`.
3. `station-agent.sh` bleibt eine Fassung lang im Image (Rückweg), dann weg.
4. v1 `/:id/seen` in der API erst entfernen, wenn keine Station es mehr
   ruft (Zugriffe zählen).

## Kiosk-Fernseher

Der Kiosk (rosenweg-tv-kiosk, Android TV) wird gerade an /stationen
gekoppelt (Website-Zweig im Worktree `wt-kiosk-st`): eigenes Token
`rw_station_…`, Kopplung mit Code auf dem Bildschirm, Lebenszeichen über
`/:id/seen`, Antwort mit der Kiosk-Konfiguration, PIN-Meldung über
`PUT /:id/kiosk/pin`, Neustart der Diashow als Flag.

Das passt zu v2 fast ohne Änderung. Vorschlag, *nachdem* beide PRs gemergt
sind (nicht vorher — sonst bauen zwei Zweige an derselben Stelle):

| Kiosk heute | in v2 |
|---|---|
| `POST/GET /kiosk/kopplung` | `POST/GET /v2/kopplung` mit `plattform: "android-tv"` — derselbe Code, nur allgemein; `/kiosk/kopplung` bleibt als Alias |
| `POST /:id/seen` mit eigenem Zustand | `POST /v2/lebenszeichen`, `agent.plattform: "android-tv"`; Modell, Android-Version, MACs ins gemeinsame Inventar, Kiosk-Eigenes (`ansicht`, Diashow-App) nach `zusatz` |
| Antwort = Kiosk-Konfiguration | dieselbe Antwort plus `konfiguration.kiosk` (heutiges `kioskAntwort`) |
| `PUT /:id/kiosk/pin` | `meldungen: [{art: "kiosk-pin", …}]` oder eigener v2-Endpunkt — die PIN-Logik (neueste Änderung gewinnt) bleibt in `lib/kiosk.js` |
| Neustart-Flag | Befehl `kiosk_neustart` (Plattform `android-tv`), mit Quittung |
| `kioskDarf` (Pfad-Liste) | `role = 'kiosk'` spricht nur v2, wie Windows |

Die App (Kotlin) braucht dafür kein Go: das Protokoll ist JSON, der
Kotlin-Client liegt im Kiosk-Repo.

## Chromecasts

Bleiben agentenlos; der Cast-Wächter der API fragt sie ab. Nichts zu tun.
