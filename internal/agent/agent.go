// SPDX-License-Identifier: GPL-3.0-or-later

// Package agent ist die Schleife, die auf jeder Plattform gleich läuft:
// anmelden, Inventar lesen, Lebenszeichen schicken, Sperre umsetzen,
// Befehle ausführen und quittieren, warten. Was plattformeigen ist, steckt
// hinter Plattform.
//
// Grundsätze (wie station-agent.sh in os-stationen):
//   - Kein offener Port. Alles, was die Verwaltung will, kommt als Antwort
//     auf das Lebenszeichen.
//   - Fällt das Netz aus, bleibt alles, wie es ist — insbesondere eine Sperre.
//     Aufgehoben wird sie nur durch eine echte Antwort der API.
//   - Ein Befehl wird höchstens einmal ausgeführt und immer quittiert.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Rosenweg/rosenweg-agent/internal/logbuch"
	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// Plattform ist das, was Windows und Linux verschieden machen.
type Plattform interface {
	// Name ist die Plattform im Protokoll: windows | linux.
	Name() string
	// Inventar liest den Zustand. voll: auch das Langsame (BitLocker,
	// Updates), sonst darf ein zwischengespeicherter Stand zurückkommen.
	Inventar(ctx context.Context, voll bool) (*protokoll.Inventar, error)
	// Anmelden liefert Kennung und Token. Windows: per Kerberos bei der API.
	// Linux: aus den Dateien von os-stationen.
	Anmelden(ctx context.Context, c *protokoll.Client, a protokoll.Anmeldung) (station, token string, err error)
	// Sperre setzt den Zustand (idempotent; wird bei jedem Lebenszeichen
	// gerufen, solange gesperrt ist).
	Sperre(ctx context.Context, an bool, grund string) error
	// SperreGespeichert: war beim letzten Lauf gesperrt? Gilt sofort beim
	// Start, noch bevor die API erreichbar ist.
	SperreGespeichert() (bool, string)
	// Ausfuehren startet einen Befehl. Länger dauernde (Neustart in 5 min)
	// werden angestossen, nicht abgewartet; zurück kommt, was geplant ist.
	Ausfuehren(ctx context.Context, art string, parameter json.RawMessage) (ergebnis, text string)
}

type Agent struct {
	P       Plattform
	Client  *protokoll.Client
	Log     *logbuch.Logbuch
	Version string
	Arch    string
	Modus   string // voll | parallel
	Jetzt   func() time.Time

	station     string
	token       string
	gesperrt    bool
	grund       string
	quittungen  []protokoll.Quittung
	meldungen   []protokoll.Meldung
	vollZuletzt time.Time
	vollJetzt   bool
	// Das zuletzt mit 401 abgelehnte Token. Linux liest sein Token aus einer
	// Datei; bekäme es nach der Ablehnung dasselbe wieder, klopfte es sonst
	// jede Sekunde an.
	abgelehnt string
}

const (
	intervallVorgabe = 120 * time.Second
	intervallMin     = 30 * time.Second
	intervallMax     = 900 * time.Second
	vollAlle         = 30 * time.Minute
	// Nach einer endgültigen Ablehnung (entfernt, unbekannt) nicht im
	// Minutentakt anklopfen.
	ablehnungPause = time.Hour
)

func (a *Agent) jetzt() time.Time {
	if a.Jetzt != nil {
		return a.Jetzt()
	}
	return time.Now()
}

func (a *Agent) info() protokoll.Agent {
	return protokoll.Agent{Name: "rosenweg-agent", Version: a.Version, Plattform: a.P.Name(), Arch: a.Arch, Modus: a.Modus}
}

// Starten setzt eine gespeicherte Sperre sofort wieder um — wie
// station-sperre nach dem Neustart über das Lebenszeichen, nur ohne auf das
// Netz zu warten: ein PC, der ohne Kabel startet, soll nicht entsperrt sein.
func (a *Agent) Starten(ctx context.Context) {
	if a.Modus == "parallel" {
		return
	}
	if an, grund := a.P.SperreGespeichert(); an {
		a.gesperrt, a.grund = true, grund
		if err := a.P.Sperre(ctx, true, grund); err != nil {
			a.Log.Fehler("Gespeicherte Sperre nicht wiederhergestellt: %v", err)
		} else {
			a.Log.Info("Gespeicherte Sperre wiederhergestellt%s", mitGrund(grund))
		}
	}
}

