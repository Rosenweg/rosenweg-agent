// SPDX-License-Identifier: GPL-3.0-or-later

// Package protokoll beschreibt das Stationen-Protokoll v2 der Rosenweg-API
// (Website: api/routes/stationen-v2.js, api/lib/stationprotokoll.js) und
// spricht es. Die Felder heissen hier wie im JSON; wer eines ändert, ändert
// es auf beiden Seiten. Beschreibung: docs/protokoll-v2.md.
package protokoll

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Version ist die Protokollnummer, nicht die Programmversion.
const Version = 2

// Agent sagt der API, wer spricht.
type Agent struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Plattform string `json:"plattform"` // windows | linux
	Arch      string `json:"arch"`
	// Modus "parallel": läuft zur Probe neben dem alten Bash-Agenten einer
	// Linux-Station. Die API liefert dann keine Befehle, und der Agent setzt
	// keine Sperre um.
	Modus string `json:"modus"`
}

// Inventar ist für alle Plattformen gleich; was nur eine hat, steht unter
// Zusatz (flach: Text, Zahl, Wahrheitswert).
type Inventar struct {
	Hostname         string             `json:"hostname"`
	Betriebssystem   Betriebssystem     `json:"betriebssystem"`
	LaufzeitSekunden int64              `json:"laufzeit_sekunden"`
	Personen         []Person           `json:"personen"`
	Netz             []Adapter          `json:"netz"`
	Platten          []Platte           `json:"platten"`
	Verschluesselung []Verschluesselung `json:"verschluesselung"`
	Updates          Updates            `json:"updates"`
	Zusatz           map[string]any     `json:"zusatz,omitempty"`
}

type Betriebssystem struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Build   string `json:"build,omitempty"`
	Kernel  string `json:"kernel,omitempty"`
}

// Person ist eine angemeldete Sitzung. Zustand: aktiv | getrennt | andere.
type Person struct {
	Name    string `json:"name"`
	Sitzung string `json:"sitzung,omitempty"`
	Zustand string `json:"zustand"`
}

// Adapter: Art kabel | wlan | andere. "andere" sind virtuelle Adapter (VPN,
// Hyper-V, Docker) — der Anschluss-Wächter der API sieht sie nicht an.
type Adapter struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	Art       string   `json:"art"`
	Verbunden bool     `json:"verbunden"`
	Standard  bool     `json:"standard"`
	IPv4      []string `json:"ipv4"`
	IPv6      []string `json:"ipv6"`
}

type Platte struct {
	Laufwerk    string `json:"laufwerk"`
	GesamtBytes uint64 `json:"gesamt_bytes"`
	FreiBytes   uint64 `json:"frei_bytes"`
}

// Verschluesselung: BitLocker auf Windows. Schutz an | aus | unbekannt.
type Verschluesselung struct {
	Laufwerk string `json:"laufwerk"`
	Schutz   string `json:"schutz"`
	Zustand  string `json:"zustand,omitempty"`
	Prozent  *int   `json:"prozent,omitempty"`
}

type Updates struct {
	Zuletzt            string   `json:"zuletzt,omitempty"`
	NeustartAusstehend *bool    `json:"neustart_ausstehend,omitempty"`
	Liste              []Update `json:"liste,omitempty"`
	Fehler             string   `json:"fehler,omitempty"`
}

type Update struct {
	Titel string `json:"titel"`
	Datum string `json:"datum,omitempty"`
	OK    *bool  `json:"ok,omitempty"`
}

// Quittung: Ergebnis ok | fehler | abgelehnt.
type Quittung struct {
	ID       int64  `json:"id"`
	Ergebnis string `json:"ergebnis"`
	Text     string `json:"text,omitempty"`
}

// Meldung: ein Ereignis für die Verwaltung (Art klein, mit Bindestrich).
type Meldung struct {
	Art  string `json:"art"`
	Text string `json:"text,omitempty"`
}

