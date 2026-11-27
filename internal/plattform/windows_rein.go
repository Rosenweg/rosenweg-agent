// SPDX-License-Identifier: GPL-3.0-or-later

package plattform

// Was das Windows-Inventar aus Text macht, ohne Windows zu brauchen — damit
// es sich auf jeder Plattform prüfen lässt (windows_rein_test.go).

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"

	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// langsamesInventar ist die Ausgabe von langsam_windows.ps1.
type langsamesInventar struct {
	Verschluesselung []struct {
		Laufwerk string `json:"laufwerk"`
		Schutz   string `json:"schutz"`
		Zustand  string `json:"zustand"`
		Prozent  *int   `json:"prozent"`
	} `json:"verschluesselung"`
	Updates []struct {
		Titel    string `json:"titel"`
		Datum    string `json:"datum"`
		Ergebnis int    `json:"ergebnis"`
	} `json:"updates"`
	UpdatesFehler string `json:"updates_fehler"`
	SecureBoot    *bool  `json:"secure_boot"`
}

func (l langsamesInventar) eintragen(inv *protokoll.Inventar) {
	for _, v := range l.Verschluesselung {
		inv.Verschluesselung = append(inv.Verschluesselung, protokoll.Verschluesselung{
			Laufwerk: v.Laufwerk, Schutz: bitlockerSchutz(v.Schutz), Zustand: v.Zustand, Prozent: v.Prozent,
		})
	}
	// Der Verlauf kommt neueste zuerst. "Zuletzt" ist das neueste, das
	// geklappt hat (ResultCode 2 = erfolgreich, 3 = mit Fehlern).
	for _, u := range l.Updates {
		ok := u.Ergebnis == 2 || u.Ergebnis == 3
		inv.Updates.Liste = append(inv.Updates.Liste, protokoll.Update{Titel: u.Titel, Datum: u.Datum, OK: &ok})
		if ok && inv.Updates.Zuletzt == "" {
			inv.Updates.Zuletzt = u.Datum
		}
	}
	inv.Updates.Fehler = l.UpdatesFehler
	if l.SecureBoot != nil {
		if inv.Zusatz == nil {
			inv.Zusatz = map[string]any{}
		}
		inv.Zusatz["secure_boot"] = *l.SecureBoot
	}
}

// bitlockerSchutz: ProtectionStatus als Text (On, Off, Unknown) — oder als
// Zahl, falls eine PowerShell die Aufzählung so ausgibt (1, 0, 2).
func bitlockerSchutz(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "1":
		return "an"
	case "off", "0":
		return "aus"
	}
	return "unbekannt"
}

// jsonAusPowershell nimmt die letzte Zeile, die mit { beginnt. Davor kann
// stehen, was ein Modul beim Laden ausgibt; ein BOM wird übergangen.
func jsonAusPowershell(aus []byte, ziel any) error {
	aus = bytes.TrimPrefix(aus, []byte("\xef\xbb\xbf"))
	zeilen := strings.Split(strings.ReplaceAll(string(aus), "\r\n", "\n"), "\n")
	for i := len(zeilen) - 1; i >= 0; i-- {
		z := strings.TrimSpace(zeilen[i])
		if strings.HasPrefix(z, "{") {
			return json.Unmarshal([]byte(z), ziel)
		}
	}
	return errors.New("keine JSON-Ausgabe")
}

// windowsName: Windows 11 nennt sich in ProductName weiterhin "Windows 10" —
// das Erkennungszeichen ist die Build-Nummer ab 22000.
func windowsName(produkt, build string) string {
	if produkt == "" {
		produkt = "Windows"
	}
	if b, err := strconv.Atoi(build); err == nil && b >= 22000 {
		produkt = strings.Replace(produkt, "Windows 10", "Windows 11", 1)
	}
	return produkt
}

// Virtuelle Adapter erkennt man am Namen des Treibers; IfType sagt bei
// Hyper-V "Ethernet". Gemeint sind die Adapter, die ein Rechner SELBST
// aufspannt (vSwitch, VPN, WSL) — sie hängen an keinem Switch-Port.
// Die Netzkarte einer VM ist dagegen echt im Sinn des Anschluss-Wächters:
// VirtIO (win11-1 in Proxmox) und "Microsoft Hyper-V Network Adapter" (Gast
// unter Hyper-V) hängen an einer Brücke, und UniFi sieht ihre MAC. Das
// zeigte der Windows-Lauf der CI (Azure-VM, 27.11.2026).
var virtuell = regexp.MustCompile(`(?i)hyper-v virtual|virtual adapter|virtual ethernet|vpn|tap-windows|wintun|wireguard|vmware virtual|virtualbox host|loopback|bluetooth|kernel debug|wan miniport`)

// istVirtuell: Beschreibung eines Adapters (GetAdaptersAddresses Description).
func istVirtuell(beschreibung string) bool { return virtuell.MatchString(beschreibung) }
