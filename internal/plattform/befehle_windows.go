// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package plattform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Befehle auf Windows. Jeder warnt zuerst, wer davorsitzt; Neustart und
// Netzstart überlassen die Wartezeit Windows selbst (shutdown /t), damit sie
// auch dann ablaufen, wenn der Agent inzwischen neu startet.
func (w *Windows) Ausfuehren(ctx context.Context, art string, roh json.RawMessage) (string, string) {
	switch art {
	case "nachricht":
		var p nachrichtParameter
		if err := lesen(roh, &p); err != nil || strings.TrimSpace(p.Text) == "" {
			return "abgelehnt", "kein Text"
		}
		n, err := anAlleSitzungen(p.Text, 0)
		if err != nil {
			return "fehler", err.Error()
		}
		if n == 0 {
			return "abgelehnt", "niemand angemeldet"
		}
		return "ok", fmt.Sprintf("angezeigt in %d Sitzung(en)", n)

	case "abmelden":
		var p verzoegerungParameter
		if err := lesen(roh, &p); err != nil {
			return "abgelehnt", err.Error()
		}
		vor := grenzen(p.VorwarnungSekunden, 30, 900, 60)
		s, err := sitzungen()
		if err != nil {
			return "fehler", err.Error()
		}
		if len(s) == 0 {
			return "abgelehnt", "niemand angemeldet"
		}
		text := "Du wirst in " + dauerText(vor) + " abgemeldet. Bitte jetzt speichern."
		if p.Text != "" {
			text = p.Text + "\n\n" + text
		}
		for _, x := range s {
			_ = nachrichtAnSitzung(x.ID, Absender, text, uint32(vor))
		}
		go func(liste []sitzung) {
			time.Sleep(time.Duration(vor) * time.Second)
			for _, x := range liste {
				if err := sitzungAbmelden(x.ID); err != nil {
					w.Log.Warnung("Sitzung %d (%s) nicht abgemeldet: %v", x.ID, x.Name, err)
				} else {
					w.Log.Info("Sitzung %d (%s) abgemeldet", x.ID, x.Name)
				}
			}
		}(s)
		return "ok", fmt.Sprintf("%d Sitzung(en) werden in %s abgemeldet", len(s), dauerText(vor))

	case "neustart":
		var p verzoegerungParameter
		if err := lesen(roh, &p); err != nil {
			return "abgelehnt", err.Error()
		}
		verz := grenzen(p.VerzoegerungSekunden, 60, 3600, 300)
		if err := neustartPlanen(verz, p.Text, "Neustart durch die Technik Rosenweg"); err != nil {
			return "fehler", err.Error()
		}
		return "ok", "Neustart in " + dauerText(verz)

	case "netzstart_einmal":
		var p verzoegerungParameter
		if err := lesen(roh, &p); err != nil {
			return "abgelehnt", err.Error()
		}
		verz := grenzen(p.VerzoegerungSekunden, 60, 3600, 120)
		aus, err := w.netzstartSetzen(ctx)
		if err != nil {
			return "fehler", err.Error()
		}
		if err := neustartPlanen(verz, p.Text, "Neustart ins Sicherungs-System (einmal vom Netz)"); err != nil {
			// Der einmalige Netzstart ist gesetzt und verfällt erst mit dem
			// nächsten Start — ohne Neustart greift er beim nächsten von Hand.
			return "fehler", "Netzstart gesetzt, aber Neustart nicht geplant: " + err.Error()
		}
		return "ok", "Neustart vom Netz in " + dauerText(verz) + ". " + aus
	}
	return "abgelehnt", "unbekannter Befehl " + art
}

func anAlleSitzungen(text string, sekunden uint32) (int, error) {
	s, err := sitzungen()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, x := range s {
		if x.Zustand != windows.WTSActive {
			continue
		}
		if nachrichtAnSitzung(x.ID, Absender, text, sekunden) == nil {
			n++
		}
	}
	return n, nil
}

// neustartPlanen: shutdown.exe zeigt selbst die Warnung mit Countdown und
// lässt sich mit "shutdown /a" abbrechen — vor Ort und von der Technik.
// /d p:4:1 = geplant, Anwendung: Wartung.
func neustartPlanen(sekunden int, text, vorgabe string) error {
	if text == "" {
		text = vorgabe
	}
	text = Absender + ": " + text + " Bitte speichern."
	if len(text) > 500 {
		text = text[:500]
	}
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	cmd := exec.Command(sys+`\shutdown.exe`, "/r", "/t", strconv.Itoa(sekunden), "/c", text, "/d", "p:4:1")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if aus, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("shutdown: %v %s", err, strings.TrimSpace(string(aus)))
	}
	return nil
}

// netzstartSetzen ruft netzstart-einmal.ps1 aus os-stationen auf — dasselbe
// Skript, das man von Hand per RDP startet (sicherung/windows/). Es prüft
// Secure Boot, sucht den PXE-Eintrag und setzt ihn mit -Setzen als
// einmaliges nächstes Startziel; neu gestartet wird hier, mit Vorwarnung,
// nicht im Skript (-Neustart startet sofort).
func (w *Windows) netzstartSetzen(ctx context.Context) (string, error) {
	if _, err := os.Stat(w.K.NetzstartSkript); err != nil {
		return "", fmt.Errorf("netzstart-einmal.ps1 fehlt (%s) — kommt mit der Gruppenrichtlinie", w.K.NetzstartSkript)
	}
	ctx, abbruch := context.WithTimeout(ctx, 2*time.Minute)
	defer abbruch()
	cmd := exec.CommandContext(ctx, powershellPfad(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", w.K.NetzstartSkript, "-Setzen")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	aus, err := cmd.CombinedOutput()
	letzte := kurz(string(aus), 400)
	if err != nil {
		return "", fmt.Errorf("netzstart-einmal.ps1: %v — %s", err, letzte)
	}
	for _, z := range strings.Split(string(aus), "\n") {
		if strings.HasPrefix(strings.TrimSpace(z), "Gewaehlt:") {
			return strings.TrimSpace(z), nil
		}
	}
	return letzte, nil
}
