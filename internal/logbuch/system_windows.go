// SPDX-License-Identifier: GPL-3.0-or-later

package logbuch

import "golang.org/x/sys/windows/svc/eventlog"

// Quelle in der Ereignisanzeige. Eingetragen von "rosenweg-agent installieren".
const Quelle = "Rosenweg-Agent"

func systemOeffnen() systemLog {
	l, err := eventlog.Open(Quelle)
	if err != nil {
		return nil
	}
	return l
}
