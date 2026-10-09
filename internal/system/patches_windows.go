//go:build windows

package system

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Windows Update's own COM API, which every Windows edition has.
const scanScript = `
$ErrorActionPreference = 'Stop'
$searcher = (New-Object -ComObject Microsoft.Update.Session).CreateUpdateSearcher()
$found = $searcher.Search("IsInstalled=0 and IsHidden=0 and Type='Software'")
@($found.Updates | ForEach-Object {
  [pscustomobject]@{ id=$_.Identity.UpdateID; title=$_.Title; msrc=$_.MsrcSeverity;
    category=(@($_.Categories) | Select-Object -First 1).Name; kb=(@($_.KBArticleIDs) -join ',');
    size_mb=[math]::Round($_.MaxDownloadSize/1MB,1); reboot=($_.InstallationBehavior.RebootBehavior -ne 0) }
}) | ConvertTo-Json -Depth 3 -Compress
`

const installScript = `
param([string]$Ids)
$ErrorActionPreference = 'Stop'
$wanted = @($Ids -split ',' | Where-Object { $_ })
$session = New-Object -ComObject Microsoft.Update.Session
$found = $session.CreateUpdateSearcher().Search("IsInstalled=0 and IsHidden=0 and Type='Software'")
$batch = New-Object -ComObject Microsoft.Update.UpdateColl
foreach ($u in $found.Updates) {
  if ($wanted.Count -eq 0 -or $wanted -contains $u.Identity.UpdateID) { if (-not $u.EulaAccepted) { $u.AcceptEula() }; [void]$batch.Add($u) }
}
$results = @()
if ($batch.Count -gt 0) {
  $downloader = $session.CreateUpdateDownloader(); $downloader.Updates = $batch; [void]$downloader.Download()
  $installer = $session.CreateUpdateInstaller(); $installer.Updates = $batch; $outcome = $installer.Install()
  for ($i = 0; $i -lt $batch.Count; $i++) {
    $code = $outcome.GetUpdateResult($i).ResultCode
    $results += [pscustomobject]@{ id=$batch.Item($i).Identity.UpdateID; ok=($code -eq 2 -or $code -eq 3); error=("Result code " + $code) }
  }
  [pscustomobject]@{ results=$results; reboot=$outcome.RebootRequired } | ConvertTo-Json -Depth 3 -Compress
} else { [pscustomobject]@{ results=@(); reboot=$false } | ConvertTo-Json -Compress }
`

// ScanUpdates lists what Windows Update offers.
func ScanUpdates(ctx context.Context) ([]Update, error) {
	var raw []struct {
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		MSRC     string  `json:"msrc"`
		Category string  `json:"category"`
		KB       string  `json:"kb"`
		SizeMB   float64 `json:"size_mb"`
		Reboot   bool    `json:"reboot"`
	}
	text := run(ctx, 15*time.Minute, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", scanScript)
	if strings.HasPrefix(text, "{") {
		text = "[" + text + "]"
	}
	if text != "" {
		if err := json.Unmarshal([]byte(text), &raw); err != nil {
			return nil, err
		}
	}
	updates := make([]Update, 0, len(raw))
	for _, u := range raw {
		kb := ""
		if u.KB != "" {
			kb = "KB" + strings.Split(u.KB, ",")[0]
		}
		updates = append(updates, Update{ID: u.ID, Title: u.Title, Severity: Severity(u.MSRC, u.Category), Category: u.Category,
			KB: kb, SizeMB: u.SizeMB, RebootRequired: u.Reboot})
	}
	return updates, nil
}

// InstallUpdates downloads and installs the named updates (all when ids is empty).
func InstallUpdates(ctx context.Context, ids []string) ([]map[string]any, string, error) {
	script := "& {" + installScript + "} -Ids '" + strings.Join(ids, ",") + "'"
	text := run(ctx, 2*time.Hour, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	var outcome struct {
		Results []map[string]any `json:"results"`
		Reboot  bool             `json:"reboot"`
	}
	if err := json.Unmarshal([]byte(text), &outcome); err != nil {
		return nil, text, err
	}
	return outcome.Results, text, nil
}
