# Pruebas de install.ps1 bajo PowerShell 7 en Linux (contenedor desechable
# lanzado por scripts/test-install.sh; repositorio en /src de solo lectura,
# artefactos de inner.sh en /work). Windows PowerShell 5.1, el registro de
# Windows y ssh-keygen.exe NO se prueban aqui.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Src = '/src'
$W = '/work'
$Port = 8098
$Base = "http://127.0.0.1:$Port/releases"
$script:Pass = 0
$script:Fail = 0
$script:Cur = ''
$script:Out = ''
$script:Code = 0

function Check([string]$Desc, [bool]$Cond) {
  if ($Cond) { $script:Pass++; return }
  $script:Fail++
  Write-Host "FALLO [$($script:Cur)] $Desc"
  ($script:Out -split "`n" | Select-Object -Last 25) | ForEach-Object { Write-Host "    | $_" }
}
function Has([string]$Text) { return $script:Out.Contains($Text) }

# ---------------------------------------------------------------- estaticas

$script:Cur = 'estatico'
$ps1 = Join-Path $Src 'install.ps1'
$tokens = $null
$errs = $null
$ast = [System.Management.Automation.Language.Parser]::ParseFile($ps1, [ref]$tokens, [ref]$errs)
$script:Out = ($errs | ForEach-Object { $_.Message }) -join "`n"
Check 'install.ps1 se analiza sin errores' ($errs.Count -eq 0)
$bytes = [System.IO.File]::ReadAllBytes($ps1)
Check 'install.ps1 es solo ASCII' (@($bytes | Where-Object { $_ -gt 126 }).Count -eq 0)
$text = [System.IO.File]::ReadAllText($ps1)
Check 'sin exit en el script (no cierra la sesion del usuario)' (@($ast.FindAll({ param($n) $n -is [System.Management.Automation.Language.ExitStatementAst] }, $true)).Count -eq 0)
Check 'sin bloque param() de script (funciona con irm | iex)' ($null -eq $ast.ParamBlock)
Check 'sin opciones para saltarse la firma' ($text -notmatch '(?i)skip.?sig|insecure|AULAGLOBAL_[A-Z_]*KEY')

Write-Host '== Lint (scripts/lint-install-ps1.ps1 -Analyze: analizador + PSScriptAnalyzer 1.24.0)'
$lint = & (Get-Command pwsh).Source -NoProfile -File (Join-Path $Src 'scripts/lint-install-ps1.ps1') -Analyze 2>&1 | Out-String
$script:Out = $lint
Check 'lint-install-ps1.ps1 -Analyze sin avisos' ($LASTEXITCODE -eq 0)

# ---------------------------------------------------------------- funciones puras
# Se extraen del propio install.ps1 (sin ejecutarlo) y se definen aqui.

Write-Host '== Funciones puras (extraidas con el AST)'
$funcs = $ast.FindAll({ param($n) $n -is [System.Management.Automation.Language.FunctionDefinitionAst] }, $true)
foreach ($f in $funcs) {
  if ($f.Name -in @('T', 'Format-Line', 'Write-Ok', 'Write-Note', 'Write-Warn', 'ConvertTo-ReleaseTag', 'ConvertTo-PlatformLabel',
      'Get-ChecksumEntry', 'Test-OldSshKeygenOutput', 'Add-PathEntry', 'ConvertTo-QuotedArg', 'Resolve-InstallDir',
      'Confirm-Choice', 'Exit-Installer', 'Invoke-Native')) {
    Invoke-Expression $f.Extent.Text
  }
}
$script:Out = ''

$script:Cur = 'T'
Check 'convierte \u00f3' ((T 'Instalaci\u00f3n') -ceq ('Instalaci' + [char]0xF3 + 'n'))
Check 'convierte marcas' ((T '\u2713 \u2717 \u00b7') -ceq ([string][char]0x2713 + ' ' + [char]0x2717 + ' ' + [char]0xB7))
Check 'respeta texto sin escapes' ((T 'C:\Users\x') -ceq 'C:\Users\x')

$script:Cur = 'ConvertTo-ReleaseTag'
foreach ($c in @(@('v1.2.3', 'v1.2.3'), @('1.2.3', 'v1.2.3'), @(' v0.1.5 ', 'v0.1.5'), @("v1.2.3`n", 'v1.2.3'))) {
  Check "acepta '$($c[0])'" ((ConvertTo-ReleaseTag $c[0]) -ceq $c[1])
}
foreach ($bad in @('1.2', 'v1.2.3-rc1', 'v1.2.3.4', 'latest', '', 'v1.2.3;x', 'V1.2.3', 'v1234567890.0.0', ('v' + [char]0x0661 + '.0.0'))) {
  Check "rechaza '$bad'" ($null -eq (ConvertTo-ReleaseTag $bad))
}

