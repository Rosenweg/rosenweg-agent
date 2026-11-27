// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

// Package dienst: auf Linux startet systemd den Agenten
// (packaging/linux/rosenweg-agent.service), ein eigener Dienstrahmen ist
// nicht nötig.
package dienst

import (
	"context"
	"errors"
)

const Name = "rosenweg-agent"

func IstDienst() bool { return false }

var nurWindows = errors.New("nur auf Windows — auf Linux: systemctl enable --now rosenweg-agent")

func Installieren(string) error              { return nurWindows }
func Starten() error                         { return nurWindows }
func Entfernen() error                       { return nurWindows }
func Laufen(func(ctx context.Context)) error { return nurWindows }
