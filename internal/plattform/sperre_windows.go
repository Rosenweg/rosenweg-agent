// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package plattform

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Sperre auf Windows — dasselbe, was station-sperre auf Linux tut:
//
//  1. Anmelden verhindern: die Gruppe Domänen-Benutzer bekommt "Lokal
//     anmelden verweigern" und "Anmelden über Remotedesktop verweigern".
//     Örtliche Konten (der lokale Administrator) kommen weiter herein — wie
//     root an einer gesperrten Linux-Station, damit vor Ort noch jemand
//     nachsehen kann.
//  2. Der Grund steht vor der Anmeldung (Anmeldehinweis, legalnotice*), wie
//     /etc/nologin auf Linux. Der vorherige Hinweis wird gesichert und beim
//     Entsperren zurückgeschrieben.
//  3. Wer schon angemeldet ist, bekommt eine Meldung und wird nach einer
//     Minute vom Bildschirm getrennt (Sperrbildschirm); entsperren kann er
//     nicht mehr, weil ihm das Anmelderecht fehlt. Offene Programme bleiben —
//     abgemeldet wird nicht, damit niemand Ungespeichertes verliert.
//
// Der Zustand steht in sperre.json (ProgramData), damit er einen Neustart
// ohne Netz übersteht. Aufgehoben wird nur durch eine echte Antwort der API
// oder durch "rosenweg-agent entfernen".

const (
	rechtLokal      = "SeDenyInteractiveLogonRight"
	rechtRemote     = "SeDenyRemoteInteractiveLogonRight"
	hinweisPfad     = `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`
	sperrVorwarnung = time.Minute
)

type sperrZustand struct {
	An      bool          `json:"an"`
	Grund   string        `json:"grund"`
	Seit    string        `json:"seit"`
	Hinweis *alterHinweis `json:"hinweis,omitempty"`
}

// alterHinweis: was vor der Sperre im Anmeldehinweis stand.
type alterHinweis struct {
	TitelDa bool   `json:"titel_da"`
	Titel   string `json:"titel"`
	TextDa  bool   `json:"text_da"`
	Text    string `json:"text"`
}

func (w *Windows) sperrPfad() string { return filepath.Join(w.K.Daten, "sperre.json") }

func (w *Windows) SperreGespeichert() (bool, string) {
	var z sperrZustand
	if zustandLesen(w.sperrPfad(), &z) {
		return z.An, z.Grund
	}
	return false, ""
}

func (w *Windows) Sperre(ctx context.Context, an bool, grund string) error {
	var z sperrZustand
	zustandLesen(w.sperrPfad(), &z)
	sid, err := domaenenBenutzer()
	if err != nil {
		return fmt.Errorf("Gruppe Domänen-Benutzer nicht gefunden: %w", err)
	}
	if !an {
		return w.entsperren(&z, sid)
	}

	neu := !z.An
	if neu {
		h, err := hinweisLesen()
		if err != nil {
			return err
		}
		z.Hinweis = &h
		z.Seit = time.Now().UTC().Format(time.RFC3339)
	}
	z.An, z.Grund = true, grund
	// Erst speichern, dann wirken: fällt der Strom dazwischen, weiss der
	// nächste Start, dass er den alten Hinweis zurückschreiben muss.
	if err := zustandSpeichern(w.sperrPfad(), z); err != nil {
		return err
	}
	var fehler []string
	for _, r := range []string{rechtLokal, rechtRemote} {
		if err := rechtSetzen(sid, r, true); err != nil {
			fehler = append(fehler, r+": "+err.Error())
		}
	}
	text := "Dieser Rechner ist gesperrt"
	if grund != "" {
		text += ": " + grund
	}
	text += ". Die Technik Rosenweg ist informiert. www.rosenweg4303.ch/stationen"
	if err := hinweisSchreiben(true, "Rechner gesperrt", true, text); err != nil {
		fehler = append(fehler, "Anmeldehinweis: "+err.Error())
	}

	dom := strings.ToUpper(domaene())
	s, _ := sitzungen()
	for _, x := range s {
		if !domaenenSitzung(x, dom) {
			continue
		}
		if neu {
			_ = nachrichtAnSitzung(x.ID, Absender, text+"\n\nDer Bildschirm wird in einer Minute gesperrt. Bitte jetzt speichern.", uint32(sperrVorwarnung.Seconds()))
			go func(id uint32) {
				time.Sleep(sperrVorwarnung)
				if an, _ := w.SperreGespeichert(); an {
					if err := sitzungTrennen(id); err != nil {
						w.Log.Warnung("Sitzung %d nicht getrennt: %v", id, err)
					}
				}
			}(x.ID)
		} else if x.Zustand == windows.WTSActive {
			// Schon gesperrt und trotzdem eine aktive Domänensitzung:
			// trennen (etwa nach einem Neustart mit automatischer Anmeldung).
			_ = sitzungTrennen(x.ID)
		}
	}
	if len(fehler) > 0 {
		return errors.New(strings.Join(fehler, "; "))
	}
	return nil
}

