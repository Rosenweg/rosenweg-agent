// SPDX-License-Identifier: GPL-3.0-or-later

// Package konfig liest die wenigen Einstellungen des Agenten. Alles hat eine
// Vorgabe, die im Haus passt; die Datei braucht es nur für Abweichungen
// (Testsystem, anderer Dienstname). Geheimnisse stehen hier nie.
package konfig

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"
)

type Konfig struct {
	// API, wie /api/stations davor steht.
	API string `json:"api"`
	// Dienstname für Kerberos (Windows). Muss im AD auf das Dienstkonto der
	// API zeigen (Website: ad-dc/stationen-agent/kerberos-dienstkonto.sh).
	SPN string `json:"spn"`
	// "voll" (Vorgabe) oder "parallel" (nur zusehen, neben station-agent.sh).
	Modus string `json:"modus"`
	// Windows: Netzstart-Skript aus os-stationen (sicherung/windows).
	NetzstartSkript string `json:"netzstart_skript"`
	// Linux: wo os-stationen Kennung, Token und API ablegt.
	StationDatei string `json:"station_datei"`
	TokenDatei   string `json:"token_datei"`
	APIDatei     string `json:"api_datei"`
	// Verzeichnis für Zustand (Sperre) und Protokolldatei.
	Daten string `json:"daten"`
}

const VorgabeAPI = "https://www.rosenweg4303.ch/api"
const VorgabeSPN = "HTTP/www.rosenweg4303.ch"

// Laden liest pfad, falls es die Datei gibt, und füllt den Rest mit vorgaben.
func Laden(pfad string, vorgaben Konfig) (Konfig, error) {
	k := vorgaben
	b, err := os.ReadFile(pfad)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return k, err
	default:
		var datei Konfig
		if err := json.Unmarshal(b, &datei); err != nil {
			return k, err
		}
		k = zusammen(k, datei)
	}
	if k.API == "" {
		k.API = VorgabeAPI
	}
	if k.SPN == "" {
		k.SPN = VorgabeSPN
	}
	if k.Modus != "parallel" {
		k.Modus = "voll"
	}
	k.API = strings.TrimRight(k.API, "/")
	return k, nil
}

func zusammen(a, b Konfig) Konfig {
	nimm := func(x *string, y string) {
		if y != "" {
			*x = y
		}
	}
	nimm(&a.API, b.API)
	nimm(&a.SPN, b.SPN)
	nimm(&a.Modus, b.Modus)
	nimm(&a.NetzstartSkript, b.NetzstartSkript)
	nimm(&a.StationDatei, b.StationDatei)
	nimm(&a.TokenDatei, b.TokenDatei)
	nimm(&a.APIDatei, b.APIDatei)
	nimm(&a.Daten, b.Daten)
	return a
}