// Laufen ist die Schleife bis ctx endet.
func (a *Agent) Laufen(ctx context.Context) {
	a.Starten(ctx)
	for {
		warte := a.Runde(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(warte):
		}
	}
}

// Runde ist ein Durchgang. Zurück: wie lange bis zum nächsten.
func (a *Agent) Runde(ctx context.Context) time.Duration {
	if a.token == "" {
		if d, ok := a.anmelden(ctx); !ok {
			return d
		}
	}
	voll := a.vollJetzt || a.jetzt().Sub(a.vollZuletzt) >= vollAlle
	beginn := a.jetzt()
	inv, err := a.P.Inventar(ctx, voll)
	if err != nil {
		a.Log.Warnung("Inventar unvollständig: %v", err)
	}
	// Die Laufzeit liest die Plattform zu Beginn; das langsame Inventar
	// (PowerShell: BitLocker, Updates) kann danach Minuten brauchen. Die API
	// rechnet beim Empfang Startzeit = jetzt - Laufzeit; ohne Nachtrag sähe
	// das wie ein zweites Gerät mit späterer Startzeit aus („doppelt
	// gemeldet", win11-1 am 27.11.2026: 4 min 40 s).
	if inv != nil && inv.LaufzeitSekunden > 0 {
		if d := a.jetzt().Sub(beginn); d > 0 {
			inv.LaufzeitSekunden += int64(d / time.Second)
		}
	}
	if voll {
		a.vollZuletzt, a.vollJetzt = a.jetzt(), false
	}
	lz := protokoll.Lebenszeichen{Protokoll: protokoll.Version, Agent: a.info(), Inventar: inv,
		Quittungen: a.quittungen, Meldungen: a.meldungen}
	aw, err := a.Client.Lebenszeichen(ctx, a.token, lz)
	var f *protokoll.Fehler
	if errors.As(err, &f) && f.Status == 401 {
		// Token wertlos (Dienst der API neu angemeldet, Station entfernt).
		// Sofort neu anmelden; die Quittungen bleiben und gehen mit dem
		// nächsten Lebenszeichen.
		a.Log.Warnung("Token abgelehnt — melde neu an")
		a.abgelehnt, a.token = a.token, ""
		return time.Second
	}
	if err != nil {
		a.Log.Warnung("Lebenszeichen gescheitert: %v", err)
		return intervallVorgabe
	}
	a.quittungen, a.meldungen = nil, nil
	if aw.StationID != "" {
		a.station = aw.StationID
	}

	if a.Modus != "parallel" {
		a.sperreUmsetzen(ctx, aw.Gesperrt, aw.SperrGrund)
		for _, b := range aw.Befehle {
			a.befehl(ctx, b)
		}
	}
	// Quittungen sofort loswerden, damit /stationen nicht zwei Minuten lang
	// „abgeholt" zeigt, und damit sie vor einem Neustart ankommen.
	if len(a.quittungen) > 0 || len(a.meldungen) > 0 {
		return 2 * time.Second
	}
	return klemmen(time.Duration(aw.IntervallSekunden) * time.Second)
}

