<#
install.ps1 — the Windows counterpart of install.sh.

  .\install.ps1                 build topic.exe, install it, link the plugin, register the Dispatcher task
  .\install.ps1 -Force          replace an existing non-link plugin directory (backed up to *.bak)
  .\install.ps1 -Uninstall      remove the binary, the plugin link and the task
  .\install.ps1 -DispatcherName "Dispatcher (windows)"
                                name this machine's Dispatcher topic (default: topic's own choice)

Honours CLAUDE_CONFIG_DIR exactly as install.sh does, so a second profile installs alongside:
  $env:CLAUDE_CONFIG_DIR = "$HOME\.claude-work"; .\install.ps1

Differences from install.sh, and why:
- topic.exe is COPIED to ~\.local\bin, not symlinked: symlinks need Developer Mode or
  elevation on Windows. Re-run this after changing anything under cmd\.
- The plugin is a directory JUNCTION back into the repo, which needs neither, so editing a
  skill still takes effect immediately.
- The Dispatcher is a Task Scheduler task (windows\claude-dispatcher.xml.template), not a
  launchd agent. Requires MSYS2's tmux (C:\msys64\usr\bin\tmux.exe, or tmux on PATH).
#>
param(
    [switch]$Force,
    [switch]$Uninstall,
    [string]$DispatcherName = ''
)
$ErrorActionPreference = 'Stop'

$Repo = $PSScriptRoot
$HomeDir = $env:USERPROFILE
$Bin = Join-Path $HomeDir '.local\bin'
$DefaultDir = Join-Path $HomeDir '.claude'

