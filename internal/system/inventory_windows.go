//go:build windows

package system

import (
	"context"
	"encoding/json"
	"runtime"
	"time"
)

// inventoryScript gathers everything in one PowerShell run and prints JSON.
const inventoryScript = `
$ErrorActionPreference = 'SilentlyContinue'
$cs = Get-CimInstance Win32_ComputerSystem
$bios = Get-CimInstance Win32_BIOS
$os = Get-CimInstance Win32_OperatingSystem
$product = Get-CimInstance Win32_ComputerSystemProduct
$bitlocker = $null
try { $bitlocker = ((Get-BitLockerVolume -MountPoint $env:SystemDrive).ProtectionStatus -eq 'On') } catch {}
$firewall = $null
try { $firewall = -not (Get-NetFirewallProfile | Where-Object { -not $_.Enabled }) } catch {}
$defender = $null
try { $mp = Get-MpComputerStatus; $defender = ($mp.AntivirusEnabled -and $mp.RealTimeProtectionEnabled) } catch {}
$keys = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*'
$software = Get-ItemProperty $keys | Where-Object { $_.DisplayName -and -not $_.SystemComponent } |
  Sort-Object DisplayName -Unique | ForEach-Object { [pscustomobject]@{ name=$_.DisplayName; version=$_.DisplayVersion; publisher=$_.Publisher; id=$_.PSChildName; size_mb=[math]::Round(($_.EstimatedSize/1024),1) } }
$services = Get-Service | Select-Object -First 400 | ForEach-Object { [pscustomobject]@{ name=$_.Name; display=$_.DisplayName; running=($_.Status -eq 'Running') } }
[pscustomobject]@{
  hardware = [pscustomobject]@{ manufacturer=$cs.Manufacturer; model=$cs.Model; model_name=$cs.Model; serial=$bios.SerialNumber;
    memory_gb=[math]::Round($cs.TotalPhysicalMemory/1GB,1); uuid=$product.UUID }
  os = [pscustomobject]@{ name=$os.Caption; version=$os.Version; build=$os.BuildNumber }
  users = @($cs.UserName) | Where-Object { $_ }
  security = [pscustomobject]@{ encrypted=$bitlocker; firewall=$firewall; antivirus=$defender }
  software = @($software)
  services = @($services)
} | ConvertTo-Json -Depth 4 -Compress
`

func powershellJSON(ctx context.Context, timeout time.Duration, script string, out any) error {
	text := run(ctx, timeout, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	return json.Unmarshal([]byte(text), out)
}

// Who reads what the server needs to know at enrollment.
func Who(ctx context.Context) Identity {
	var info struct {
		Manufacturer string `json:"manufacturer"`
		Model        string `json:"model"`
		Serial       string `json:"serial"`
		UUID         string `json:"uuid"`
		Version      string `json:"version"`
	}
	_ = powershellJSON(ctx, time.Minute, `$cs=Get-CimInstance Win32_ComputerSystem; $b=Get-CimInstance Win32_BIOS; $p=Get-CimInstance Win32_ComputerSystemProduct; $o=Get-CimInstance Win32_OperatingSystem;
[pscustomobject]@{manufacturer=$cs.Manufacturer; model=$cs.Model; serial=$b.SerialNumber; uuid=$p.UUID; version=$o.Version} | ConvertTo-Json -Compress`, &info)
	return Identity{Hostname: hostname(), OS: "windows", OSVersion: info.Version, Arch: runtime.GOARCH, Serial: info.Serial,
		MachineID: info.UUID, Manufacturer: info.Manufacturer, Model: info.Model}
}

// Inventory reads hardware, Windows, security state, services and every program.
func Inventory(ctx context.Context) map[string]any {
	var data map[string]any
	_ = powershellJSON(ctx, 3*time.Minute, inventoryScript, &data)
	if data == nil {
		data = map[string]any{}
	}
	data["hostname"] = hostname()
	data["disks"] = Disks()
	data["network"] = Network()
	return data
}
