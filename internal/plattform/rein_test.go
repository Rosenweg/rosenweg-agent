// SPDX-License-Identifier: GPL-3.0-or-later

package plattform

import (
	"testing"

	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// Ausgabe von langsam_windows.ps1, wie PowerShell 5.1 sie auf einem
// deutschen Windows 11 schreibt (gekürzt), mit Modul-Geschwätz davor.
const psAusgabe = "\xef\xbb\xbfWARNUNG: irgendein Modul\r\n" +
	`{"verschluesselung":[{"laufwerk":"C:","schutz":"On","zustand":"FullyEncrypted","prozent":100},{"laufwerk":"D:","schutz":"Off","zustand":"FullyDecrypted","prozent":0}],` +
	`"updates":[{"titel":"2026-11 Kumulatives Update für Windows 11 Version 24H2 (KB5046617)","datum":"2026-11-20T03:12:00.0000000Z","ergebnis":2},` +
	`{"titel":"Defender-Update","datum":"2026-11-19T03:00:00.0000000Z","ergebnis":4}],"updates_fehler":null,"secure_boot":false}` + "\r\n"

func TestLangsamesInventar(t *testing.T) {
	var l langsamesInventar
	if err := jsonAusPowershell([]byte(psAusgabe), &l); err != nil {
		t.Fatal(err)
	}
	inv := &protokoll.Inventar{}
	l.eintragen(inv)
	if len(inv.Verschluesselung) != 2 || inv.Verschluesselung[0].Schutz != "an" || inv.Verschluesselung[1].Schutz != "aus" {
		t.Fatalf("BitLocker: %+v", inv.Verschluesselung)
	}
	if inv.Updates.Zuletzt != "2026-11-20T03:12:00.0000000Z" || *inv.Updates.Liste[1].OK {
		t.Fatalf("Updates: %+v", inv.Updates)
	}
	if inv.Zusatz["secure_boot"] != false {
		t.Fatalf("Secure Boot: %v", inv.Zusatz)
	}
	if err := jsonAusPowershell([]byte("Fehler\r\n"), &l); err == nil {
		t.Fatal("ohne JSON muss es einen Fehler geben")
	}
}

func TestBitlockerSchutz(t *testing.T) {
	for ein, aus := range map[string]string{"On": "an", "1": "an", "Off": "aus", "0": "aus", "Unknown": "unbekannt", "": "unbekannt"} {
		if g := bitlockerSchutz(ein); g != aus {
			t.Errorf("%q -> %q, erwartet %q", ein, g, aus)
		}
	}
}

func TestWindowsName(t *testing.T) {
	if g := windowsName("Windows 10 Pro", "26100"); g != "Windows 11 Pro" {
		t.Errorf("Windows 11 an der Build-Nummer: %q", g)
	}
	if g := windowsName("Windows 10 Pro", "19045"); g != "Windows 10 Pro" {
		t.Errorf("Windows 10 bleibt: %q", g)
	}
}

func TestLinuxTexte(t *testing.T) {
	w := osRelease("PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nVERSION_ID=\"13\"\n# x\n")
	if w["PRETTY_NAME"] != "Debian GNU/Linux 13 (trixie)" || w["VERSION_ID"] != "13" {
		t.Fatalf("os-release: %v", w)
	}
	if uptimeLesen("12345.67 999.00\n") != 12345 {
		t.Fatal("uptime")
	}
	nutzer := loginctlNutzer(" 1000 anna.beispiel\n  113 lightdm\n70001 x\n")
	if len(nutzer) != 1 || nutzer[0] != "anna.beispiel" {
		t.Fatalf("Nutzer: %v", nutzer)
	}
	p := loginctlSitzungen("  3 1000 anna.beispiel seat0 tty7\n c1 113 lightdm seat0\n", func(id string) string {
		if id == "3" {
			return "active"
		}
		return "online"
	})
	if len(p) != 1 || p[0].Zustand != "aktiv" || p[0].Name != "anna.beispiel" {
		t.Fatalf("Sitzungen: %+v", p)
	}
}

func TestGrenzen(t *testing.T) {
	if grenzen(0, 60, 3600, 300) != 300 || grenzen(5, 60, 3600, 300) != 60 || grenzen(9999, 60, 3600, 300) != 3600 {
		t.Fatal("grenzen")
	}
	if dauerText(300) != "5 min" || dauerText(90) != "90 s" {
		t.Fatal("dauerText")
	}
}

func TestVirtuelleAdapter(t *testing.T) {
	for b, v := range map[string]bool{
		"Red Hat VirtIO Ethernet Adapter":           false,
		"Microsoft Hyper-V Network Adapter":         false,
		"Intel(R) Ethernet Connection (17) I219-LM": false,
		"Intel(R) Wi-Fi 6E AX211 160MHz":            false,
		"Hyper-V Virtual Ethernet Adapter":          true,
		"WireGuard Tunnel":                          true,
		"TAP-Windows Adapter V9":                    true,
		"VirtualBox Host-Only Ethernet Adapter":     true,
		"Bluetooth Device (Personal Area Network)":  true,
	} {
		if istVirtuell(b) != v {
			t.Errorf("%q: virtuell=%v erwartet", b, v)
		}
	}
}
