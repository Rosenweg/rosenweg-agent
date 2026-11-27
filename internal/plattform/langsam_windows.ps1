# SPDX-License-Identifier: GPL-3.0-or-later
# Langsame Teile des Windows-Inventars fuer rosenweg-agent (alle 30 Minuten
# und auf Befehl "Inventar jetzt lesen"). Laeuft als SYSTEM, wird nicht auf
# die Platte gelegt, sondern mit -EncodedCommand uebergeben.
# Ausgabe: genau eine Zeile JSON, Felder wie in internal/plattform/windows_rein.go.
# PowerShell 5.1, keine Zusatzmodule.
$ErrorActionPreference = 'Stop'
# Updates heissen "Kumulatives Update fuer ..." - ohne UTF-8 kaemen die
# Umlaute in der OEM-Codepage an.
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$ergebnis = [ordered]@{
    verschluesselung = $null
    updates          = @()
    updates_fehler   = $null
    secure_boot      = $null
}

# BitLocker. Fehlt das Modul (Home-Ausgabe), bleibt es leer - "unbekannt"
# ist ehrlicher als "aus".
try {
    $ergebnis.verschluesselung = @(Get-BitLockerVolume | ForEach-Object {
        [ordered]@{
            laufwerk = [string]$_.MountPoint
            schutz   = [string]$_.ProtectionStatus
            zustand  = [string]$_.VolumeStatus
            prozent  = [int]$_.EncryptionPercentage
        }
    })
} catch {
    $ergebnis.verschluesselung = $null
}

# Update-Verlauf ueber die Windows-Update-API: Get-HotFix kennt nur einen
# Teil und oft kein Datum. Operation 1 = Installation.
try {
    $sucher = (New-Object -ComObject Microsoft.Update.Session).CreateUpdateSearcher()
    $anzahl = $sucher.GetTotalHistoryCount()
    if ($anzahl -gt 0) {
        $ergebnis.updates = @($sucher.QueryHistory(0, [Math]::Min($anzahl, 40)) |
            Where-Object { $_.Operation -eq 1 -and $_.Title } |
            Select-Object -First 10 |
            ForEach-Object {
                [ordered]@{
                    titel    = [string]$_.Title
                    datum    = $_.Date.ToUniversalTime().ToString('o')
                    ergebnis = [int]$_.ResultCode
                }
            })
    }
} catch {
    $ergebnis.updates_fehler = $_.Exception.Message
}

# Secure Boot: wichtig fuer "Netzstart einmal" - unser Netzstart ist nicht
# signiert (os-stationen/sicherung/README.md).
try {
    $ergebnis.secure_boot = [bool](Confirm-SecureBootUEFI)
} catch {
    $ergebnis.secure_boot = $null
}

$ergebnis | ConvertTo-Json -Depth 4 -Compress
