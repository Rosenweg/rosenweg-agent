// SPDX-License-Identifier: GPL-3.0-or-later

// rosenweg-agent — meldet einen Rechner an /stationen der Rosenweg-Verwaltung
// (Linux-Stationen, Windows-PCs der Domäne) und führt aus, was Technik dort
// auslöst. Beschreibung: README.md, docs/architektur.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/Rosenweg/rosenweg-agent/internal/agent"
	"github.com/Rosenweg/rosenweg-agent/internal/dienst"
	"github.com/Rosenweg/rosenweg-agent/internal/konfig"
	"github.com/Rosenweg/rosenweg-agent/internal/logbuch"
	"github.com/Rosenweg/rosenweg-agent/internal/plattform"
	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// version setzt der Bau (-ldflags "-X main.version=…", siehe Makefile).
var version = "0.0.0-dev"

const hilfe = `rosenweg-agent %s — Rosenweg-Stationen-Agent

  rosenweg-agent lauf          im Vordergrund laufen (Strg+C beendet)
  rosenweg-agent probe         einmal anmelden und melden, ohne Befehle
                               und ohne Sperre (Modus "parallel"); zeigt die Antwort
  rosenweg-agent inventar      Inventar lesen und ausgeben, ohne Netz
  rosenweg-agent installieren  Windows: Dienst anlegen bzw. anpassen und starten
  rosenweg-agent entfernen     Windows: Sperre aufheben, Dienst entfernen
  rosenweg-agent version

  -konfig DATEI   Einstellungen (Vorgabe Windows: %%ProgramData%%\Rosenweg\Agent\agent.json,
                  Linux: /etc/rosenweg-agent/agent.json; ohne Datei gelten die Vorgaben)
`

func main() {
	konfigPfad := flag.String("konfig", plattform.KonfigPfad(), "Einstellungen")
	flag.Usage = func() { fmt.Fprintf(os.Stderr, hilfe, version) }
	flag.Parse()

	befehl := flag.Arg(0)
	if dienst.IstDienst() {
		befehl = "dienst"
	}
	if befehl == "" || befehl == "hilfe" {
		flag.Usage()
		os.Exit(2)
	}
	if befehl == "version" {
		fmt.Println(version)
		return
	}

	k, err := konfig.Laden(*konfigPfad, plattform.Vorgaben())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Einstellungen %s: %v\n", *konfigPfad, err)
		os.Exit(1)
	}

	// Im Dienst nur Datei und Ereignisanzeige; im Vordergrund auch stderr.
	var echo = os.Stderr
	logPfad := filepath.Join(k.Daten, "agent.log")
	if befehl == "inventar" || befehl == "version" {
		logPfad = ""
	}
	if befehl == "dienst" {
		echo = nil
	}
	log := logbuch.Neu(logPfad, echoOderNil(echo))
	defer log.Schliessen()

	p := plattform.Neu(k, log)
	a := &agent.Agent{
		P: p, Log: log, Version: version, Arch: runtime.GOARCH, Modus: k.Modus,
		Client: protokoll.NeuerClient(k.API, "rosenweg-agent/"+version+" ("+runtime.GOOS+")"),
	}

	switch befehl {
	case "dienst":
		log.Info("Dienst startet (Version %s, API %s, Modus %s)", version, k.API, k.Modus)
		if err := dienst.Laufen(a.Laufen); err != nil {
			log.Fehler("Dienst: %v", err)
			os.Exit(1)
		}
		log.Info("Dienst beendet")

	case "lauf":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		log.Info("Agent startet (Version %s, API %s, Modus %s)", version, k.API, k.Modus)
		a.Laufen(ctx)

	case "probe":
		aw, err := a.Probe(context.Background())
		if err != nil {
			fmt.Fprintln(os.Stderr, "Probe gescheitert:", err)
			os.Exit(1)
		}
		ausgeben(aw)

	case "inventar":
		inv, err := p.Inventar(context.Background(), true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Hinweis:", err)
		}
		ausgeben(inv)

	case "installieren":
		exe, err := os.Executable()
		if err == nil {
			err = dienst.Installieren(exe)
		}
		if err == nil {
			err = dienst.Starten()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Installieren:", err)
			os.Exit(1)
		}
		fmt.Println("Dienst", dienst.Name, "installiert und gestartet.")

	case "entfernen":
		// Erst die Sperre aufheben: ohne Agent hebt sie niemand mehr auf.
		var fehler []error
		if r, ok := any(p).(interface{ AufraeumenBeimEntfernen() error }); ok {
			if err := r.AufraeumenBeimEntfernen(); err != nil {
				fehler = append(fehler, fmt.Errorf("Sperre aufheben: %w", err))
			}
		}
		if err := dienst.Entfernen(); err != nil {
			fehler = append(fehler, fmt.Errorf("Dienst: %w", err))
		}
		if len(fehler) > 0 {
			fmt.Fprintln(os.Stderr, "Entfernen:", errors.Join(fehler...))
			os.Exit(1)
		}
		fmt.Println("Dienst", dienst.Name, "entfernt.")

	default:
		flag.Usage()
		os.Exit(2)
	}
}

// Ein nil *os.File in einer io.Writer-Schnittstelle wäre nicht nil.
func echoOderNil(f *os.File) interface{ Write([]byte) (int, error) } {
	if f == nil {
		return nil
	}
	return f
}

func ausgeben(v any) {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	_ = e.Encode(v)
}
