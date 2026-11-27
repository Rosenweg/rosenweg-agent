// SPDX-License-Identifier: GPL-3.0-or-later

package plattform

// Die paar Windows-Aufrufe, die golang.org/x/sys/windows nicht mitbringt:
// Sitzungen (wtsapi32), Anmelderechte (LSA, advapi32), Laufzeit (kernel32).
// Alles in einer Datei, damit man sieht, was der Agent am System anfasst.

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	wtsapi32                        = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSQuerySessionInformationW = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSSendMessageW             = wtsapi32.NewProc("WTSSendMessageW")
	procWTSLogoffSession            = wtsapi32.NewProc("WTSLogoffSession")
	procWTSDisconnectSession        = wtsapi32.NewProc("WTSDisconnectSession")

	kernel32           = windows.NewLazySystemDLL("kernel32.dll")
	procGetTickCount64 = kernel32.NewProc("GetTickCount64")

	advapi32                   = windows.NewLazySystemDLL("advapi32.dll")
	procLsaOpenPolicy          = advapi32.NewProc("LsaOpenPolicy")
	procLsaAddAccountRights    = advapi32.NewProc("LsaAddAccountRights")
	procLsaRemoveAccountRights = advapi32.NewProc("LsaRemoveAccountRights")
	procLsaClose               = advapi32.NewProc("LsaClose")
	procLsaNtStatusToWinError  = advapi32.NewProc("LsaNtStatusToWinError")
)

const (
	wtsCurrentServer = 0
	wtsUserName      = 5
	wtsDomainName    = 7

	mbOK            = 0x0
	mbIconWarning   = 0x30
	mbSetForeground = 0x10000
	mbTopmost       = 0x40000
)

func laufzeitSekunden() int64 {
	r, _, _ := procGetTickCount64.Call()
	return int64(r / 1000)
}

type sitzung struct {
	ID      uint32
	Zustand uint32
	Station string
	Name    string // DOMÄNE\name
}

// sitzungen: alle Sitzungen mit angemeldeter Person (Konsole und RDP),
// ohne Sitzung 0 (Dienste).
func sitzungen() ([]sitzung, error) {
	var info *windows.WTS_SESSION_INFO
	var n uint32
	if err := windows.WTSEnumerateSessions(wtsCurrentServer, 0, 1, &info, &n); err != nil {
		return nil, err
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	liste := unsafe.Slice(info, n)
	var aus []sitzung
	for _, s := range liste {
		if s.SessionID == 0 {
			continue
		}
		name := sitzungsText(s.SessionID, wtsUserName)
		if name == "" {
			continue
		}
		if d := sitzungsText(s.SessionID, wtsDomainName); d != "" {
			name = d + `\` + name
		}
		aus = append(aus, sitzung{ID: s.SessionID, Zustand: s.State, Station: windows.UTF16PtrToString(s.WindowStationName), Name: name})
	}
	return aus, nil
}

func sitzungsText(id uint32, klasse uint32) string {
	var buf *uint16
	var laenge uint32
	r, _, _ := procWTSQuerySessionInformationW.Call(wtsCurrentServer, uintptr(id), uintptr(klasse),
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&laenge)))
	if r == 0 || buf == nil {
		return ""
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return windows.UTF16PtrToString(buf)
}

// nachrichtAnSitzung zeigt ein Meldungsfenster in der Sitzung, ohne auf den
// Klick zu warten (bWait = false): der Agent soll nicht hängen, weil
// niemand vor dem Bildschirm sitzt. Nach sekunden verschwindet es (0 = nie).
func nachrichtAnSitzung(id uint32, titel, text string, sekunden uint32) error {
	t, err := windows.UTF16FromString(titel)
	if err != nil {
		return err
	}
	m, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	var antwort uint32
	r, _, e := procWTSSendMessageW.Call(wtsCurrentServer, uintptr(id),
		uintptr(unsafe.Pointer(&t[0])), uintptr((len(t)-1)*2),
		uintptr(unsafe.Pointer(&m[0])), uintptr((len(m)-1)*2),
		uintptr(mbOK|mbIconWarning|mbSetForeground|mbTopmost), uintptr(sekunden),
		uintptr(unsafe.Pointer(&antwort)), 0)
	if r == 0 {
		return e
	}
	return nil
}

func sitzungAbmelden(id uint32) error {
	r, _, e := procWTSLogoffSession.Call(wtsCurrentServer, uintptr(id), 0)
	if r == 0 {
		return e
	}
	return nil
}

// sitzungTrennen trennt die Sitzung vom Bildschirm: an der Konsole erscheint
// der Sperrbildschirm, eine RDP-Verbindung bricht ab. Die Sitzung selbst
// (offene Programme) bleibt — das ist der Unterschied zum Abmelden.
func sitzungTrennen(id uint32) error {
	r, _, e := procWTSDisconnectSession.Call(wtsCurrentServer, uintptr(id), 0)
	if r == 0 {
		return e
	}
	return nil
}

// ─── LSA: Anmelderechte ────────────────────────────────────────────

type lsaUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        *uint16
}

type lsaObjectAttributes struct {
	Length                   uint32
	RootDirectory            windows.Handle
	ObjectName               *lsaUnicodeString
	Attributes               uint32
	SecurityDescriptor       uintptr
	SecurityQualityOfService uintptr
}

const (
	policyLookupNames          = 0x00000800
	policyCreateAccount        = 0x00000010
	ntStatusObjectNameNotFound = 0xC0000034
)

func lsaFehler(status uintptr) error {
	if status == 0 {
		return nil
	}
	w, _, _ := procLsaNtStatusToWinError.Call(status)
	return fmt.Errorf("LSA: %w", windows.Errno(w))
}

func lsaString(s string) (lsaUnicodeString, error) {
	u, err := windows.UTF16FromString(s)
	if err != nil {
		return lsaUnicodeString{}, err
	}
	n := uint16((len(u) - 1) * 2)
	return lsaUnicodeString{Length: n, MaximumLength: n + 2, Buffer: &u[0]}, nil
}

func lsaRichtlinie() (uintptr, error) {
	var attr lsaObjectAttributes
	attr.Length = uint32(unsafe.Sizeof(attr))
	var h uintptr
	st, _, _ := procLsaOpenPolicy.Call(0, uintptr(unsafe.Pointer(&attr)), policyLookupNames|policyCreateAccount, uintptr(unsafe.Pointer(&h)))
	if err := lsaFehler(st); err != nil {
		return 0, err
	}
	return h, nil
}

// rechtSetzen gibt sid das Recht (an) oder nimmt es ihm (aus).
func rechtSetzen(sid *windows.SID, recht string, an bool) error {
	h, err := lsaRichtlinie()
	if err != nil {
		return err
	}
	defer procLsaClose.Call(h)
	r, err := lsaString(recht)
	if err != nil {
		return err
	}
	var st uintptr
	if an {
		st, _, _ = procLsaAddAccountRights.Call(h, uintptr(unsafe.Pointer(sid)), uintptr(unsafe.Pointer(&r)), 1)
	} else {
		st, _, _ = procLsaRemoveAccountRights.Call(h, uintptr(unsafe.Pointer(sid)), 0, uintptr(unsafe.Pointer(&r)), 1)
		// Hatte das Konto das Recht gar nicht: nichts zu tun.
		if st == ntStatusObjectNameNotFound {
			return nil
		}
	}
	return lsaFehler(st)
}
