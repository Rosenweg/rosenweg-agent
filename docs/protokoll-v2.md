# Stationen-Protokoll v2

JSON über HTTPS, nur vom Gerät zur API. Basis `https://www.rosenweg4303.ch/api`.
Umsetzung der API: Website `api/routes/stationen-v2.js` (Routen),
`api/lib/stationprotokoll.js` (Prüfungen, getestet in
`api/test/stationprotokoll.test.js`). Felder heissen hier wie dort; wer eines
ändert, ändert beide Seiten und `internal/protokoll/protokoll.go`.

## Anmelden (Windows, Kerberos)

```
POST /stations/v2/anmelden
Authorization: Negotiate <SPNEGO-Token, Base64>
{ "protokoll": 2, "agent": {…}, "inventar": {…} }

200 { "protokoll": 2, "station_id": "win11-1", "token": "rw_station_<64 hex>", "intervall_sekunden": 120 }
401 + WWW-Authenticate: Negotiate   kein/ungültiges Ticket, Wiederholung ("grund" sagt warum)
403  kein Computerkonto, fremder Realm, nicht aus dem Hausnetz, auf /stationen entfernt
404  Rechner steht nicht auf /stationen (auch nach AD-Abgleich nicht)
409  Name gehört einer Station anderer Art (z. B. Linux)
503  Kerberos in der API nicht eingerichtet
```

403/404/409 sind endgültig: der Agent fragt danach nur noch stündlich und hebt
eine eigene Sperre auf.

Linux-Stationen melden sich nicht an — sie haben ihr Token aus `install.sh`.

## Lebenszeichen

```
POST /stations/v2/lebenszeichen
Authorization: Bearer rw_station_…
{
  "protokoll": 2,
  "agent": { "name": "rosenweg-agent", "version": "0.1.0", "plattform": "windows",
             "arch": "amd64", "modus": "voll" },
  "inventar": { … siehe unten … },
  "quittungen": [ { "id": 12, "ergebnis": "ok", "text": "Neustart in 5 min" } ],
  "meldungen":  [ { "art": "sperre-an", "text": "Anschluss gewechselt" } ]
}

200 {
  "ok": true, "protokoll": 2, "station_id": "win11-1",
  "intervall_sekunden": 120,
  "gesperrt": false, "sperr_grund": null,
  "modus": "voll",
  "befehle": [ { "id": 13, "art": "nachricht", "parameter": { "text": "…" },
                 "gueltig_bis": "2026-11-27T10:00:00.000Z" } ]
}
401  Token ungültig -> Windows: neu anmelden; Linux: Station entfernt?
400  protokoll ≠ 2 oder Plattform passt nicht zur Station (windows ↔ role windows,
     linux ↔ desktop/laptop/display)
```

- `agent.modus = "parallel"`: nur zusehen. Die API speichert das Inventar in
  `agent_inventar`, lässt `last_state`/`last_seen_at` dem alten Bash-Agenten
  und liefert keine Befehle; der Agent setzt keine Sperre um. Für die
  Umstellung der Linux-Stationen.
- `meldungen.art`: Kleinbuchstaben, Ziffern, Bindestrich; landet als
  Ereignis `agent-<art>` auf /stationen.
- Quittungen nur für abgeholte, noch offene Befehle der eigenen Station.

### Inventar

| Feld | Typ | Anmerkung |
|---|---|---|
| `hostname` | Text ≤ 63 | |
| `betriebssystem` | `{name, version, build, kernel}` | Windows 11 heisst in der Registry „Windows 10" — der Agent korrigiert an der Build-Nummer |
| `laufzeit_sekunden` | Zahl | Doppel-Erkennung rechnet daraus die Startzeit |
| `personen[]` | `{name, sitzung, zustand: aktiv\|getrennt\|andere}` | ≤ 20 |
| `netz[]` | `{name, mac, art: kabel\|wlan\|andere, verbunden, standard, ipv4[], ipv6[]}` | ≤ 16; `andere` = virtuell, nicht für den Anschluss-Wächter |
| `platten[]` | `{laufwerk, gesamt_bytes, frei_bytes}` | |
| `verschluesselung[]` | `{laufwerk, schutz: an\|aus\|unbekannt, zustand, prozent}` | BitLocker |
| `updates` | `{zuletzt, neustart_ausstehend, liste[{titel, datum, ok}], fehler}` | |
| `zusatz` | flaches Objekt, ≤ 30 Schlüssel `[a-z][a-z0-9_]*`, Text/Zahl/bool | `secure_boot`, `domaene`, `rolle`, `os_stationen`, `konfig` |

Was die API daraus ableitet und in `last_state` neben das Inventar legt
(damit Wächter und Seite wie bei v1 lesen): `station_id, hostname, ip,
os_version, uptime_seconds, disk_percent, ts, epoch, protokoll,
agent_version, plattform`.

## Befehle (Verwaltung)

```
GET    /stations/v2/admin/:id/befehle          letzte 30, mit Ergebnis
POST   /stations/v2/admin/:id/befehle          { "art": "...", "parameter": {...} }
DELETE /stations/v2/admin/:id/befehle/:bid     zurückziehen, solange unabgeholt
```

Nur Gruppe `technik`. Arten und Parameter:

| art | parameter | gültig |
|---|---|---|
| `nachricht` | `text` (Pflicht, ≤ 500) | 60 min |
| `abmelden` | `vorwarnung_sekunden` 30–900 (60), `text` | 15 min |
| `neustart` | `verzoegerung_sekunden` 60–3600 (300), `text` | 15 min |
| `netzstart_einmal` | `verzoegerung_sekunden` 60–3600 (120), `text` — nur Windows | 15 min |
| `inventar` | – | 15 min |

Ergebnis: `ok`, `fehler`, `abgelehnt` (vom Agenten), `verfallen` (nie
abgeholt), `zurueckgezogen` (von Technik).

## Kopplung mit Geräte-Code (Vorschlag)

Für Geräte ohne Domäne und ohne Installer (Kiosk-Fernseher). Wie die
Kiosk-Kopplung, die gerade entsteht, nur unter v2 und für jede Art:

```
POST /stations/v2/kopplung   { token_hash, plattform, inventar }  -> { code, link, qr, gueltig_bis }
GET  /stations/v2/kopplung   Authorization: Bearer <eigenes Token> -> wartet | gekoppelt
```

Das Gerät erzeugt sein Token selbst und schickt nur den sha256; bestätigen
kann nur Technik auf /stationen. Siehe [umstellung.md](umstellung.md#kiosk-fernseher).