$script:Cur = 'ConvertTo-PlatformLabel'
foreach ($c in @(@('AMD64', 'windows_x64'), @('X64', 'windows_x64'), @('x64', 'windows_x64'), @('ARM64', 'windows_arm64'), @('Arm64', 'windows_arm64'))) {
  Check "$($c[0])" ((ConvertTo-PlatformLabel $c[0]) -ceq $c[1])
}
foreach ($bad in @('X86', 'x86', 'Arm', 'IA64', '', 'AMD6')) {
  Check "rechaza '$bad'" ($null -eq (ConvertTo-PlatformLabel $bad))
}

$script:Cur = 'Get-ChecksumEntry'
$h1 = 'a' * 64
$h2 = 'b' * 64
$sums = "$h1  aulaglobal-mcp_1.0.0_windows_x64.zip`n$h2  aulaglobal-mcp_1.0.0_linux_x64.tar.gz`n"
Check 'encuentra una linea exacta' ((Get-ChecksumEntry $sums 'aulaglobal-mcp_1.0.0_windows_x64.zip').Count -eq 1)
Check 'devuelve el hash correcto' ((Get-ChecksumEntry $sums 'aulaglobal-mcp_1.0.0_linux_x64.tar.gz')[0] -ceq $h2)
Check 'no hace coincidencia parcial' ((Get-ChecksumEntry $sums 'windows_x64.zip').Count -eq 0)
Check 'no hace coincidencia por sufijo' ((Get-ChecksumEntry "$h1  x-aulaglobal-mcp_1.0.0_windows_x64.zip`n" 'aulaglobal-mcp_1.0.0_windows_x64.zip').Count -eq 0)
Check 'distingue mayusculas' ((Get-ChecksumEntry $sums 'AULAGLOBAL-MCP_1.0.0_WINDOWS_X64.ZIP').Count -eq 0)
Check 'admite CRLF' ((Get-ChecksumEntry ($sums -replace "`n", "`r`n") 'aulaglobal-mcp_1.0.0_windows_x64.zip').Count -eq 1)
Check 'admite modo binario (*)' ((Get-ChecksumEntry "$h1 *a.zip`n" 'a.zip').Count -eq 1)
Check 'detecta duplicados' ((Get-ChecksumEntry "$h1  a.zip`n$h2  a.zip`n" 'a.zip').Count -eq 2)
Check 'lista vacia si no esta' ((Get-ChecksumEntry $sums 'nada.zip').Count -eq 0)
Check 'texto vacio' ((Get-ChecksumEntry '' 'a.zip').Count -eq 0)

$script:Cur = 'Test-OldSshKeygenOutput'
Check 'unknown option' (Test-OldSshKeygenOutput 'ssh-keygen.exe: unknown option -- Y')
Check 'illegal option' (Test-OldSshKeygenOutput 'ssh-keygen: illegal option -- Y')
Check 'usage' (Test-OldSshKeygenOutput 'usage: ssh-keygen [-q] [-b bits]')
Check 'firma mala no es "antiguo"' (-not (Test-OldSshKeygenOutput 'Could not verify signature.'))
Check 'vacio no es "antiguo"' (-not (Test-OldSshKeygenOutput ''))

