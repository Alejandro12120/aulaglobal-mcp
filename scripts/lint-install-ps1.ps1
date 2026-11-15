# Comprueba install.ps1: se analiza sin errores, es solo ASCII y (con -Analyze)
# PSScriptAnalyzer no da avisos. Se usa en CI (windows-latest) y en
# scripts/test-install.sh. Con "powershell" (5.1) solo tiene sentido sin
# -Analyze: es la comprobacion de que el analizador de 5.1 acepta el script.
param([switch]$Analyze)
$ErrorActionPreference = 'Stop'

$path = Join-Path (Split-Path -Parent $PSScriptRoot) 'install.ps1'
$failed = $false

$tokens = $null
$errors = $null
[void][System.Management.Automation.Language.Parser]::ParseFile($path, [ref]$tokens, [ref]$errors)
foreach ($e in $errors) {
  Write-Host ('install.ps1:{0}: {1}' -f $e.Extent.StartLineNumber, $e.Message)
  $failed = $true
}

# Windows PowerShell 5.1 puede decodificar mal el texto no ASCII de "irm | iex".
$line = 0
foreach ($l in [System.IO.File]::ReadAllLines($path)) {
  $line++
  if ($l -match '[^\x00-\x7F]') {
    Write-Host ('install.ps1:{0}: caracteres no ASCII' -f $line)
    $failed = $true
  }
}

if ($Analyze) {
  $ProgressPreference = 'SilentlyContinue'
  Install-Module PSScriptAnalyzer -RequiredVersion 1.24.0 -Force -Scope CurrentUser -Repository PSGallery
  Import-Module PSScriptAnalyzer -RequiredVersion 1.24.0
  # Write-Host es intencionado: es un instalador interactivo con salida para personas.
  $findings = @(Invoke-ScriptAnalyzer -Path $path -Severity Warning, Error -ExcludeRule PSAvoidUsingWriteHost)
  foreach ($f in $findings) {
    Write-Host ('install.ps1:{0}: {1}: {2}' -f $f.Line, $f.RuleName, $f.Message)
    $failed = $true
  }
}

if ($failed) { exit 1 }
Write-Host ('install.ps1 OK (PowerShell {0}{1})' -f $PSVersionTable.PSVersion, $(if ($Analyze) { ', PSScriptAnalyzer' } else { '' }))
