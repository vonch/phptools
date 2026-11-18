param(
    [switch]$SkipTests,
    [string]$UpdateDownloadURL = 'https://phptools.wuyouzhilian.cn/PHPTools.exe',
    [string]$UpdateNotes = ''
)

$ErrorActionPreference = 'Stop'
$projectDir = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$previousToolchain = $env:GOTOOLCHAIN
$versionSource = Get-Content -LiteralPath (Join-Path $projectDir 'internal/desktop/app.go') -Raw
$versionMatch = [regex]::Match($versionSource, '(?m)^const appVersion = "([0-9]+\.[0-9]+\.[0-9]+)"\s*$')
if (-not $versionMatch.Success) { throw 'Could not read the release version from internal/desktop/app.go.' }
$releaseVersion = $versionMatch.Groups[1].Value
$releaseDirectory = "dist/PHPTools-$releaseVersion"
$UpdateDownloadURL = $UpdateDownloadURL.Trim()
if ($UpdateDownloadURL) {
    $updateUri = $null
    if (-not [Uri]::TryCreate($UpdateDownloadURL, [UriKind]::Absolute, [ref]$updateUri) -or
        $updateUri.Scheme -ne 'https' -or -not $updateUri.Host -or $updateUri.UserInfo -or $updateUri.Fragment -or
        -not $updateUri.AbsolutePath.EndsWith('.exe', [StringComparison]::OrdinalIgnoreCase)) {
        throw 'UpdateDownloadURL must be the HTTPS URL of the published PHPTools.exe, without credentials or a fragment. ZIP packages cannot be used for in-app upgrades.'
    }
}
Push-Location $projectDir
try {
    $env:GOTOOLCHAIN = 'go1.26.9'
    @'
const fs = require("node:fs");
const pkg = JSON.parse(fs.readFileSync("frontend/package.json", "utf8"));
const lock = JSON.parse(fs.readFileSync("frontend/package-lock.json", "utf8"));
if ([pkg.version, lock.version, lock.packages[""].version].some(v => v !== process.argv[2])) {
    throw new Error("Frontend versions must match appVersion");
}
'@ | node - $releaseVersion
    if ($LASTEXITCODE -ne 0) { throw 'Frontend package versions must match appVersion before packaging.' }
    # The binding generator type-checks the embedded assets on a fresh checkout.
    if (-not (Test-Path -LiteralPath 'frontend/dist/index.html')) {
        New-Item -ItemType Directory -Path 'frontend/dist' -Force | Out-Null
        Set-Content -LiteralPath 'frontend/dist/index.html' -Encoding utf8 -Value '<html><body>PHPTools frontend build in progress.</body></html>'
    }
    go mod download
    if ($LASTEXITCODE -ne 0) { throw 'Go dependencies failed.' }
    go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.32 generate bindings -ts -i
    if ($LASTEXITCODE -ne 0) { throw 'Binding generation failed.' }
    Push-Location frontend
    try {
        npm ci
        if ($LASTEXITCODE -ne 0) { throw 'Frontend dependencies failed.' }
        npm run build
        if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
        if (-not $SkipTests) {
            npm test
            if ($LASTEXITCODE -ne 0) { throw 'Frontend tests failed.' }
        }
    } finally { Pop-Location }
    if (-not $SkipTests) {
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
        go test -C third_party/wails ./internal/webview2/pkg/edge
        if ($LASTEXITCODE -ne 0) { throw 'WebView2 suspension tests failed.' }
    }
    New-Item -ItemType Directory -Path $releaseDirectory -Force | Out-Null
    go run ./scripts/embedicon
    if ($LASTEXITCODE -ne 0) { throw 'Windows icon resource failed.' }
    go build -trimpath -tags production -ldflags '-s -w -H windowsgui' -o "$releaseDirectory/PHPTools.exe" .
    if ($LASTEXITCODE -ne 0) { throw 'Executable build failed.' }
    go run ./scripts/embedicon -exe "$releaseDirectory/PHPTools.exe"
    if ($LASTEXITCODE -ne 0) { throw 'The executable has no Windows icon.' }
    Copy-Item -LiteralPath README.md,THIRD_PARTY_NOTICES.md,FIRST_RUN.md -Destination $releaseDirectory
    New-Item -ItemType Directory -Path "$releaseDirectory/licenses" -Force | Out-Null
    Copy-Item -LiteralPath third_party/wails/LICENSE -Destination "$releaseDirectory/licenses/Wails-LICENSE.txt"
    Compress-Archive -Path "$releaseDirectory/*" -DestinationPath "dist/PHPTools-$releaseVersion-windows-x64.zip" -Force
    if ($UpdateDownloadURL) {
        $executablePath = (Resolve-Path -LiteralPath "$releaseDirectory/PHPTools.exe").Path
        $updateManifest = [ordered]@{
            version = $releaseVersion
            downloadUrl = $UpdateDownloadURL
            sha256 = (Get-FileHash -LiteralPath $executablePath -Algorithm SHA256).Hash.ToLowerInvariant()
            size = (Get-Item -LiteralPath $executablePath).Length
            releaseDate = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
            notes = $UpdateNotes
        }
        $updateManifestPath = Join-Path $projectDir "dist/PHPTools-$releaseVersion-update.json"
        $publishedManifestPath = Join-Path $projectDir "dist/update.json"
        $manifestJSON = $updateManifest | ConvertTo-Json
        [IO.File]::WriteAllText($updateManifestPath, $manifestJSON, [Text.UTF8Encoding]::new($false))
        [IO.File]::WriteAllText($publishedManifestPath, $manifestJSON, [Text.UTF8Encoding]::new($false))
        Write-Output "Update manifest: $updateManifestPath"
        Write-Output "Publish as: https://phptools.wuyouzhilian.cn/update.json"
    }
    Get-FileHash -LiteralPath "dist/PHPTools-$releaseVersion-windows-x64.zip" -Algorithm SHA256 |
        Format-List Algorithm,Hash,Path
} finally {
    Pop-Location
    $env:GOTOOLCHAIN = $previousToolchain
}
