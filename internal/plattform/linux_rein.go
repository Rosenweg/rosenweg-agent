// SPDX-License-Identifier: GPL-3.0-or-later

package plattform

// Was das Linux-Inventar aus Text macht — ohne Linux prüfbar (linux_rein_test.go).

import (
	"strconv"
	"strings"

	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// osRelease liest /etc/os-release (SCHLUESSEL=wert, Werte ggf. in Anführungszeichen).
func osRelease(text string) map[string]string {
	w := map[string]string{}
	for _, z := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(z), "=")
		if !ok || k == "" || strings.HasPrefix(k, "#") {
			continue
		}
		w[k] = strings.Trim(v, `"'`)
	}
	return w
}

// uptimeLesen: erste Zahl aus /proc/uptime, ganze Sekunden.
func uptimeLesen(text string) int64 {
	f := strings.Fields(text)
	if len(f) == 0 {
		return 0
	}
	s, _, _ := strings.Cut(f[0], ".")
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func uidMensch(uid string) bool {
	n, err := strconv.Atoi(uid)
	return err == nil && n >= 1000 && n < 60000
}

// loginctlNutzer: "UID NAME …" je Zeile -> Namen der Menschen.
func loginctlNutzer(text string) []string {
	var aus []string
	for _, z := range strings.Split(text, "\n") {
		f := strings.Fields(z)
		if len(f) >= 2 && uidMensch(f[0]) {
			aus = append(aus, f[1])
		}
	}
	return aus
}

// loginctlSitzungen: "SESSION UID USER SEAT TTY …" je Zeile. zustand fragt
// den Zustand einer Sitzung ab (active, online, closing).
func loginctlSitzungen(text string, zustand func(id string) string) []protokoll.Person {
	var aus []protokoll.Person
	for _, z := range strings.Split(text, "\n") {
		f := strings.Fields(z)
		if len(f) < 3 || !uidMensch(f[1]) {
			continue
		}
		p := protokoll.Person{Name: f[2], Sitzung: f[0], Zustand: "andere"}
		switch zustand(f[0]) {
		case "active":
			p.Zustand = "aktiv"
		case "online":
			p.Zustand = "getrennt"
		}
		aus = append(aus, p)
	}
	return aus
}