func (w *Windows) entsperren(z *sperrZustand, sid *windows.SID) error {
	var fehler []string
	for _, r := range []string{rechtLokal, rechtRemote} {
		if err := rechtSetzen(sid, r, false); err != nil {
			fehler = append(fehler, r+": "+err.Error())
		}
	}
	if z.Hinweis != nil {
		h := z.Hinweis
		if err := hinweisSchreiben(h.TitelDa, h.Titel, h.TextDa, h.Text); err != nil {
			fehler = append(fehler, "Anmeldehinweis: "+err.Error())
		}
	}
	if len(fehler) > 0 {
		return errors.New(strings.Join(fehler, "; "))
	}
	return zustandSpeichern(w.sperrPfad(), sperrZustand{An: false})
}

// AufraeumenBeimEntfernen hebt eine Sperre auf, bevor der Dienst entfernt
// wird — sonst bliebe ein Rechner gesperrt, den niemand mehr entsperren kann.
func (w *Windows) AufraeumenBeimEntfernen() error {
	if an, _ := w.SperreGespeichert(); !an {
		return nil
	}
	return w.Sperre(context.Background(), false, "")
}

func domaenenSitzung(s sitzung, dom string) bool {
	if dom == "" {
		return false
	}
	i := strings.Index(s.Name, `\`)
	return i > 0 && strings.EqualFold(s.Name[:i], dom)
}

// domaenenBenutzer: die SID der Gruppe Domänen-Benutzer (RID 513). Über die
// SID und nicht den Namen, weil der Name je Sprache anders heisst. Die
// Domänen-SID ist die des eigenen Computerkontos ohne den letzten Teil.
func domaenenBenutzer() (*windows.SID, error) {
	dom := domaene()
	if dom == "" {
		return nil, errors.New("Rechner ist in keiner Domäne")
	}
	sid, _, _, err := windows.LookupSID("", dom+`\`+hostname()+"$")
	if err != nil {
		return nil, err
	}
	s := sid.String()
	i := strings.LastIndex(s, "-")
	if i < 0 || !strings.HasPrefix(s, "S-1-5-21-") {
		return nil, fmt.Errorf("unerwartete SID %s", s)
	}
	return windows.StringToSid(s[:i] + "-513")
}

func hinweisLesen() (alterHinweis, error) {
	var h alterHinweis
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, hinweisPfad, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return h, nil
		}
		return h, err
	}
	defer k.Close()
	if v, _, err := k.GetStringValue("legalnoticecaption"); err == nil {
		h.TitelDa, h.Titel = true, v
	}
	if v, _, err := k.GetStringValue("legalnoticetext"); err == nil {
		h.TextDa, h.Text = true, v
	}
	return h, nil
}

func hinweisSchreiben(titelDa bool, titel string, textDa bool, text string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, hinweisPfad, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	setze := func(name string, da bool, wert string) error {
		if da {
			return k.SetStringValue(name, wert)
		}
		if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := setze("legalnoticecaption", titelDa, titel); err != nil {
		return err
	}
	return setze("legalnoticetext", textDa, text)
}
