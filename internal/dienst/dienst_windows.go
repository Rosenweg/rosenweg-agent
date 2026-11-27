// SPDX-License-Identifier: GPL-3.0-or-later

// Package dienst: der Agent als Windows-Dienst (unter SYSTEM, automatisch
// verzögert gestartet, bei Absturz nach einer Minute neu).
package dienst

import (
	"context"
	"errors"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/Rosenweg/rosenweg-agent/internal/logbuch"
)

const (
	Name         = "RosenwegAgent"
	Anzeigename  = "Rosenweg-Agent"
	Beschreibung = "Meldet diesen Rechner an /stationen der Rosenweg-Verwaltung und führt dort ausgelöste Befehle aus (Nachricht, Sperre, Neustart). Rosenweg/rosenweg-agent"
)

func IstDienst() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// Installieren legt den Dienst an oder passt einen vorhandenen an
// (Programmpfad, Start, Wiederherstellung). Wiederholbar.
func Installieren(exe string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	cfg := mgr.Config{
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true, // erst wenn Netz und Anmeldedienste laufen
		DisplayName:      Anzeigename,
		Description:      Beschreibung,
		ServiceStartName: "LocalSystem",
		ErrorControl:     mgr.ErrorNormal,
	}
	s, err := m.OpenService(Name)
	if err == nil {
		alt, err := s.Config()
		if err == nil {
			alt.BinaryPathName = `"` + exe + `" dienst`
			alt.StartType, alt.DelayedAutoStart = cfg.StartType, cfg.DelayedAutoStart
			alt.DisplayName, alt.Description = cfg.DisplayName, cfg.Description
			err = s.UpdateConfig(alt)
		}
		if err != nil {
			s.Close()
			return err
		}
	} else {
		s, err = m.CreateService(Name, exe, cfg, "dienst")
		if err != nil {
			return err
		}
	}
	defer s.Close()
	// Absturz: nach einer Minute neu starten, dreimal, Zähler täglich zurück.
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Minute},
	}, 86400); err != nil {
		return err
	}
	if err := eventlog.InstallAsEventCreate(logbuch.Quelle, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil &&
		!strings.Contains(err.Error(), "exists") {
		return err
	}
	return nil
}

// Starten startet den Dienst, falls er nicht läuft.
func Starten() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return err
	}
	defer s.Close()
	st, err := s.Query()
	if err == nil && st.State == svc.Running {
		return nil
	}
	return s.Start()
}

// Entfernen hält den Dienst an und löscht ihn samt Ereignisquelle.
func Entfernen() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return nil
		}
		return err
	}
	defer s.Close()
	if st, err := s.Control(svc.Stop); err == nil {
		for i := 0; i < 30 && st.State != svc.Stopped; i++ {
			time.Sleep(time.Second)
			if st, err = s.Query(); err != nil {
				break
			}
		}
	}
	if err := s.Delete(); err != nil {
		return err
	}
	_ = eventlog.Remove(logbuch.Quelle)
	return nil
}

// Laufen übergibt an den Dienststeuerungs-Manager; lauf bekommt einen
// Kontext, der beim Anhalten oder Herunterfahren endet.
func Laufen(lauf func(ctx context.Context)) error {
	return svc.Run(Name, &handler{lauf: lauf})
}

type handler struct{ lauf func(ctx context.Context) }

func (h *handler) Execute(_ []string, anfragen <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, abbruch := context.WithCancel(context.Background())
	fertig := make(chan struct{})
	go func() { h.lauf(ctx); close(fertig) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case a := <-anfragen:
			switch a.Cmd {
			case svc.Interrogate:
				status <- a.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				abbruch()
				select {
				case <-fertig:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		case <-fertig:
			abbruch()
			return false, 1
		}
	}
}