# Install into whichever profile is active, exactly as `claude` and `topic` resolve it.
$ClaudeDir = if ($env:CLAUDE_CONFIG_DIR) { $env:CLAUDE_CONFIG_DIR.TrimEnd('\', '/') } else { $DefaultDir }
$IsDefault = ($ClaudeDir -ieq $DefaultDir)
$PluginDest = Join-Path $ClaudeDir 'skills\topics'

# The task name and log must differ per profile, or installing a second profile would
# silently replace the first profile's Dispatcher task.
$Suffix = ''
if (-not $IsDefault) {
    $Suffix = '-' + ((Split-Path $ClaudeDir -Leaf) -replace '^\.', '' -replace '^claude-', '')
}
$TaskPath = '\claude-topics\'
$TaskName = "claude-dispatcher$Suffix"
$StateDir = Join-Path $env:LOCALAPPDATA 'claude-topics'
$RenderedXml = Join-Path $StateDir "tasks\$TaskName.xml"
$LogPath = Join-Path $StateDir "$TaskName.log"

function Say([string]$msg) { Write-Host "  $msg" }

function Test-Junction([string]$path) {
    $item = Get-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue
    return $item -and ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)
}

# Remove a junction WITHOUT touching its target. Remove-Item -Recurse on a junction can
# delete the target's contents on Windows PowerShell 5.1; rmdir removes only the link.
function Remove-Junction([string]$path) { cmd /c rmdir "$path" | Out-Null }

if ($Uninstall) {
    Write-Host 'Uninstalling...'
    if (Get-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -ErrorAction SilentlyContinue) {
        Unregister-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -Confirm:$false
        Say "removed task $TaskPath$TaskName"
    }
    if (Test-Path $RenderedXml) { Remove-Item $RenderedXml; Say "removed $RenderedXml" }
    if ($IsDefault -and (Test-Path (Join-Path $Bin 'topic.exe'))) {
        Remove-Item (Join-Path $Bin 'topic.exe'); Say "removed $Bin\topic.exe"
    }
    if (Test-Junction $PluginDest) { Remove-Junction $PluginDest; Say "removed $PluginDest" }
    Write-Host ''
    Write-Host 'Left in place (deliberately - this is your data, not the tool):'
    Say "$ClaudeDir\topics\   topic registry, handoff docs, and briefs"
    exit 0
}

# ---- build ---------------------------------------------------------------------------
$go = (Get-Command go -ErrorAction SilentlyContinue).Source
if (-not $go -and (Test-Path 'C:\Program Files\Go\bin\go.exe')) { $go = 'C:\Program Files\Go\bin\go.exe' }
if (-not $go) { throw 'go not found on PATH - needed to build bin\topic.exe (winget install GoLang.Go)' }
Push-Location $Repo
try {
    & $go build -o bin\topic.exe ./cmd/topic
    if ($LASTEXITCODE -ne 0) { throw "go build failed (exit $LASTEXITCODE)" }
} finally { Pop-Location }
Say 'built bin\topic.exe'

$tmux = (Get-Command tmux -ErrorAction SilentlyContinue).Source
if (-not $tmux -and (Test-Path 'C:\msys64\usr\bin\tmux.exe')) { $tmux = 'C:\msys64\usr\bin\tmux.exe' }
if (-not $tmux) {
    Write-Warning 'MSYS2 tmux not found. Install MSYS2, then: pacman -S tmux  (topic up will fail without it)'
}

Write-Host "Installing from $Repo"
Say "profile: $ClaudeDir"

# ---- binary ----------------------------------------------------------------------------
New-Item -ItemType Directory -Force $Bin | Out-Null
Copy-Item (Join-Path $Repo 'bin\topic.exe') (Join-Path $Bin 'topic.exe') -Force
Say "copied $Bin\topic.exe"

# ---- plugin ----------------------------------------------------------------------------
$pluginSrc = Join-Path $Repo 'plugin'
if (Test-Path -LiteralPath $PluginDest) {
    if (Test-Junction $PluginDest) {
        $target = (Get-Item -LiteralPath $PluginDest -Force).Target
        if ($target -and ($target -ieq $pluginSrc -or $target -contains $pluginSrc)) {
            Say "ok (already linked): $PluginDest"
        } else {
            Remove-Junction $PluginDest
        }
    } elseif (-not $Force) {
        throw "refusing to overwrite real directory: $PluginDest (use -Force)"
    } else {
        Rename-Item -LiteralPath $PluginDest "$PluginDest.bak"
        Say "backed up -> $PluginDest.bak"
    }
}
if (-not (Test-Path -LiteralPath $PluginDest)) {
    New-Item -ItemType Directory -Force (Split-Path $PluginDest) | Out-Null
    cmd /c mklink /J "$PluginDest" "$pluginSrc" | Out-Null
    Say "linked $PluginDest -> $pluginSrc"
}

# ---- Dispatcher task ---------------------------------------------------------------------
New-Item -ItemType Directory -Force (Split-Path $RenderedXml) | Out-Null

# The default profile must run with CLAUDE_CONFIG_DIR UNSET, not set to ~\.claude: the two
# select different config files and only the unset one has completed onboarding.
#
# conhost --headless keeps a console window from flashing up every five minutes, but it
# mangles QUOTED arguments on their way to cmd.exe: `set "X=a b"&& ...` runs nothing at
# all, silently. So the command line is built without quotes — `set X=a b&& ...` is
# still exact, provided nothing precedes && — and paths containing spaces are refused
# rather than quoted into a task that would do nothing.
$topicExe = Join-Path $Bin 'topic.exe'
foreach ($p in @($topicExe, $LogPath) + @(if (-not $IsDefault) { $ClaudeDir })) {
    if ($p -match '\s') { throw "path contains a space, which the headless Dispatcher task cannot quote: $p" }
}
if ($DispatcherName -match '[&|<>^"%]') { throw "Dispatcher name may not contain & | < > ^ `" or %: $DispatcherName" }
$sets = ''
if (-not $IsDefault) { $sets += "set CLAUDE_CONFIG_DIR=$ClaudeDir&& " }
if ($DispatcherName) { $sets += "set CLAUDE_DISPATCHER_NAME=$DispatcherName&& " }
$arguments = "--headless cmd.exe /d /c $sets$topicExe ensure-dispatcher >> $LogPath 2>&1"

$esc = { param($s) [Security.SecurityElement]::Escape($s) }
$user = "$env:USERDOMAIN\$env:USERNAME"
$xml = (Get-Content -Raw (Join-Path $Repo 'windows\claude-dispatcher.xml.template')) `
    -replace '__USER__', (& $esc $user) `
    -replace '__CONFIG_DIR__', (& $esc $ClaudeDir) `
    -replace '__COMMAND__', (& $esc 'C:\Windows\System32\conhost.exe') `
    -replace '__ARGUMENTS__', ((& $esc $arguments) -replace '\$', '$$$$') `
    -replace '__HOME__', (& $esc $HomeDir)
[IO.File]::WriteAllText($RenderedXml, $xml, (New-Object Text.UTF8Encoding $false))
Say "rendered $RenderedXml"

# -Xml takes a .NET (UTF-16) string, and an encoding="UTF-8" declaration makes Task
# Scheduler reject it ("unable to switch the encoding"), so register without it. The copy
# on disk keeps its declaration, since that one really is UTF-8.
Register-ScheduledTask -TaskPath $TaskPath -TaskName $TaskName -Xml ($xml -replace '^\s*<\?xml[^>]*\?>\s*', '') -Force | Out-Null
Say "registered task $TaskPath$TaskName (logon + every 5 min; log: $LogPath)"
if ($IsDefault) { Say 'default profile: CLAUDE_CONFIG_DIR deliberately left unset in the task' }

Write-Host ''
Write-Host 'Done. Check with:'
Say 'topic --help'
Say 'topic list'
Say "Start-ScheduledTask -TaskPath '$TaskPath' -TaskName '$TaskName'   # start the Dispatcher now"
Write-Host ''
if (-not (($env:Path -split ';') -contains $Bin)) {
    Write-Host "NOTE: $Bin is not on your PATH - add it to your user PATH."
}
