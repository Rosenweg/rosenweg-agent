// SPDX-License-Identifier: GPL-3.0-or-later

package agent

// Die Schleife gegen eine Attrappe der API (httptest) und eine Attrappe der
// Plattform — ohne Windows, ohne Netz ins Haus.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rosenweg/rosenweg-agent/internal/logbuch"
	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

type attrappe struct {
	mu          sync.Mutex
	sperren     []string // "an:grund" / "aus"
	befehle     []string
	anmeldungen int
	token       func(n int) string
	gespeichert bool
}

func (p *attrappe) Name() string { return "windows" }
func (p *attrappe) Inventar(context.Context, bool) (*protokoll.Inventar, error) {
	return &protokoll.Inventar{Hostname: "WIN11-1", LaufzeitSekunden: 60}, nil
}
func (p *attrappe) Anmelden(ctx context.Context, c *protokoll.Client, a protokoll.Anmeldung) (string, string, error) {
	p.mu.Lock()
	p.anmeldungen++
	n := p.anmeldungen
	p.mu.Unlock()
	return "win11-1", p.token(n), nil
}
func (p *attrappe) Sperre(_ context.Context, an bool, grund string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if an {
		p.sperren = append(p.sperren, "an:"+grund)
	} else {
		p.sperren = append(p.sperren, "aus")
	}
	return nil
}
func (p *attrappe) SperreGespeichert() (bool, string) { return p.gespeichert, "alt" }
func (p *attrappe) Ausfuehren(_ context.Context, art string, roh json.RawMessage) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.befehle = append(p.befehle, art+string(roh))
	return "ok", "erledigt"
}

// api ist die Attrappe der Rosenweg-API: sie antwortet der Reihe nach mit
// den vorbereiteten Antworten und merkt sich, was kam.
type api struct {
	t         *testing.T
	mu        sync.Mutex
	antworten []func(w http.ResponseWriter, lz protokoll.Lebenszeichen, auth string)
	erhalten  []protokoll.Lebenszeichen
	auth      []string
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/stations/v2/lebenszeichen" || r.Method != http.MethodPost {
		http.Error(w, "falscher Pfad "+r.URL.Path, 404)
		return
	}
	b, _ := io.ReadAll(r.Body)
	var lz protokoll.Lebenszeichen
	if err := json.Unmarshal(b, &lz); err != nil {
		a.t.Errorf("kein JSON: %v", err)
	}
	a.mu.Lock()
	a.erhalten = append(a.erhalten, lz)
	a.auth = append(a.auth, r.Header.Get("Authorization"))
	var f func(http.ResponseWriter, protokoll.Lebenszeichen, string)
	if len(a.antworten) > 0 {
		f, a.antworten = a.antworten[0], a.antworten[1:]
	}
	a.mu.Unlock()
	if f == nil {
		ok(w, protokoll.Antwort{})
		return
	}
	f(w, lz, r.Header.Get("Authorization"))
}

func ok(w http.ResponseWriter, aw protokoll.Antwort) {
	aw.OK, aw.Protokoll, aw.StationID = true, 2, "win11-1"
	if aw.IntervallSekunden == 0 {
		aw.IntervallSekunden = 120
	}
	_ = json.NewEncoder(w).Encode(aw)
}

func neu(t *testing.T, a *api, p *attrappe, modus string) *Agent {
	t.Helper()
	srv := httptest.NewServer(a)
	t.Cleanup(srv.Close)
	return &Agent{P: p, Client: protokoll.NeuerClient(srv.URL+"/api", "test"), Log: logbuch.Neu("", nil),
		Version: "0.1.0", Arch: "amd64", Modus: modus}
}

func tokenFest(n int) string { return "rw_station_eins" }

