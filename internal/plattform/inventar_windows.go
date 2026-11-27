// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package plattform

import (
	"context"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// Das Langsame (BitLocker, Update-Verlauf, Secure Boot) liest ein kleines
// PowerShell-Skript: dafür gibt es keine Win32-Aufrufe, nur WMI und COM, und
// beides aus Go anzusprechen hiesse eine COM-Bibliothek mitzunehmen.
// PowerShell 5.1 ist auf jedem Windows 10/11 da.
//
//go:embed langsam_windows.ps1
var langsamSkript string

func (w *Windows) Inventar(ctx context.Context, voll bool) (*protokoll.Inventar, error) {
	var fehler []string
	inv := &protokoll.Inventar{Hostname: hostname(), LaufzeitSekunden: laufzeitSekunden(), Zusatz: map[string]any{}}
	inv.Betriebssystem = betriebssystem()

	if s, err := sitzungen(); err != nil {
		fehler = append(fehler, "Sitzungen: "+err.Error())
	} else {
		for _, x := range s {
			inv.Personen = append(inv.Personen, protokoll.Person{Name: x.Name, Sitzung: fmt.Sprintf("%d %s", x.ID, x.Station), Zustand: sitzungsZustand(x.Zustand)})
		}
	}
	if n, err := netzWindows(); err != nil {
		fehler = append(fehler, "Netz: "+err.Error())
	} else {
		inv.Netz = n
	}
	inv.Platten = plattenWindows()
	if neustartAusstehend() {
		t := true
		inv.Updates.NeustartAusstehend = &t
	} else {
		f := false
		inv.Updates.NeustartAusstehend = &f
	}
	if d := domaene(); d != "" {
		inv.Zusatz["domaene"] = d
	}

	w.mu.Lock()
	alt, wann := w.langsam, w.langsamZ
	w.mu.Unlock()
	if voll || wann.IsZero() {
		neu, err := langsamLesen(ctx)
		if err != nil {
			fehler = append(fehler, "BitLocker/Updates: "+err.Error())
			neu.UpdatesFehler = err.Error()
		}
		w.mu.Lock()
		w.langsam, w.langsamZ = neu, time.Now()
		w.mu.Unlock()
		alt = neu
	}
	alt.eintragen(inv)

	if len(fehler) > 0 {
		return inv, errors.New(strings.Join(fehler, "; "))
	}
	return inv, nil
}

func sitzungsZustand(z uint32) string {
	switch z {
	case windows.WTSActive:
		return "aktiv"
	case windows.WTSDisconnected:
		return "getrennt"
	}
	return "andere"
}

// betriebssystem liest die Angaben, die winver zeigt. Windows 11 nennt sich
// in ProductName weiterhin "Windows 10" — das Erkennungszeichen ist die
// Build-Nummer ab 22000.
func betriebssystem() protokoll.Betriebssystem {
	bs := protokoll.Betriebssystem{Name: "Windows"}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return bs
	}
	defer k.Close()
	name, _, _ := k.GetStringValue("ProductName")
	ver, _, _ := k.GetStringValue("DisplayVersion")
	build, _, _ := k.GetStringValue("CurrentBuild")
	ubr, _, _ := k.GetIntegerValue("UBR")
	bs.Name = windowsName(name, build)
	bs.Version = ver
	if build != "" {
		bs.Build = build
		if ubr > 0 {
			bs.Build = fmt.Sprintf("%s.%d", build, ubr)
		}
	}
	return bs
}