$script:Cur = 'Add-PathEntry'
$env:AG_TEST_ROOT = 'D:\q'
Check 'a un PATH vacio' ((Add-PathEntry '' 'C:\X') -ceq 'C:\X')
Check 'al final' ((Add-PathEntry 'C:\A;C:\B' 'C:\X') -ceq 'C:\A;C:\B;C:\X')
Check 'quita el ; final' ((Add-PathEntry 'C:\A;' 'C:\X') -ceq 'C:\A;C:\X')
Check 'idempotente' ((Add-PathEntry 'C:\A;C:\X' 'C:\X') -ceq 'C:\A;C:\X')
Check 'idempotente con barra final' ((Add-PathEntry 'C:\A;C:\X\' 'C:\X') -ceq 'C:\A;C:\X\')
Check 'idempotente sin distinguir mayusculas' ((Add-PathEntry 'c:\a;c:\x' 'C:\X') -ceq 'c:\a;c:\x')
Check 'idempotente con variables de entorno' ((Add-PathEntry 'C:\A;%AG_TEST_ROOT%\bin' 'D:\q\bin') -ceq 'C:\A;%AG_TEST_ROOT%\bin')
Check 'no confunde prefijos' ((Add-PathEntry 'C:\A;C:\XY' 'C:\X') -ceq 'C:\A;C:\XY;C:\X')
$p = 'C:\A'
for ($i = 0; $i -lt 3; $i++) { $p = Add-PathEntry $p 'C:\Nueva' }
Check 'tres llamadas, una sola entrada' ($p -ceq 'C:\A;C:\Nueva')

$script:Cur = 'ConvertTo-QuotedArg'
Check 'simple' ((ConvertTo-QuotedArg 'abc') -ceq 'abc')
Check 'con espacios' ((ConvertTo-QuotedArg 'a b') -ceq '"a b"')
Check 'vacio' ((ConvertTo-QuotedArg '') -ceq '""')
Check 'con comillas' ((ConvertTo-QuotedArg 'a"b') -ceq '"a\"b"')
Check 'barra final con espacios' ((ConvertTo-QuotedArg 'C:\x y\') -ceq '"C:\x y\\"')

$script:Cur = 'Resolve-InstallDir (semantica de Linux)'
Check '~' ((Resolve-InstallDir '~' '/home/u' '/work') -ceq '/home/u')
Check '~/bin' ((Resolve-InstallDir '~/bin' '/home/u' '/work') -ceq '/home/u/bin')
Check 'relativa' ((Resolve-InstallDir 'rel' '/home/u' '/work') -ceq '/work/rel')
Check 'quita barra final' ((Resolve-InstallDir '/a/b/' '/home/u' '/work') -ceq '/a/b')
Check 'comillas' ((Resolve-InstallDir '"/a b"' '/home/u' '/work') -ceq '/a b')
$env:AG_TEST_DIR = '/zz'
Check 'variables %X%' ((Resolve-InstallDir '%AG_TEST_DIR%/x' '/home/u' '/work') -ceq '/zz/x')

$script:Cur = 'Confirm-Choice'
$script:Answers = @()
function Read-Host {
  param([string]$Prompt)
  $a = $script:Answers[0]
  $script:Answers = @($script:Answers | Select-Object -Skip 1)
  return $a
}
function Ask([string]$answer, [bool]$default) { $script:Answers = @($answer); return (Confirm-Choice 'pregunta' $default $true) }
Check 'Enter = predeterminado true' (Ask '' $true)
Check 'Enter = predeterminado false' (-not (Ask '' $false))
Check 's' (Ask 's' $false)
Check 'S' (Ask 'S' $false)
Check 'si' (Ask 'si' $false)
Check 'si con tilde' (Ask (T 's\u00ed') $false)
Check 'yes' (Ask 'yes' $false)
Check 'n' (-not (Ask 'n' $true))
Check 'NO' (-not (Ask 'NO' $true))
Check 'texto raro = predeterminado' (Ask 'quizas' $true)
$script:Answers = @('s')
Check 'sin terminal no pregunta' (-not (Confirm-Choice 'pregunta' $false $false))
Remove-Item Function:\Read-Host

# ---------------------------------------------------------------- extremo a extremo (pwsh en Linux)

Write-Host '== Extremo a extremo con servidor local'
foreach ($d in @('cases', 'tb')) { New-Item -ItemType Directory -Force -Path (Join-Path $W $d) | Out-Null }
$key = (Get-Content -Raw (Join-Path $W 'keys/test.pub')).Trim() -split ' '
$testKey = "$($key[0]) $($key[1]) aulaglobal-mcp-update"
$testPs1 = Join-Path $W 'install.test.ps1'
$orig = [System.IO.File]::ReadAllText($ps1)
$mod = [regex]::Replace($orig, "(?m)^(\s*\`$SignKey = ')[^']+(')", { param($m) $m.Groups[1].Value + $testKey + $m.Groups[2].Value })
[System.IO.File]::WriteAllText($testPs1, $mod)
$script:Cur = 'preparacion'
Check 'la copia de prueba solo cambia la linea de la clave' ((Compare-Object ($orig -split "`n") ($mod -split "`n")).Count -eq 2)

$latest = Join-Path $W 'latest-tag'
$server = Start-Process -FilePath (Join-Path $W 'bin/testserver') -ArgumentList @('-dir', (Join-Path $W 'srv'), '-latest', $latest, '-addr', "127.0.0.1:$Port") `
  -RedirectStandardError (Join-Path $W 'server-ps.log') -PassThru
try {
  $ready = $false
  for ($i = 0; $i -lt 20 -and -not $ready; $i++) {
    try { Invoke-WebRequest -Uri "$Base/tag/x" -UseBasicParsing | Out-Null; $ready = $true } catch { Start-Sleep -Milliseconds 300 }
  }
  Check 'servidor local arrancado' $ready

  $archLabel = if ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() -eq 'Arm64') { 'windows_arm64' } else { 'windows_x64' }
  $pwsh = (Get-Command pwsh).Source

  function New-Case([string]$Name) {
    $c = Join-Path (Join-Path $W 'cases') $Name
    if (Test-Path $c) { Remove-Item -Recurse -Force $c }
    New-Item -ItemType Directory -Force -Path (Join-Path $c 'tmp'), (Join-Path $c 'home') | Out-Null
    return $c
  }
  # Ejecuta el instalador como un usuario: el texto del script entra por la
  # tuberia hacia iex, igual que con "irm | iex".
  function Invoke-Installer([string]$Case, [string]$ScriptPath, [hashtable]$EnvVars, [string]$PathOverride) {
    $saved = @{}
    $names = @('AULAGLOBAL_RELEASE_BASE_URL', 'AULAGLOBAL_YES', 'AULAGLOBAL_INSTALL_DIR', 'AULAGLOBAL_VERSION', 'TMPDIR', 'HOME', 'PATH')
    foreach ($n in $names) { $saved[$n] = [Environment]::GetEnvironmentVariable($n) }
    try {
      foreach ($n in @('AULAGLOBAL_YES', 'AULAGLOBAL_INSTALL_DIR', 'AULAGLOBAL_VERSION')) { [Environment]::SetEnvironmentVariable($n, $null) }
      $env:AULAGLOBAL_RELEASE_BASE_URL = $Base
      $env:TMPDIR = (Join-Path $Case 'tmp')
      $env:HOME = (Join-Path $Case 'home')
      if ($PathOverride) { $env:PATH = $PathOverride }
      foreach ($k in $EnvVars.Keys) { [Environment]::SetEnvironmentVariable($k, $EnvVars[$k]) }
      $cmd = "[System.IO.File]::ReadAllText('$ScriptPath') | Invoke-Expression; exit `$LASTEXITCODE"
      $o = & $pwsh -NoProfile -NonInteractive -Command $cmd 2>&1 | Out-String
      $script:Out = $o -replace "`r", ''
      $script:Code = $LASTEXITCODE
    }
    finally {
      foreach ($n in $names) { [Environment]::SetEnvironmentVariable($n, $saved[$n]) }
    }
  }
  function Exe([string]$Case) { return (Join-Path (Join-Path $Case 'bin') 'aulaglobal-mcp.exe') }
  function Backup-Srv { if (Test-Path "$W/srv.ps.orig") { Remove-Item -Recurse -Force "$W/srv.ps.orig" }; Copy-Item -Recurse "$W/srv" "$W/srv.ps.orig" }
  function Restore-Srv { Remove-Item -Recurse -Force "$W/srv"; Copy-Item -Recurse "$W/srv.ps.orig" "$W/srv" }
  Backup-Srv
  $rel2 = "$W/srv/releases/download/v0.0.2"
  $zip2 = "$rel2/aulaglobal-mcp_0.0.2_$archLabel.zip"

  $script:Cur = 'ps1: instalacion basica'
  $c = New-Case 'basic'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'codigo 0' ($script:Code -eq 0)
  Check 'exe instalado' (Test-Path (Exe $c))
  Check 'contenido = entrada del zip (stub 0.0.2)' ((Get-Content -Raw (Exe $c)) -match 'echo 0\.0\.2')
  Check 'paso 1/5' (Has '[1/5] Preparando la instalaci')
  Check 'paso 5/5' (Has '[5/5] Preparando la terminal')
  Check 'firma verificada' (Has ('Firma de la versi' + [char]0xF3 + 'n verificada'))
  Check 'checksum correcto' (Has 'Checksum SHA-256 correcto')
  Check 'ultima version resuelta por redireccion' (Has ([string][char]0xDA + 'ltima versi' + [char]0xF3 + 'n publicada: v0.0.2'))
  Check 'no ejecuta setup sin terminal' (Has 'no se ejecuta setup')
  Check 'solo el exe en la carpeta' ((@(Get-ChildItem -Force (Join-Path $c 'bin')).Count) -eq 1)
  Check 'sin temporales' ((@(Get-ChildItem -Force (Join-Path $c 'tmp')).Count) -eq 0)
  Check 'marcas del estilo de setup' ((Has ([string][char]0x2713)) -and (Has ([string][char]0xB7)))
  Check 'sin emojis ni exclamaciones' (-not (Has '!!'))

  $script:Cur = 'ps1: reinstalacion (misma version) y .old'
  & chmod +x (Exe $c)
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'codigo 0' ($script:Code -eq 0)
  Check 'detecta la version instalada' (Has ('Ya tienes la versi' + [char]0xF3 + 'n 0.0.2 instalada'))
  Check 'el anterior queda como .old' (Test-Path ((Exe $c) + '.old'))
  Check 'exe nuevo presente' (Test-Path (Exe $c))
  Check 'sin temporales de instalacion' ((@(Get-ChildItem -Force (Join-Path $c 'bin') | Where-Object { $_.Name -like '*install-*' }).Count) -eq 0)

  $script:Cur = 'ps1: actualizacion 0.0.1 -> 0.0.2'
  $c = New-Case 'upgrade'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_VERSION = 'v0.0.1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'instala 0.0.1' ((Get-Content -Raw (Exe $c)) -match 'echo 0\.0\.1')
  & chmod +x (Exe $c)
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'informa del reemplazo' (Has ('Tienes la versi' + [char]0xF3 + 'n 0.0.1 instalada; se sustituir' + [char]0xE1 + ' por la 0.0.2'))
  Check 'queda 0.0.2' ((Get-Content -Raw (Exe $c)) -match 'echo 0\.0\.2')

  $script:Cur = 'ps1: version sin v y no validas'
  $c = New-Case 'pin'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_VERSION = '0.0.1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check '0.0.1 sin v' ((Test-Path (Exe $c)) -and ((Get-Content -Raw (Exe $c)) -match 'echo 0\.0\.1'))
  foreach ($bad in @('1.2', 'v1.2.3-rc1', 'latest')) {
    $c = New-Case 'badver'
    Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_VERSION = $bad; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
    $script:Cur = "ps1: version no valida $bad"
    Check 'falla con codigo 1' ($script:Code -eq 1)
    Check 'mensaje' (Has 'no es v')
    Check 'nada instalado' (-not (Test-Path (Join-Path $c 'bin')))
  }
  $script:Cur = 'ps1: version inexistente'
  $c = New-Case 'nover'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_VERSION = 'v0.0.99'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'falla' ($script:Code -eq 1)
  Check 'mensaje de descarga' (Has 'No se pudo descargar')
  Check 'nada instalado' (-not (Test-Path (Join-Path $c 'bin')))
  Check 'sin temporales' ((@(Get-ChildItem -Force (Join-Path $c 'tmp')).Count) -eq 0)

  $script:Cur = 'ps1: zip manipulado (checksum)'
  Restore-Srv
  $fs = [System.IO.File]::Open($zip2, 'Append'); $fs.WriteByte(0x78); $fs.Dispose()
  $c = New-Case 'badzip'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'falla' ($script:Code -eq 1)
  Check 'mensaje de checksum' (Has 'checksum SHA-256 del archivo descargado no coincide')
  Check 'la firma si era correcta' (Has 'verificada')
  Check 'nada instalado' (-not (Test-Path (Join-Path $c 'bin')))
  Check 'sin temporales' ((@(Get-ChildItem -Force (Join-Path $c 'tmp')).Count) -eq 0)

  $script:Cur = 'ps1: checksums.txt manipulado (firma)'
  Restore-Srv
  $b = [System.IO.File]::ReadAllBytes("$rel2/checksums.txt")
  if ($b[0] -eq 0x30) { $b[0] = 0x31 } else { $b[0] = 0x30 }
  [System.IO.File]::WriteAllBytes("$rel2/checksums.txt", $b)
  $c = New-Case 'badsums'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'falla' ($script:Code -eq 1)
  Check 'mensaje de firma' (Has ('La firma de la versi' + [char]0xF3 + 'n no es v' + [char]0xE1 + 'lida'))
  Check 'no llega al checksum' (-not (Has 'Checksum SHA-256 correcto'))
  Check 'nada instalado' (-not (Test-Path (Join-Path $c 'bin')))

  $script:Cur = 'ps1: firma ausente'
  Restore-Srv
  Remove-Item "$rel2/checksums.txt.sig"
  $c = New-Case 'nosig'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'falla' ($script:Code -eq 1)
  Check 'nada instalado' (-not (Test-Path (Join-Path $c 'bin')))
  Restore-Srv

  $script:Cur = 'ps1: clave distinta (instalador publicado vs version de prueba)'
  $c = New-Case 'wrongkey'
  Invoke-Installer $c $ps1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'falla' ($script:Code -eq 1)
  Check 'mensaje de firma' (Has ('no es v' + [char]0xE1 + 'lida'))
  Check 'nada instalado' (-not (Test-Path (Join-Path $c 'bin')))

  # PATH restringido: todo /usr/bin y /usr/local/bin menos ssh-keygen
  function New-ToolDir([string]$Name, [string]$ShimBody) {
    $d = Join-Path (Join-Path $W 'tb') $Name
    if (Test-Path $d) { Remove-Item -Recurse -Force $d }
    New-Item -ItemType Directory -Force -Path $d | Out-Null
    foreach ($dir in @('/usr/bin', '/usr/local/bin')) {
      if (Test-Path $dir) {
        foreach ($f in (Get-ChildItem $dir)) {
          if ($f.Name -ne 'ssh-keygen' -and -not (Test-Path -LiteralPath (Join-Path $d $f.Name))) {
            [System.IO.File]::CreateSymbolicLink((Join-Path $d $f.Name), $f.FullName) | Out-Null
          }
        }
      }
    }
    if ($ShimBody) {
      $shim = Join-Path $d 'ssh-keygen'
      [System.IO.File]::WriteAllText($shim, $ShimBody)
      & chmod +x $shim
    }
    return $d
  }
  $script:Cur = 'ps1: sin ssh-keygen'
  $noSsh = New-ToolDir 'nossh' $null
  $c = New-Case 'nossh'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') } $noSsh
  Check 'falla' ($script:Code -eq 1)
  Check 'mensaje' (Has 'No se encuentra ssh-keygen')
  Check 'explica el Cliente OpenSSH' (Has 'Cliente OpenSSH')
  Check 'nada instalado' (-not (Test-Path (Join-Path $c 'bin')))

  $script:Cur = 'ps1: ssh-keygen antiguo'
  $oldSsh = New-ToolDir 'oldssh' "#!/bin/sh`necho 'ssh-keygen: unknown option -- Y' >&2`nexit 1`n"
  $c = New-Case 'oldssh'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') } $oldSsh
  Check 'falla' ($script:Code -eq 1)
  Check 'mensaje' (Has 'demasiado antiguo')
  Check 'nada instalado' (-not (Test-Path (Join-Path $c 'bin')))

  $script:Cur = 'ps1: .exe en uso (no se puede apartar el anterior)'
  $c = New-Case 'locked'
  New-Item -ItemType Directory -Force -Path (Join-Path $c 'bin') | Out-Null
  Set-Content -Path (Exe $c) -Value 'anterior'
  New-Item -ItemType Directory -Force -Path ((Exe $c) + '.old/sub') | Out-Null # .old no se puede borrar con Remove-Item -Force
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path $c 'bin') }
  Check 'falla' ($script:Code -eq 1)
  Check 'pide cerrar el asistente' (Has 'Cierra el asistente')
  Check 'el exe anterior sigue intacto' ((Get-Content -Raw (Exe $c)).Trim() -eq 'anterior')
  Check 'sin temporales de instalacion' ((@(Get-ChildItem -Force (Join-Path $c 'bin') | Where-Object { $_.Name -like '*install-*' }).Count) -eq 0)

  $script:Cur = 'ps1: carpeta imposible'
  $c = New-Case 'badpath'
  Set-Content -Path (Join-Path $c 'afile') -Value 'x'
  Invoke-Installer $c $testPs1 @{ AULAGLOBAL_YES = '1'; AULAGLOBAL_INSTALL_DIR = (Join-Path (Join-Path $c 'afile') 'bin') }
  Check 'falla' ($script:Code -eq 1)
  Check 'sin temporales' ((@(Get-ChildItem -Force (Join-Path $c 'tmp')).Count) -eq 0)
}
finally {
  if ($server -and -not $server.HasExited) { Stop-Process -Id $server.Id -Force }
}

Write-Host ''
Write-Host "== Resultado PowerShell: $($script:Pass) comprobaciones correctas, $($script:Fail) fallidas"
if ($script:Fail -gt 0) { exit 1 }
exit 0
