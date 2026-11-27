// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package plattform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Rosenweg/rosenweg-agent/internal/konfig"
	"github.com/Rosenweg/rosenweg-agent/internal/logbuch"
	"github.com/Rosenweg/rosenweg-agent/internal/protokoll"
)

// Linux: eine Station von os-stationen. Kennung und Token stehen schon da
// (install.sh, /etc/rosenweg-station/), der Agent übernimmt sie — eine
// zweite Registrierung braucht es nicht. Die Sperre setzt weiter
// station-sperre um (os-stationen/agent), Befehle laufen über systemd.
//
// Stand (27.11.2026): Inventar, Lebenszeichen, Nachricht, Abmelden,
// Neustart. Was station-agent.sh darüber hinaus tut (Update, Zurücksetzen,
// SSH-Wartung, MQTT), bleibt bis zur Umstellung dort — docs/umstellung.md.
type Linux struct {
	K   konfig.Konfig
	Log *logbuch.Logbuch
}

func Vorgaben() konfig.Konfig {
	return konfig.Konfig{
		StationDatei: "/etc/rosenweg-station/station-id",
		TokenDatei:   "/etc/rosenweg-station/token",
		APIDatei:     "/etc/rosenweg-station/api-url",
		Daten:        "/var/lib/rosenweg-agent",
	}
}

func KonfigPfad() string { return "/etc/rosenweg-agent/agent.json" }

func Neu(k konfig.Konfig, log *logbuch.Logbuch) *Linux {
	// Die API-Adresse, die os-stationen hinterlegt hat, gilt vor der Vorgabe.
	if b, err := os.ReadFile(k.APIDatei); err == nil && k.API == konfig.VorgabeAPI {
		if s := strings.TrimSpace(string(b)); s != "" {
			k.API = strings.TrimRight(s, "/")
		}
	}
	return &Linux{K: k, Log: log}
}

func (l *Linux) Name() string { return "linux" }

func (l *Linux) Anmelden(ctx context.Context, c *protokoll.Client, a protokoll.Anmeldung) (string, string, error) {
	id, err1 := os.ReadFile(l.K.StationDatei)
	tok, err2 := os.ReadFile(l.K.TokenDatei)
	if err1 != nil || err2 != nil {
		return "", "", &protokoll.Fehler{Status: 404, Text: "keine Station: " + l.K.TokenDatei + " fehlt (install.sh von os-stationen)"}
	}
	return strings.TrimSpace(string(id)), strings.TrimSpace(string(tok)), nil
}

func (l *Linux) Inventar(ctx context.Context, voll bool) (*protokoll.Inventar, error) {
	var fehler []string
	h, _ := os.Hostname()
	inv := &protokoll.Inventar{Hostname: h, Zusatz: map[string]any{}}
	inv.Betriebssystem = linuxBetriebssystem()
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		inv.LaufzeitSekunden = uptimeLesen(string(b))
	}
	inv.Personen = personenLinux(ctx)
	n, err := netzLinux()
	if err != nil {
		fehler = append(fehler, "Netz: "+err.Error())
	}
	inv.Netz = n
	for _, pfad := range []string{"/", "/persist", "/home"} {
		var st unix.Statfs_t
		if unix.Statfs(pfad, &st) != nil || st.Blocks == 0 {
			continue
		}
		bs := uint64(st.Bsize)
		inv.Platten = append(inv.Platten, protokoll.Platte{Laufwerk: pfad, GesamtBytes: st.Blocks * bs, FreiBytes: st.Bavail * bs})
	}
	// Was os-stationen über sich weiss: Rolle, OS- und Konfig-Stand.
	var st struct {
		Role          string `json:"role"`
		OSVersion     string `json:"os_version"`
		ConfigVersion string `json:"config_version"`
	}
	if zustandLesen("/etc/rosenweg-station/state.json", &st) {
		if st.Role != "" {
			inv.Zusatz["rolle"] = st.Role
		}
		if st.OSVersion != "" {
			inv.Zusatz["os_stationen"] = st.OSVersion
		}
		if st.ConfigVersion != "" {
			inv.Zusatz["konfig"] = st.ConfigVersion
		}
	}
	if len(fehler) > 0 {
		return inv, errors.New(strings.Join(fehler, "; "))
	}
	return inv, nil
}

func linuxBetriebssystem() protokoll.Betriebssystem {
	bs := protokoll.Betriebssystem{Name: "Linux"}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		w := osRelease(string(b))
		if w["PRETTY_NAME"] != "" {
			bs.Name = w["PRETTY_NAME"]
		}
		bs.Version = w["VERSION_ID"]
	}
	var u unix.Utsname
	if unix.Uname(&u) == nil {
		bs.Kernel = unix.ByteSliceToString(u.Release[:])
	}
	return bs
}

// personenLinux: wer angemeldet ist, aus loginctl (grafische Sitzungen und
// SSH), ohne Systemkonten.
func personenLinux(ctx context.Context) []protokoll.Person {
	ctx, abbruch := context.WithTimeout(ctx, 5*time.Second)
	defer abbruch()
	aus, err := exec.CommandContext(ctx, "loginctl", "list-sessions", "--no-legend").Output()
	if err != nil {
		return nil
	}
	return loginctlSitzungen(string(aus), func(id string) string {
		z, err := exec.CommandContext(ctx, "loginctl", "show-session", id, "-p", "State", "--value").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(z))
	})
}