func netzWindows() ([]protokoll.Adapter, error) {
	groesse := uint32(15000)
	var buf []byte
	var erst *windows.IpAdapterAddresses
	for i := 0; i < 3; i++ {
		buf = make([]byte, groesse)
		erst = (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(syscall.AF_UNSPEC, windows.GAA_FLAG_INCLUDE_PREFIX, 0, erst, &groesse)
		if err == nil {
			break
		}
		if err != windows.ERROR_BUFFER_OVERFLOW {
			return nil, err
		}
		if i == 2 {
			return nil, err
		}
	}
	quelle := standardQuelle()
	var aus []protokoll.Adapter
	for a := erst; a != nil; a = a.Next {
		if a.IfType == windows.IF_TYPE_SOFTWARE_LOOPBACK || a.IfType == windows.IF_TYPE_TUNNEL {
			continue
		}
		beschr := windows.UTF16PtrToString(a.Description)
		art := "andere"
		switch {
		case istVirtuell(beschr):
		case a.IfType == windows.IF_TYPE_ETHERNET_CSMACD:
			art = "kabel"
		case a.IfType == windows.IF_TYPE_IEEE80211:
			art = "wlan"
		}
		var mac string
		if a.PhysicalAddressLength == 6 {
			mac = net.HardwareAddr(a.PhysicalAddress[:6]).String()
		}
		var addrs []net.Addr
		for u := a.FirstUnicastAddress; u != nil; u = u.Next {
			ip := u.Address.IP()
			if ip == nil {
				continue
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			addrs = append(addrs, &net.IPNet{IP: ip, Mask: net.CIDRMask(int(u.OnLinkPrefixLength), bits)})
		}
		v4, v6 := adressenTeilen(addrs)
		aus = append(aus, protokoll.Adapter{
			Name: windows.UTF16PtrToString(a.FriendlyName), MAC: mac, Art: art,
			Verbunden: a.OperStatus == windows.IfOperStatusUp, Standard: istStandard(v4, quelle),
			IPv4: leer(v4), IPv6: leer(v6),
		})
	}
	sortiereNetz(aus)
	return aus, nil
}

func leer(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func plattenWindows() []protokoll.Platte {
	buf := make([]uint16, 256)
	n, err := windows.GetLogicalDriveStrings(uint32(len(buf)), &buf[0])
	if err != nil || n == 0 {
		return nil
	}
	var aus []protokoll.Platte
	for _, wurzel := range strings.Split(string(utf16.Decode(buf[:n])), "\x00") {
		if wurzel == "" {
			continue
		}
		p, _ := windows.UTF16PtrFromString(wurzel)
		if windows.GetDriveType(p) != windows.DRIVE_FIXED {
			continue
		}
		var frei, gesamt, freiGesamt uint64
		if windows.GetDiskFreeSpaceEx(p, &frei, &gesamt, &freiGesamt) != nil {
			continue
		}
		aus = append(aus, protokoll.Platte{Laufwerk: strings.TrimSuffix(wurzel, `\`), GesamtBytes: gesamt, FreiBytes: freiGesamt})
	}
	return aus
}

// neustartAusstehend: Windows Update oder ein Installationsprogramm wartet
// auf einen Neustart. Dieselben Schlüssel, die auch die Update-Seite liest.
func neustartAusstehend() bool {
	for _, pfad := range []string{
		`SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`,
	} {
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, pfad, registry.QUERY_VALUE); err == nil {
			k.Close()
			return true
		}
	}
	return false
}

func domaene() string {
	var name *uint16
	var art uint32
	if windows.NetGetJoinInformation(nil, &name, &art) != nil {
		return ""
	}
	defer windows.NetApiBufferFree((*byte)(unsafe.Pointer(name)))
	if art != windows.NetSetupDomainName {
		return ""
	}
	return windows.UTF16PtrToString(name)
}

// ─── Das Langsame ───────────────────────────────────────────────────

func langsamLesen(ctx context.Context) (langsamesInventar, error) {
	var l langsamesInventar
	ctx, abbruch := context.WithTimeout(ctx, 90*time.Second)
	defer abbruch()
	aus, err := powershell(ctx, langsamSkript)
	if err != nil {
		return l, err
	}
	return l, jsonAusPowershell(aus, &l)
}

// powershell führt ein Skript aus, ohne es auf die Platte zu legen
// (-EncodedCommand: UTF-16LE, Base64) — so greift weder eine Ausführungs-
// richtlinie noch AppLocker auf einen Dateipfad.
func powershell(ctx context.Context, skript string, args ...string) ([]byte, error) {
	u := utf16.Encode([]rune(skript))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		b[2*i], b[2*i+1] = byte(c), byte(c>>8)
	}
	cmd := exec.CommandContext(ctx, powershellPfad(), append([]string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(b)}, args...)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Output()
}

func powershellPfad() string {
	sys, err := windows.GetSystemDirectory()
	if err != nil {
		return "powershell.exe"
	}
	return sys + `\WindowsPowerShell\v1.0\powershell.exe`
}
