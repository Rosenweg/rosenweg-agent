// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package plattform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alexbrainman/sspi/negotiate"

	"github.com/Rosenweg/rosenweg-agent/internal/konfig"
	"github.com/Rosenweg/rosenweg-agent/internal/logbuch"
	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// Windows: der Agent läuft als Dienst unter SYSTEM. Als SYSTEM spricht er im
// Netz als Computerkonto (WIN11-1$) — damit meldet er sich bei der API an.
type Windows struct {
	K   konfig.Konfig
	Log *logbuch.Logbuch

	mu       sync.Mutex
	langsam  langsamesInventar
	langsamZ time.Time
}

// Vorgaben für Windows: Programm und Skript unter Programme, Zustand und
// Protokoll unter ProgramData (nur SYSTEM und Administratoren, setzt
// agent-installieren.ps1).
func Vorgaben() konfig.Konfig {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	pd := os.Getenv("ProgramData")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return konfig.Konfig{
		NetzstartSkript: filepath.Join(pf, "Rosenweg", "Agent", "netzstart-einmal.ps1"),
		Daten:           filepath.Join(pd, "Rosenweg", "Agent"),
	}
}

func KonfigPfad() string {
	return filepath.Join(Vorgaben().Daten, "agent.json")
}

func Neu(k konfig.Konfig, log *logbuch.Logbuch) *Windows {
	return &Windows{K: k, Log: log}
}

func (w *Windows) Name() string { return "windows" }

// Anmelden holt über SSPI (Negotiate) ein Kerberos-Token für den Dienst der
// API und tauscht es gegen ein Stationstoken. Das Token bleibt nur im
// Speicher (siehe Website api/routes/stationen-v2.js, /anmelden).
func (w *Windows) Anmelden(ctx context.Context, c *protokoll.Client, a protokoll.Anmeldung) (string, string, error) {
	cred, err := negotiate.AcquireCurrentUserCredentials()
	if err != nil {
		return "", "", fmt.Errorf("Anmeldedaten des Computerkontos: %w", err)
	}
	defer cred.Release()
	sc, token, err := negotiate.NewClientContext(cred, w.K.SPN)
	if err != nil {
		return "", "", fmt.Errorf("Kerberos-Ticket für %s: %w", w.K.SPN, err)
	}
	defer sc.Release()
	aw, err := c.Anmelden(ctx, token, a)
	if err != nil {
		return "", "", err
	}
	return aw.StationID, aw.Token, nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return strings.ToUpper(h)
}