type Lebenszeichen struct {
	Protokoll  int        `json:"protokoll"`
	Agent      Agent      `json:"agent"`
	Inventar   *Inventar  `json:"inventar"`
	Quittungen []Quittung `json:"quittungen,omitempty"`
	Meldungen  []Meldung  `json:"meldungen,omitempty"`
}

type Befehl struct {
	ID         int64           `json:"id"`
	Art        string          `json:"art"`
	Parameter  json.RawMessage `json:"parameter"`
	GueltigBis time.Time       `json:"gueltig_bis"`
}

type Antwort struct {
	OK                bool     `json:"ok"`
	Protokoll         int      `json:"protokoll"`
	StationID         string   `json:"station_id"`
	IntervallSekunden int      `json:"intervall_sekunden"`
	Gesperrt          bool     `json:"gesperrt"`
	SperrGrund        string   `json:"sperr_grund"`
	Modus             string   `json:"modus"`
	Befehle           []Befehl `json:"befehle"`
}

type Anmeldung struct {
	Protokoll int       `json:"protokoll"`
	Agent     Agent     `json:"agent"`
	Inventar  *Inventar `json:"inventar,omitempty"`
}

type AnmeldeAntwort struct {
	Protokoll         int    `json:"protokoll"`
	StationID         string `json:"station_id"`
	Token             string `json:"token"`
	IntervallSekunden int    `json:"intervall_sekunden"`
}

// Fehler ist eine Antwort der API mit Status ausserhalb 2xx.
type Fehler struct {
	Status int
	Text   string
}

func (f *Fehler) Error() string { return fmt.Sprintf("API %d: %s", f.Status, f.Text) }

// Endgueltig: die API hat entschieden, nicht das Netz. 403/404/409 bei der
// Anmeldung heissen „dieser Rechner wird hier nicht (mehr) geführt".
func (f *Fehler) Endgueltig() bool { return f.Status == 403 || f.Status == 404 || f.Status == 409 }

// Client spricht mit der API. Basis ist z. B. https://www.rosenweg4303.ch/api.
type Client struct {
	Basis string
	HTTP  *http.Client
	Agent string // User-Agent
}

func NeuerClient(basis, agent string) *Client {
	return &Client{
		Basis: strings.TrimRight(basis, "/"),
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		Agent: agent,
	}
}

// Anmelden schickt ein SPNEGO-Token (roh, nicht Base64) und bekommt ein Token.
func (c *Client) Anmelden(ctx context.Context, spnego []byte, a Anmeldung) (AnmeldeAntwort, error) {
	var aw AnmeldeAntwort
	err := c.post(ctx, "/stations/v2/anmelden", "Negotiate "+base64.StdEncoding.EncodeToString(spnego), a, &aw)
	if err == nil && (aw.Token == "" || aw.StationID == "") {
		err = fmt.Errorf("Anmeldung ohne Token oder Kennung")
	}
	return aw, err
}

func (c *Client) Lebenszeichen(ctx context.Context, token string, lz Lebenszeichen) (Antwort, error) {
	var aw Antwort
	err := c.post(ctx, "/stations/v2/lebenszeichen", "Bearer "+token, lz, &aw)
	if err == nil && !aw.OK {
		err = fmt.Errorf("Antwort ohne ok")
	}
	return aw, err
}

func (c *Client) post(ctx context.Context, pfad, auth string, rumpf, ziel any) error {
	b, err := json.Marshal(rumpf)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Basis+pfad, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	req.Header.Set("User-Agent", c.Agent)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	daten, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		var e struct {
			Error string `json:"error"`
			Grund string `json:"grund"`
		}
		_ = json.Unmarshal(daten, &e)
		text := e.Error
		if e.Grund != "" {
			text += " (" + e.Grund + ")"
		}
		if text == "" {
			text = strings.TrimSpace(string(daten))
			if len(text) > 200 {
				text = text[:200]
			}
		}
		return &Fehler{Status: res.StatusCode, Text: text}
	}
	return json.Unmarshal(daten, ziel)
}