func netzLinux() ([]protokoll.Adapter, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	quelle := standardQuelle()
	var aus []protokoll.Adapter
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		art := "andere"
		basis := filepath.Join("/sys/class/net", i.Name)
		if _, err := os.Stat(filepath.Join(basis, "device")); err == nil {
			art = "kabel"
			if _, err := os.Stat(filepath.Join(basis, "wireless")); err == nil {
				art = "wlan"
			}
		}
		addrs, _ := i.Addrs()
		v4, v6 := adressenTeilen(addrs)
		if v4 == nil {
			v4 = []string{}
		}
		if v6 == nil {
			v6 = []string{}
		}
		aus = append(aus, protokoll.Adapter{Name: i.Name, MAC: i.HardwareAddr.String(), Art: art,
			Verbunden: i.Flags&net.FlagUp != 0 && i.Flags&net.FlagRunning != 0, Standard: istStandard(v4, quelle), IPv4: v4, IPv6: v6})
	}
	sortiereNetz(aus)
	return aus, nil
}

// ─── Sperre: station-sperre aus os-stationen ───────────────────────

func (l *Linux) SperreGespeichert() (bool, string) {
	// /etc/nologin liegt auf dem flüchtigen Root; station-agent.sh stellt die
	// Sperre nach dem Neustart über das Lebenszeichen wieder her. Hier
	// genauso — nichts Eigenes gespeichert.
	return false, ""
}

func (l *Linux) Sperre(ctx context.Context, an bool, grund string) error {
	pfad, err := exec.LookPath("station-sperre")
	if err != nil {
		return errors.New("station-sperre fehlt (nur auf os-stationen)")
	}
	args := []string{"aus"}
	if an {
		args = []string{"an", grund}
	}
	if aus, err := exec.CommandContext(ctx, pfad, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("station-sperre: %v %s", err, kurz(string(aus), 200))
	}
	return nil
}

// ─── Befehle ────────────────────────────────────────────────────────

func (l *Linux) Ausfuehren(ctx context.Context, art string, roh json.RawMessage) (string, string) {
	switch art {
	case "nachricht":
		var p nachrichtParameter
		if err := lesen(roh, &p); err != nil || strings.TrimSpace(p.Text) == "" {
			return "abgelehnt", "kein Text"
		}
		n := l.nachricht(ctx, p.Text)
		if n == 0 {
			return "abgelehnt", "niemand angemeldet"
		}
		return "ok", fmt.Sprintf("angezeigt für %d Person(en)", n)
	case "abmelden":
		var p verzoegerungParameter
		if err := lesen(roh, &p); err != nil {
			return "abgelehnt", err.Error()
		}
		vor := grenzen(p.VorwarnungSekunden, 30, 900, 60)
		nutzer := l.angemeldete(ctx)
		if len(nutzer) == 0 {
			return "abgelehnt", "niemand angemeldet"
		}
		text := "Du wirst in " + dauerText(vor) + " abgemeldet. Bitte jetzt speichern."
		if p.Text != "" {
			text = p.Text + " — " + text
		}
		l.nachricht(ctx, text)
		go func() {
			time.Sleep(time.Duration(vor) * time.Second)
			for _, n := range nutzer {
				if err := exec.Command("loginctl", "terminate-user", n).Run(); err != nil {
					l.Log.Warnung("%s nicht abgemeldet: %v", n, err)
				}
			}
		}()
		return "ok", fmt.Sprintf("%d Person(en) werden in %s abgemeldet", len(nutzer), dauerText(vor))
	case "neustart":
		var p verzoegerungParameter
		if err := lesen(roh, &p); err != nil {
			return "abgelehnt", err.Error()
		}
		verz := grenzen(p.VerzoegerungSekunden, 60, 3600, 300)
		// shutdown rechnet in Minuten; aufrunden, damit die Vorwarnung nie
		// kürzer ist als verlangt.
		min := (verz + 59) / 60
		text := Absender + ": Neustart in " + dauerText(min*60)
		if p.Text != "" {
			text += " — " + p.Text
		}
		l.nachricht(ctx, text)
		if aus, err := exec.CommandContext(ctx, "shutdown", "-r", fmt.Sprintf("+%d", min), text).CombinedOutput(); err != nil {
			return "fehler", fmt.Sprintf("shutdown: %v %s", err, kurz(string(aus), 200))
		}
		return "ok", "Neustart in " + dauerText(min*60)
	}
	return "abgelehnt", "auf Linux nicht vorgesehen: " + art
}

// angemeldete: Konten mit Sitzung, ohne Systemkonten (UID < 1000).
func (l *Linux) angemeldete(ctx context.Context) []string {
	aus, err := exec.CommandContext(ctx, "loginctl", "list-users", "--no-legend").Output()
	if err != nil {
		return nil
	}
	return loginctlNutzer(string(aus))
}

// nachricht zeigt den Text jeder angemeldeten Person als Desktop-Meldung
// (notify-send in ihrer Sitzung) und zusätzlich in jedem Terminal (wall).
func (l *Linux) nachricht(ctx context.Context, text string) int {
	n := 0
	aus, _ := exec.CommandContext(ctx, "loginctl", "list-users", "--no-legend").Output()
	for _, z := range strings.Split(string(aus), "\n") {
		f := strings.Fields(z)
		if len(f) < 2 || !uidMensch(f[0]) {
			continue
		}
		bus := "unix:path=/run/user/" + f[0] + "/bus"
		cmd := exec.CommandContext(ctx, "runuser", "-u", f[1], "--", "env", "DBUS_SESSION_BUS_ADDRESS="+bus,
			"notify-send", "-u", "critical", "-a", Absender, Absender, text)
		if cmd.Run() == nil {
			n++
		}
	}
	_ = exec.CommandContext(ctx, "wall", Absender+": "+text).Run()
	return n
}