func TestSperreUndBefehle(t *testing.T) {
	a := &api{t: t}
	a.antworten = append(a.antworten,
		func(w http.ResponseWriter, lz protokoll.Lebenszeichen, _ string) {
			if lz.Protokoll != 2 || lz.Agent.Plattform != "windows" || lz.Inventar.Hostname != "WIN11-1" {
				t.Errorf("Lebenszeichen falsch: %+v", lz)
			}
			ok(w, protokoll.Antwort{Gesperrt: true, SperrGrund: "Anschluss gewechselt", Befehle: []protokoll.Befehl{
				{ID: 7, Art: "nachricht", Parameter: json.RawMessage(`{"text":"Hallo"}`), GueltigBis: time.Now().Add(time.Hour)},
				{ID: 8, Art: "neustart", Parameter: json.RawMessage(`{}`), GueltigBis: time.Now().Add(-time.Minute)},
			}})
		},
		func(w http.ResponseWriter, lz protokoll.Lebenszeichen, _ string) {
			if len(lz.Quittungen) != 2 || lz.Quittungen[0].ID != 7 || lz.Quittungen[0].Ergebnis != "ok" ||
				lz.Quittungen[1].ID != 8 || lz.Quittungen[1].Ergebnis != "abgelehnt" {
				t.Errorf("Quittungen falsch: %+v", lz.Quittungen)
			}
			if len(lz.Meldungen) != 1 || lz.Meldungen[0].Art != "sperre-an" {
				t.Errorf("Meldung fehlt: %+v", lz.Meldungen)
			}
			ok(w, protokoll.Antwort{Gesperrt: true, SperrGrund: "Anschluss gewechselt"})
		},
		func(w http.ResponseWriter, lz protokoll.Lebenszeichen, _ string) {
			if len(lz.Quittungen) != 0 {
				t.Errorf("Quittungen doppelt geschickt: %+v", lz.Quittungen)
			}
			ok(w, protokoll.Antwort{Gesperrt: false})
		},
	)
	p := &attrappe{token: tokenFest}
	ag := neu(t, a, p, "voll")
	ctx := context.Background()

	if d := ag.Runde(ctx); d > 5*time.Second {
		t.Fatalf("nach Befehlen sofort wieder melden, nicht nach %v", d)
	}
	if d := ag.Runde(ctx); d != 120*time.Second {
		t.Fatalf("Intervall aus der Antwort erwartet, bekam %v", d)
	}
	ag.Runde(ctx)

	if strings.Join(p.befehle, "|") != `nachricht{"text":"Hallo"}` {
		t.Fatalf("ausgeführt: %v (der verfallene Neustart darf nicht laufen)", p.befehle)
	}
	// Gesperrt: beim ersten Mal und beim zweiten Lebenszeichen erneut
	// durchgesetzt, beim dritten aufgehoben.
	if strings.Join(p.sperren, "|") != "an:Anschluss gewechselt|an:Anschluss gewechselt|aus" {
		t.Fatalf("Sperre: %v", p.sperren)
	}
	if a.auth[0] != "Bearer rw_station_eins" {
		t.Fatalf("Authorization: %q", a.auth[0])
	}
}

func TestNetzausfallHebtSperreNichtAuf(t *testing.T) {
	p := &attrappe{token: tokenFest, gespeichert: true}
	ag := &Agent{P: p, Client: protokoll.NeuerClient("http://127.0.0.1:1/api", "test"), Log: logbuch.Neu("", nil), Modus: "voll"}
	ag.Starten(context.Background())
	ag.Runde(context.Background())
	if strings.Join(p.sperren, "|") != "an:alt" {
		t.Fatalf("nur die gespeicherte Sperre, nichts aufgehoben: %v", p.sperren)
	}
}

func TestTokenAbgelehntNeuAnmelden(t *testing.T) {
	a := &api{t: t}
	a.antworten = append(a.antworten,
		func(w http.ResponseWriter, _ protokoll.Lebenszeichen, _ string) {
			w.WriteHeader(401)
			_, _ = io.WriteString(w, `{"error":"Stationstoken ungültig"}`)
		},
		func(w http.ResponseWriter, _ protokoll.Lebenszeichen, auth string) {
			if auth != "Bearer rw_station_2" {
				t.Errorf("neues Token erwartet, bekam %q", auth)
			}
			ok(w, protokoll.Antwort{})
		},
	)
	p := &attrappe{token: func(n int) string { return "rw_station_" + string(rune('0'+n)) }}
	ag := neu(t, a, p, "voll")
	if d := ag.Runde(context.Background()); d > 2*time.Second {
		t.Fatalf("nach 401 gleich neu anmelden, nicht nach %v", d)
	}
	ag.Runde(context.Background())
	if p.anmeldungen != 2 {
		t.Fatalf("zwei Anmeldungen erwartet, %d", p.anmeldungen)
	}
}

