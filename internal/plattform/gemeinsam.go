// SPDX-License-Identifier: GPL-3.0-or-later

// Package plattform enthält, was Windows und Linux verschieden machen:
// Inventar, Anmeldung, Sperre, Befehle. Die Schleife darum ist
// internal/agent und für alle gleich.
package plattform

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// Parameter der Befehle, wie die API sie prüft (lib/stationprotokoll.js).
type nachrichtParameter struct {
	Text string `json:"text"`
}

type verzoegerungParameter struct {
	VerzoegerungSekunden int    `json:"verzoegerung_sekunden"`
	VorwarnungSekunden   int    `json:"vorwarnung_sekunden"`
	Text                 string `json:"text"`
}

func lesen(roh json.RawMessage, ziel any) error {
	if len(roh) == 0 {
		return nil
	}
	return json.Unmarshal(roh, ziel)
}

// grenzen hält Zeiten in dem Rahmen, den die API auch prüft — falls ein
// Befehl je an ihr vorbei käme.
func grenzen(s, min, max, vorgabe int) int {
	if s == 0 {
		return vorgabe
	}
	if s < min {
		return min
	}
	if s > max {
		return max
	}
	return s
}

// Titel in jeder Nachricht an die Person vor dem Gerät — damit klar ist,
// dass das vom Haus kommt und nicht aus dem Internet.
const Absender = "Technik Rosenweg"

// standardQuelle ist die lokale Adresse, über die das Gerät ins Netz geht.
// UDP "verbinden" schickt kein Paket, legt nur die Route fest.
func standardQuelle() net.IP {
	c, err := net.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		return nil
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok {
		return a.IP
	}
	return nil
}

// adressenTeilen trennt IPv4 und IPv6 und lässt Link-lokale weg (fe80:: und
// 169.254.x — die sagen nichts darüber, wo das Gerät hängt).
func adressenTeilen(addrs []net.Addr) (v4, v6 []string) {
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		if ipn.IP.To4() != nil {
			v4 = append(v4, ipn.IP.String())
		} else {
			v6 = append(v6, ipn.IP.String())
		}
	}
	return
}

// istStandard: trägt dieser Adapter die Adresse der Standardroute?
func istStandard(v4 []string, quelle net.IP) bool {
	if quelle == nil {
		return false
	}
	for _, a := range v4 {
		if a == quelle.String() {
			return true
		}
	}
	return false
}

func sortiereNetz(n []protokoll.Adapter) {
	rang := map[string]int{"kabel": 0, "wlan": 1, "andere": 2}
	sort.SliceStable(n, func(i, j int) bool {
		if n[i].Standard != n[j].Standard {
			return n[i].Standard
		}
		return rang[n[i].Art] < rang[n[j].Art]
	})
}

// zustandSpeichern schreibt atomar (tmp + rename), damit ein Stromausfall
// mitten im Schreiben keine halbe Sperre hinterlässt.
func zustandSpeichern(pfad string, wert any) error {
	b, err := json.MarshalIndent(wert, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pfad), 0o750); err != nil {
		return err
	}
	tmp := pfad + ".neu"
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, pfad)
}

func zustandLesen(pfad string, ziel any) bool {
	b, err := os.ReadFile(pfad)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, ziel) == nil
}

func kurz(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func dauerText(s int) string {
	d := time.Duration(s) * time.Second
	if d >= time.Minute && d%time.Minute == 0 {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d s", s)
}
