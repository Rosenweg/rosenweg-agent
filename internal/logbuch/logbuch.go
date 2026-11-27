// SPDX-License-Identifier: GPL-3.0-or-later

// Package logbuch schreibt, was der Agent tut: in eine Datei (mit einer
// Vorgängerdatei, je höchstens 1 MB) und auf Windows zusätzlich in die
// Ereignisanzeige (Anwendung, Quelle "Rosenweg-Agent"). Auf Linux geht es
// nach stderr — dort nimmt es das Journal von systemd auf.
package logbuch

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logbuch struct {
	mu      sync.Mutex
	pfad    string
	datei   *os.File
	groesse int64
	echo    io.Writer
	system  systemLog
}

// systemLog ist die Ereignisanzeige (Windows) oder nichts.
type systemLog interface {
	Info(id uint32, text string) error
	Warning(id uint32, text string) error
	Error(id uint32, text string) error
	Close() error
}

const maxGroesse = 1 << 20

// Neu öffnet pfad (leer = keine Datei). echo bekommt jede Zeile zusätzlich
// (stderr im Vordergrund, nil im Dienst).
func Neu(pfad string, echo io.Writer) *Logbuch {
	l := &Logbuch{pfad: pfad, echo: echo, system: systemOeffnen()}
	if pfad != "" {
		_ = os.MkdirAll(filepath.Dir(pfad), 0o750)
		l.oeffnen()
	}
	return l
}

func (l *Logbuch) oeffnen() {
	f, err := os.OpenFile(l.pfad, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
	if err != nil {
		return
	}
	l.datei = f
	if st, err := f.Stat(); err == nil {
		l.groesse = st.Size()
	}
}

func (l *Logbuch) schreiben(stufe, text string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	zeile := fmt.Sprintf("%s %-7s %s\n", time.Now().Format("2006-01-02 15:04:05"), stufe, text)
	if l.echo != nil {
		_, _ = io.WriteString(l.echo, zeile)
	}
	if l.datei != nil {
		if l.groesse+int64(len(zeile)) > maxGroesse {
			_ = l.datei.Close()
			_ = os.Rename(l.pfad, l.pfad+".1")
			l.oeffnen()
			l.groesse = 0
		}
		if l.datei != nil {
			n, _ := io.WriteString(l.datei, zeile)
			l.groesse += int64(n)
		}
	}
}

// Info, Warnung, Fehler: die Ereignis-ID trennt nur die Stufen (1, 2, 3) —
// gefiltert wird in der Ereignisanzeige nach Quelle, nicht nach Nummer.
func (l *Logbuch) Info(format string, a ...any) {
	t := fmt.Sprintf(format, a...)
	l.schreiben("INFO", t)
	if l.system != nil {
		_ = l.system.Info(1, t)
	}
}

func (l *Logbuch) Warnung(format string, a ...any) {
	t := fmt.Sprintf(format, a...)
	l.schreiben("WARNUNG", t)
	if l.system != nil {
		_ = l.system.Warning(2, t)
	}
}

func (l *Logbuch) Fehler(format string, a ...any) {
	t := fmt.Sprintf(format, a...)
	l.schreiben("FEHLER", t)
	if l.system != nil {
		_ = l.system.Error(3, t)
	}
}

func (l *Logbuch) Schliessen() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.datei != nil {
		_ = l.datei.Close()
		l.datei = nil
	}
	if l.system != nil {
		_ = l.system.Close()
	}
}