func TestGleichesTokenNachAblehnungWartet(t *testing.T) {
	a := &api{t: t}
	a.antworten = append(a.antworten, func(w http.ResponseWriter, _ protokoll.Lebenszeichen, _ string) {
		w.WriteHeader(401)
	})
	p := &attrappe{token: tokenFest} // wie Linux: Token aus der Datei
	ag := neu(t, a, p, "voll")
	ag.Runde(context.Background())
	if d := ag.Runde(context.Background()); d != ablehnungPause {
		t.Fatalf("gleiches Token: Pause erwartet, bekam %v", d)
	}
	if len(a.erhalten) != 1 {
		t.Fatalf("das abgelehnte Token darf nicht erneut gesendet werden (%d)", len(a.erhalten))
	}
}

func TestParallelFuehrtNichtsAus(t *testing.T) {
	a := &api{t: t}
	a.antworten = append(a.antworten, func(w http.ResponseWriter, lz protokoll.Lebenszeichen, _ string) {
		if lz.Agent.Modus != "parallel" {
			t.Errorf("Modus: %q", lz.Agent.Modus)
		}
		ok(w, protokoll.Antwort{Gesperrt: true, Befehle: []protokoll.Befehl{{ID: 1, Art: "neustart", GueltigBis: time.Now().Add(time.Hour)}}})
	})
	p := &attrappe{token: tokenFest, gespeichert: true}
	ag := neu(t, a, p, "parallel")
	ag.Starten(context.Background())
	ag.Runde(context.Background())
	if len(p.befehle) != 0 || len(p.sperren) != 0 {
		t.Fatalf("parallel: nichts ausführen, nichts sperren (%v, %v)", p.befehle, p.sperren)
	}
}

func TestIntervallGrenzen(t *testing.T) {
	for _, f := range []struct{ ein, aus time.Duration }{
		{0, intervallVorgabe}, {5 * time.Second, intervallMin}, {time.Hour, intervallMax}, {60 * time.Second, 60 * time.Second},
	} {
		if g := klemmen(f.ein); g != f.aus {
			t.Errorf("klemmen(%v) = %v, erwartet %v", f.ein, g, f.aus)
		}
	}
}

// langsam braucht für das Inventar (laut Uhr des Agenten) 280 s, wie das
// PowerShell-Inventar unter Last auf win11-1.
type langsam struct {
	attrappe
	uhr *time.Time
}

func (p *langsam) Inventar(context.Context, bool) (*protokoll.Inventar, error) {
	inv := &protokoll.Inventar{Hostname: "WIN11-1", LaufzeitSekunden: 60}
	*p.uhr = p.uhr.Add(280 * time.Second)
	return inv, nil
}

// Die Laufzeit gilt beim Senden, nicht beim Beginn des Inventars — sonst
// errechnet die API eine spätere Startzeit und meldet die Station doppelt.
func TestLaufzeitBeimSenden(t *testing.T) {
	a := &api{t: t}
	uhr := time.Date(2026, 11, 27, 13, 22, 0, 0, time.UTC)
	p := &langsam{attrappe: attrappe{token: tokenFest}, uhr: &uhr}
	ag := neu(t, a, &p.attrappe, "dienst")
	ag.P = p
	ag.Jetzt = func() time.Time { return uhr }
	ag.Runde(context.Background())
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.erhalten) == 0 {
		t.Fatal("kein Lebenszeichen")
	}
	if got := a.erhalten[len(a.erhalten)-1].Inventar.LaufzeitSekunden; got != 340 {
		t.Errorf("Laufzeit %d, erwartet 340", got)
	}
}