func (a *Agent) anmelden(ctx context.Context) (time.Duration, bool) {
	inv, _ := a.P.Inventar(ctx, false)
	station, token, err := a.P.Anmelden(ctx, a.Client, protokoll.Anmeldung{Protokoll: protokoll.Version, Agent: a.info(), Inventar: inv})
	if err != nil {
		var f *protokoll.Fehler
		if errors.As(err, &f) && f.Endgueltig() {
			a.Log.Fehler("Anmeldung abgewiesen: %v", err)
			// Wird der Rechner hier nicht (mehr) geführt, soll er auch nicht
			// gesperrt bleiben: niemand könnte die Sperre je aufheben.
			if a.gesperrt && a.Modus != "parallel" {
				a.sperreUmsetzen(ctx, false, "")
			}
			return ablehnungPause, false
		}
		a.Log.Warnung("Anmeldung gescheitert: %v", err)
		return time.Minute, false
	}
	if token == a.abgelehnt {
		a.Log.Fehler("Das Token wurde eben abgelehnt und ist unverändert — Station auf /stationen entfernt?")
		return ablehnungPause, false
	}
	if station != a.station {
		a.Log.Info("Angemeldet als %s", station)
	}
	a.station, a.token = station, token
	return 0, true
}

func (a *Agent) sperreUmsetzen(ctx context.Context, an bool, grund string) {
	wechsel := an != a.gesperrt || (an && grund != a.grund)
	// Solange gesperrt: jedes Mal durchsetzen (eine Gruppenrichtlinie kann
	// das Anmelderecht zwischendurch zurückgeschrieben haben). Entsperrt:
	// nur beim Wechsel.
	if !an && !wechsel {
		return
	}
	if err := a.P.Sperre(ctx, an, grund); err != nil {
		a.Log.Fehler("Sperre %s: %v", anAus(an), err)
		if wechsel {
			a.meldungen = append(a.meldungen, protokoll.Meldung{Art: "sperre-fehler", Text: err.Error()})
		}
		return
	}
	if wechsel {
		a.Log.Info("Sperre %s%s", anAus(an), mitGrund(grund))
		a.meldungen = append(a.meldungen, protokoll.Meldung{Art: "sperre-" + anAus(an), Text: grund})
	}
	a.gesperrt, a.grund = an, grund
}

func (a *Agent) befehl(ctx context.Context, b protokoll.Befehl) {
	q := protokoll.Quittung{ID: b.ID}
	if !b.GueltigBis.IsZero() && a.jetzt().After(b.GueltigBis) {
		q.Ergebnis, q.Text = "abgelehnt", "verfallen, bevor er ankam"
	} else if b.Art == "inventar" {
		a.vollJetzt = true
		q.Ergebnis, q.Text = "ok", "wird mit dem nächsten Lebenszeichen geschickt"
	} else {
		q.Ergebnis, q.Text = a.P.Ausfuehren(ctx, b.Art, b.Parameter)
	}
	a.Log.Info("Befehl %d %s: %s %s", b.ID, b.Art, q.Ergebnis, q.Text)
	a.quittungen = append(a.quittungen, q)
}

// Probe: einmal anmelden und ein Lebenszeichen im Modus "parallel" — es
// holt keine Befehle ab und ändert keine Sperre. Zum Prüfen von Hand.
func (a *Agent) Probe(ctx context.Context) (protokoll.Antwort, error) {
	a.Modus = "parallel"
	if a.token == "" {
		inv, _ := a.P.Inventar(ctx, false)
		s, t, err := a.P.Anmelden(ctx, a.Client, protokoll.Anmeldung{Protokoll: protokoll.Version, Agent: a.info(), Inventar: inv})
		if err != nil {
			return protokoll.Antwort{}, fmt.Errorf("Anmeldung: %w", err)
		}
		a.station, a.token = s, t
	}
	inv, err := a.P.Inventar(ctx, true)
	if err != nil {
		a.Log.Warnung("Inventar unvollständig: %v", err)
	}
	return a.Client.Lebenszeichen(ctx, a.token, protokoll.Lebenszeichen{Protokoll: protokoll.Version, Agent: a.info(), Inventar: inv})
}

func klemmen(d time.Duration) time.Duration {
	switch {
	case d <= 0:
		return intervallVorgabe
	case d < intervallMin:
		return intervallMin
	case d > intervallMax:
		return intervallMax
	}
	return d
}

func anAus(an bool) string {
	if an {
		return "an"
	}
	return "aus"
}

func mitGrund(g string) string {
	if g == "" {
		return ""
	}
	return " (" + g + ")"
}
