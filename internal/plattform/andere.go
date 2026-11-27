// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows && !linux

package plattform

// Nur damit sich das Programm auch auf dem Entwicklungsrechner bauen und
// prüfen lässt. Im Haus gibt es keine Macs (Stefan, 27.11.2026) — darum
// kein Inventar, keine Anmeldung, keine Befehle.

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/Rosenweg/rosenweg-agent/internal/konfig"
	"github.com/Rosenweg/rosenweg-agent/internal/logbuch"
	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

type Andere struct{}

func Vorgaben() konfig.Konfig { return konfig.Konfig{Daten: os.TempDir()} }
func KonfigPfad() string      { return "agent.json" }

func Neu(k konfig.Konfig, log *logbuch.Logbuch) *Andere { return &Andere{} }

func (Andere) Name() string { return "andere" }
func (Andere) Inventar(ctx context.Context, voll bool) (*protokoll.Inventar, error) {
	h, _ := os.Hostname()
	return &protokoll.Inventar{Hostname: h}, nil
}
func (Andere) Anmelden(context.Context, *protokoll.Client, protokoll.Anmeldung) (string, string, error) {
	return "", "", errors.New("auf dieser Plattform nicht vorgesehen")
}
func (Andere) Sperre(context.Context, bool, string) error { return errors.New("nicht vorgesehen") }
func (Andere) SperreGespeichert() (bool, string)          { return false, "" }
func (Andere) Ausfuehren(context.Context, string, json.RawMessage) (string, string) {
	return "abgelehnt", "nicht vorgesehen"
}
