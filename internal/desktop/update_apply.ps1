param([Parameter(Mandatory = $true)][string]$PlanPath)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2
$plan = $null
$managerProcess = $null
$startedProcess = $null
$trustedPlan = $false
$committed = $false
$parentExited = $false
$keepStaging = $false
$succeeded = $false
$resultMessage = ''
$resultWarning = ''
$resultWriteError = ''
$backupForRecovery = ''
$restartPrevious = $false

function Same-Path([string]$first, [string]$second) {
    return [string]::Equals([IO.Path]::GetFullPath($first), [IO.Path]::GetFullPath($second), [StringComparison]::OrdinalIgnoreCase)
}

function Assert-PlainPath([string]$path) {
    $currentPath = [IO.Path]::GetFullPath($path)
    while ($true) {
        $attributes = [IO.File]::GetAttributes($currentPath)
        if (($attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
            throw "Update path contains a reparse point: $currentPath"
        }
        $nextPath = [IO.Path]::GetDirectoryName($currentPath)
        if ([string]::IsNullOrEmpty($nextPath) -or (Same-Path $currentPath $nextPath)) { break }
        $currentPath = $nextPath
    }
}

function Get-FileDigest([string]$path) {
    $stream = [IO.File]::OpenRead($path)
    $digest = [Security.Cryptography.SHA256]::Create()
    try {
        return [BitConverter]::ToString($digest.ComputeHash($stream)).Replace('-', '').ToLowerInvariant()
    } finally {
        $digest.Dispose()
        $stream.Dispose()
    }
}

function Test-SharingViolation($errorRecord) {
    $current = $errorRecord.Exception
    while ($null -ne $current) {
        if ($current.HResult -eq -2147024864) { return $true }
        $current = $current.InnerException
    }
    return $false
}

function Wait-ImageReleased([string]$path) {
    $name = [IO.Path]::GetFileNameWithoutExtension($path)
    $wait = [Diagnostics.Stopwatch]::StartNew()
    while ($wait.ElapsedMilliseconds -lt 5000) {
        $busy = $false
        foreach ($process in [Diagnostics.Process]::GetProcessesByName($name)) {
            try {
                if (Same-Path $process.MainModule.FileName $path) { $busy = $true }
            } catch { }
            finally { $process.Dispose() }
        }
        if (-not $busy) { return }
        Start-Sleep -Milliseconds 200
    }
}

function Replace-UpdateFile([string]$source, [string]$destination, [string]$backup) {
    $wait = [Diagnostics.Stopwatch]::StartNew()
    while ($true) {
        try {
            [IO.File]::Replace($source, $destination, $backup)
            return
        } catch {
            if (-not (Test-SharingViolation $_) -or $wait.ElapsedMilliseconds -ge 30000) { throw }
            Start-Sleep -Milliseconds 250
        }
    }
}

function Write-NewText([string]$path, [string]$value) {
    # The parent retries reads until this handle closes and the token is complete.
    $stream = [IO.File]::Open($path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $content = [Text.Encoding]::UTF8.GetBytes($value)
        $stream.Write($content, 0, $content.Length)
        $stream.Flush()
    } finally { $stream.Dispose() }
}

function Assert-Ownership {
    Assert-PlainPath $plan.directory
    $ownerFile = [IO.Path]::Combine($plan.directory, '.update-owner')
    Assert-PlainPath $ownerFile
    if ([IO.File]::ReadAllText($ownerFile, [Text.Encoding]::UTF8) -cne $plan.token) {
        throw 'The update staging directory ownership changed.'
    }
}

function Assert-NotCancelled {
    $abortPath = [IO.Path]::Combine($plan.directory, 'abort')
    if ([IO.File]::Exists($abortPath)) {
        Assert-PlainPath $abortPath
        if ([IO.File]::ReadAllText($abortPath, [Text.Encoding]::UTF8) -ceq $plan.token) {
            throw 'The update was cancelled before application replacement.'
        }
    }
}

function Write-UpdateResult([bool]$success, [string]$message, [string]$backupPath) {
    Assert-PlainPath $plan.configDir
    $resultPath = [IO.Path]::Combine($plan.configDir, 'update-result.json')
    $temporaryResult = [IO.Path]::Combine($plan.configDir, ('.update-result-' + $plan.token + '.tmp'))
    if ([IO.File]::Exists($resultPath)) { Assert-PlainPath $resultPath }
    $stagingPath = ''
    if ($keepStaging) { $stagingPath = $plan.directory }
    $result = [pscustomobject]@{
        version = $plan.version
        success = $success
        message = $message
        backupPath = $backupPath
        warning = $resultWarning
        stagingPath = $stagingPath
        completedAt = [DateTime]::UtcNow.ToString('o')
    }
    try {
        $stream = [IO.File]::Open($temporaryResult, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write)
        try {
            $content = [Text.Encoding]::UTF8.GetBytes(($result | ConvertTo-Json -Compress))
            $stream.Write($content, 0, $content.Length)
            $stream.Flush()
        } finally { $stream.Dispose() }
        if ([IO.File]::Exists($resultPath)) {
            # Preserve a null .NET string; PowerShell coerces $null to "".
            [IO.File]::Replace($temporaryResult, $resultPath, [NullString]::Value)
        } else {
            [IO.File]::Move($temporaryResult, $resultPath)
        }
    } finally {
        if ([IO.File]::Exists($temporaryResult)) { [IO.File]::Delete($temporaryResult) }
    }
}

function Start-Application {
    $startInfo = New-Object Diagnostics.ProcessStartInfo
    $startInfo.FileName = $plan.target
    $startInfo.Arguments = $plan.restartArgs
    $startInfo.WorkingDirectory = [IO.Path]::GetDirectoryName($plan.target)
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $process = [Diagnostics.Process]::Start($startInfo)
    if ($null -eq $process) { throw 'The application could not be started.' }
    return $process
}

try {
    $plan = [IO.File]::ReadAllText($PlanPath, [Text.Encoding]::UTF8) | ConvertFrom-Json
    foreach ($path in @($plan.directory, $plan.target, $plan.source, $plan.backup, $plan.configDir)) {
        if (-not [IO.Path]::IsPathRooted($path) -or $path.Contains('"')) {
            throw 'The update plan contains an invalid absolute path.'
        }
    }
    if (-not (Same-Path ([IO.Path]::GetDirectoryName($plan.directory)) ([IO.Path]::GetDirectoryName($plan.target))) -or
        -not (Same-Path ([IO.Path]::GetDirectoryName($plan.source)) $plan.directory) -or
        -not (Same-Path $plan.backup ([IO.Path]::Combine($plan.directory, 'previous.exe'))) -or
        -not (Same-Path $PlanPath ([IO.Path]::Combine($plan.directory, 'plan.json'))) -or
        -not (Same-Path $MyInvocation.MyCommand.Path ([IO.Path]::Combine($plan.directory, 'apply-update.ps1'))) -or
        (Same-Path $plan.source $plan.backup)) {
        throw 'The update files are not in the designated staging directory.'
    }
    if ($plan.token -notmatch '^[A-Za-z0-9]{16,128}$' -or $plan.sha256 -notmatch '^[0-9a-fA-F]{64}$' -or
        $plan.previousHash -notmatch '^[0-9a-fA-F]{64}$') {
        throw 'The update plan has invalid ownership or integrity information.'
    }
    Assert-Ownership
    foreach ($path in @($plan.target, $plan.source, $plan.configDir)) { Assert-PlainPath $path }
    if (-not [IO.File]::Exists($plan.target) -or -not [IO.File]::Exists($plan.source) -or
        -not [IO.Directory]::Exists($plan.configDir) -or [IO.File]::Exists($plan.backup)) {
        throw 'An update input is unavailable, or a previous backup already exists.'
    }
    $trustedPlan = $true
    if ((Get-FileDigest $plan.source) -ne $plan.sha256 -or (Get-FileDigest $plan.target) -ne $plan.previousHash) {
        throw 'The update input changed after verification.'
    }
    $managerProcess = [Diagnostics.Process]::GetProcessById([int]$plan.parentPid)
    $null = $managerProcess.Handle
    if ($managerProcess.StartTime.ToUniversalTime().ToFileTimeUtc().ToString() -cne $plan.parentCreated -or
        -not (Same-Path $managerProcess.MainModule.FileName $plan.target)) {
        throw 'The application process identity changed.'
    }
    Write-NewText ([IO.Path]::Combine($plan.directory, 'ready')) $plan.token
    $commitPath = [IO.Path]::Combine($plan.directory, 'commit')
    $commitWait = [Diagnostics.Stopwatch]::StartNew()
    while (-not [IO.File]::Exists($commitPath)) {
        Assert-NotCancelled
        if ($commitWait.ElapsedMilliseconds -gt 15000 -or $managerProcess.HasExited) {
            throw 'The application did not commit the update.'
        }
        Start-Sleep -Milliseconds 50
    }
    Assert-PlainPath $commitPath
    if ([IO.File]::ReadAllText($commitPath, [Text.Encoding]::UTF8) -cne $plan.token) {
        throw 'The update commit credential does not match.'
    }
    $committed = $true
    $exitWait = [Diagnostics.Stopwatch]::StartNew()
    while (-not $managerProcess.WaitForExit(100)) {
        Assert-NotCancelled
        if ($exitWait.ElapsedMilliseconds -gt 120000) { throw 'The application did not exit before the update timeout.' }
    }
    $parentExited = $true
    Assert-NotCancelled
    Assert-Ownership
    Wait-ImageReleased $plan.target
    foreach ($path in @($plan.source, $plan.target)) { Assert-PlainPath $path }
    if ((Get-FileDigest $plan.source) -ne $plan.sha256 -or (Get-FileDigest $plan.target) -ne $plan.previousHash) {
        throw 'An update file changed before replacement.'
    }
    Replace-UpdateFile $plan.source $plan.target $plan.backup
    if ((Get-FileDigest $plan.target) -ne $plan.sha256) { throw 'The replaced executable failed its integrity check.' }
    $startedProcess = Start-Application
    if ($startedProcess.WaitForExit(2000)) { throw 'The new application exited immediately after launch.' }
    $succeeded = $true
    $resultMessage = 'The update was installed and the application restarted.'
    [IO.File]::Delete($plan.backup)
}
catch {
    $resultMessage = $_.Exception.Message
    if ($succeeded) {
        # The replacement is already running. Retain the backup if cleanup failed.
        $keepStaging = $true
        $backupForRecovery = $plan.backup
        $resultWarning = 'The previous-version backup could not be removed. ' + $resultMessage
        $resultMessage = 'The update was installed and the application restarted.'
    } elseif ($trustedPlan -and $committed -and $parentExited) {
        try {
            if ($null -ne $startedProcess -and -not $startedProcess.HasExited) {
                throw 'The new process is still running; the backup was retained for manual recovery.'
            }
            Assert-Ownership
            if ([IO.File]::Exists($plan.backup)) {
                Assert-PlainPath $plan.backup
                if ((Get-FileDigest $plan.backup) -ne $plan.previousHash) { throw 'The previous-version backup failed its integrity check.' }
                if ([IO.File]::Exists($plan.target)) {
                    Assert-PlainPath $plan.target
                    Replace-UpdateFile $plan.backup $plan.target ([IO.Path]::Combine($plan.directory, 'rejected.exe'))
                } else {
                    [IO.File]::Move($plan.backup, $plan.target)
                }
            }
            Assert-PlainPath $plan.target
            if ((Get-FileDigest $plan.target) -ne $plan.previousHash) { throw 'The previous executable could not be restored.' }
            $restartPrevious = $true
            $resultMessage += ' The previous executable was restored.'
        } catch {
            $keepStaging = $true
            $backupForRecovery = $plan.backup
            $resultMessage += ' Recovery failed: ' + $_.Exception.Message
        }
    }
}
finally {
    if ($null -ne $managerProcess) { $managerProcess.Dispose() }
    if ($null -ne $startedProcess) { $startedProcess.Dispose() }
    if ($trustedPlan) {
        try { Write-UpdateResult $succeeded $resultMessage $backupForRecovery }
        catch { $keepStaging = $true; $resultWriteError = $_.Exception.Message }
        if ($restartPrevious) {
            try {
                $restarted = Start-Application
                $restarted.Dispose()
            } catch {
                $resultMessage += ' Restart the previous application manually: ' + $_.Exception.Message
                try { Write-UpdateResult $false $resultMessage $backupForRecovery }
                catch { $keepStaging = $true; $resultWriteError = $_.Exception.Message }
            }
        }
        if (-not $keepStaging) {
            try {
                Assert-Ownership
                if ([IO.Directory]::GetDirectories($plan.directory).Length -ne 0) {
                    throw 'An unexpected child directory appeared in the update staging directory.'
                }
                $allowedNames = @('.update-owner', 'apply-update.ps1', 'plan.json', 'ready', 'commit', 'commit.pending', 'abort', 'previous.exe', 'rejected.exe', [IO.Path]::GetFileName($plan.source))
                $files = [IO.Directory]::GetFiles($plan.directory)
                foreach ($file in $files) {
                    Assert-PlainPath $file
                    if ($allowedNames -notcontains [IO.Path]::GetFileName($file)) {
                        throw 'An unexpected file appeared in the update staging directory.'
                    }
                }
                foreach ($file in $files) { [IO.File]::Delete($file) }
                [IO.Directory]::Delete($plan.directory, $false)
            } catch {
                $keepStaging = $true
                $resultWarning = 'Staging cleanup failed at ' + $plan.directory + ': ' + $_.Exception.Message
                try { Write-UpdateResult $succeeded $resultMessage $backupForRecovery }
                catch { $resultWriteError = $_.Exception.Message }
            }
        }
    }
}

# All process handles and safe cleanup work are finished before this modal
# fallback. It is used only when the application cannot read a saved result.
if ($resultWriteError -ne '') {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class PHPToolsUpdateNotice {
    [DllImport("user32.dll", CharSet = CharSet.Unicode, ExactSpelling = true)]
    public static extern int MessageBoxW(IntPtr owner, string text, string title, uint flags);
}
'@
    $notice = "PHPTools could not save its update result.`r`n" + $resultWriteError + "`r`n`r`n" + $resultMessage
    if ($resultWarning -ne '') { $notice += "`r`n" + $resultWarning }
    $notice += "`r`n`r`nRetained update directory: " + $plan.directory
    if ($backupForRecovery -ne '') { $notice += "`r`nPrevious-version backup: " + $backupForRecovery }
    $null = [PHPToolsUpdateNotice]::MessageBoxW([IntPtr]::Zero, $notice, 'PHPTools update needs attention', 0x1030)
}

if (-not $succeeded) { exit 1 }
