# Instalador de aulaglobal-mcp para Windows (PowerShell 5.1 y PowerShell 7).
#
#   irm https://github.com/Alejandro12120/aulaglobal-mcp/releases/latest/download/install.ps1 | iex
#
# Variables de entorno opcionales (definelas antes, en la misma ventana):
#   $env:AULAGLOBAL_VERSION = 'v0.1.5'       instala esa version en vez de la ultima
#   $env:AULAGLOBAL_INSTALL_DIR = 'C:\ruta'  carpeta de instalacion; si se define, no se pregunta
#   $env:AULAGLOBAL_YES = '1'                acepta las opciones predeterminadas, sin preguntas
#
# Que hace: descarga el archivo de tu sistema y checksums.txt con su firma,
# verifica la firma (ssh-keygen -Y verify, OpenSSH 8.1 o posterior) con la clave
# publica de abajo, comprueba el SHA-256 y solo entonces instala el programa.
# No existe ninguna forma de saltarse la verificacion de la firma.
#
# Este archivo es solo ASCII a proposito: con "irm | iex", Windows PowerShell 5.1
# puede decodificar mal los caracteres no ASCII. Los acentos y los simbolos se
# escriben como \uXXXX y los convierte la funcion T.
#
# Todo va dentro de un bloque & { ... }: no deja funciones ni variables en tu
# sesion y los errores no cierran tu ventana (no se usa exit).

& {
  $ErrorActionPreference = 'Stop'
  Set-StrictMode -Version 2.0

  $Repo = 'Alejandro12120/aulaglobal-mcp'
  $BinName = 'aulaglobal-mcp'
  $SignNamespace = 'aulaglobal-mcp-update'
  # Misma clave que internal/selfupdate/update_key.pub (una prueba de Go lo comprueba).
  $SignKey = 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFNMn9RCnq08BKgWOIIpDJLaCTEpsREfzlK9VM5609tY aulaglobal-mcp-update'
  $ManualUrl = 'https://github.com/' + $Repo + '/releases/latest'

  # ------------------------------------------------------------ texto y salida

  # Convierte las secuencias \uXXXX de una plantilla en los caracteres reales.
  function T([string]$Text) {
    [regex]::Replace($Text, '\\u([0-9a-fA-F]{4})', {
        param($m)
        [string][char][Convert]::ToInt32($m.Groups[1].Value, 16)
      })
  }

  function Format-Line([string]$Template, [object[]]$Values) {
    $text = T $Template
    if ($null -ne $Values -and $Values.Count -gt 0) { $text = $text -f $Values }
    $text
  }
  function Write-Ok([string]$Template, [Parameter(ValueFromRemainingArguments = $true)][object[]]$Values) {
    Write-Host ((T '\u2713') + ' ' + (Format-Line $Template $Values))
  }
  function Write-Note([string]$Template, [Parameter(ValueFromRemainingArguments = $true)][object[]]$Values) {
    Write-Host ((T '\u00b7') + ' ' + (Format-Line $Template $Values))
  }
  function Write-Warn([string]$Template, [Parameter(ValueFromRemainingArguments = $true)][object[]]$Values) {
    Write-Host ('! ' + (Format-Line $Template $Values))
  }
  function Write-Step([int]$Number, [string]$Title) {
    Write-Host ''
    Write-Host ('[{0}/5] {1}' -f $Number, (T $Title))
    Write-Host ''
  }

  # Interrumpe la instalacion con un mensaje (la primera linea) y detalles.
  # El bloque principal los muestra como un error normal y deja el codigo en 1.
  function Exit-Installer([string]$Template, [string[]]$Details) {
    $lines = @('@@' + (T $Template))
    foreach ($d in $Details) { $lines += (T $d) }
    throw ($lines -join "`n")
  }

  # ------------------------------------------------------------ funciones puras

  # "v1.2.3" o "1.2.3" -> "v1.2.3"; cualquier otra cosa -> $null.
  function ConvertTo-ReleaseTag([string]$Value) {
    $v = $Value.Trim()
    if ($v -cmatch '^v?([0-9]{1,9})\.([0-9]{1,9})\.([0-9]{1,9})\z') {
      return ('v{0}.{1}.{2}' -f $Matches[1], $Matches[2], $Matches[3])
    }
    return $null
  }

  # Arquitectura de Windows -> etiqueta del archivo publicado, o $null.
  function ConvertTo-PlatformLabel([string]$Arch) {
    if ($Arch -match '^(?i:x64|amd64)\z') { return 'windows_x64' }
    if ($Arch -match '^(?i:arm64)\z') { return 'windows_arm64' }
    return $null
  }

  # Lineas de checksums.txt ("<hex>  <archivo>") cuyo nombre es exactamente $Name.
  function Get-ChecksumEntry([string]$Text, [string]$Name) {
    $found = @()
    foreach ($line in ($Text -split "`n")) {
      $f = @($line.Trim() -split '\s+')
      if ($f.Count -eq 2 -and (($f[1] -ceq $Name) -or ($f[1] -ceq ('*' + $Name)))) { $found += $f[0] }
    }
    return , $found
  }

  # Devuelve $true si la salida de ssh-keygen indica que no conoce -Y verify.
  function Test-OldSshKeygenOutput([string]$Text) {
    return [bool]($Text -match '(?i)unknown option|illegal option|invalid option|unsupported option|usage: ssh-keygen')
  }

  # Devuelve $PathValue con $Dir al final si no estaba ya (sin distinguir
  # mayusculas ni barra final); si ya estaba, devuelve $PathValue sin cambios.
  function Add-PathEntry([string]$PathValue, [string]$Dir) {
    $norm = { param($p) [Environment]::ExpandEnvironmentVariables($p).Trim().TrimEnd('\', '/') }
    $target = & $norm $Dir
    if ($PathValue) {
      foreach ($e in ($PathValue -split ';')) {
        if ($e.Trim() -and ((& $norm $e) -ieq $target)) { return $PathValue }
      }
    }
    if ([string]::IsNullOrEmpty($PathValue)) { return $Dir }
    return ($PathValue.TrimEnd(';') + ';' + $Dir)
  }

  # Argumento de linea de comandos con las comillas que necesita Windows.
  function ConvertTo-QuotedArg([string]$Arg) {
    if ($Arg.Length -gt 0 -and $Arg -notmatch '[\s"]') { return $Arg }
    return '"' + ($Arg -replace '(\\*)"', '$1$1\"' -replace '(\\+)\z', '$1$1') + '"'
  }

  # Carpeta de instalacion absoluta: expande ~ y variables de entorno.
  function Resolve-InstallDir([string]$Value, [string]$Home1, [string]$Cwd) {
    $v = [Environment]::ExpandEnvironmentVariables($Value.Trim().Trim('"'))
    if ($v -eq '~') { $v = $Home1 }
    elseif ($v.StartsWith('~\') -or $v.StartsWith('~/')) { $v = Join-Path $Home1 $v.Substring(2) }
    if (-not [System.IO.Path]::IsPathRooted($v)) { $v = Join-Path $Cwd $v }
    $full = [System.IO.Path]::GetFullPath($v)
    $root = [System.IO.Path]::GetPathRoot($full)
    if ($full.Length -gt $root.Length) { $full = $full.TrimEnd('\', '/') }
    return $full
  }

  # Pregunta si/no. Sin terminal interactiva devuelve el valor predeterminado.
  function Confirm-Choice([string]$Prompt, [bool]$Default, [bool]$Interactive) {
    if (-not $Interactive) { return $Default }
    Write-Host ''
    $a = Read-Host -Prompt (T $Prompt)
    if ($null -eq $a) { return $Default }
    $a = $a.Trim().ToLowerInvariant()
    if ($a -in @('s', 'si', (T 's\u00ed'), 'y', 'yes')) { return $true }
    if ($a -in @('n', 'no')) { return $false }
    return $Default
  }

  # ------------------------------------------------------------ red

  function Save-Url([string]$Url, [string]$Path) {
    $old = $ProgressPreference
    $ProgressPreference = 'SilentlyContinue' # en 5.1 la barra de progreso ralentiza mucho la descarga
    try {
      Invoke-WebRequest -Uri $Url -OutFile $Path -UseBasicParsing -TimeoutSec 900 `
        -Headers @{ 'User-Agent' = 'aulaglobal-mcp-installer' } | Out-Null
    }
    finally { $ProgressPreference = $old }
  }

  # Etiqueta de la ultima version: la pagina /releases/latest redirige a /releases/tag/vX.Y.Z.
  function Get-LatestTag([string]$BaseUrl) {
    $req = [System.Net.HttpWebRequest]::Create($BaseUrl + '/latest')
    $req.AllowAutoRedirect = $false
    $req.Method = 'HEAD'
    $req.UserAgent = 'aulaglobal-mcp-installer'
    $req.Timeout = 30000
    $resp = $null
    try { $resp = $req.GetResponse() }
    catch [System.Net.WebException] { $resp = $_.Exception.Response }
    if ($null -eq $resp) { return $null }
    $loc = $null
    try { $loc = [string]$resp.Headers['Location'] }
    finally { $resp.Close() }
    if (-not $loc -or $loc -notmatch '/releases/tag/[^/]+\z') { return $null }
    return (ConvertTo-ReleaseTag ($loc.Substring($loc.LastIndexOf('/') + 1)))
  }

  # ------------------------------------------------------------ procesos

  # Ejecuta un programa con stdin tomado byte a byte de un archivo (o vacio).
  # No se usan redirecciones de PowerShell: en 5.1 recodifican los bytes.
  function Invoke-Native([string]$FilePath, [string[]]$Arguments, [string]$StdinPath) {
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    $psi.Arguments = (@($Arguments | ForEach-Object { ConvertTo-QuotedArg $_ }) -join ' ')
    $psi.UseShellExecute = $false
    $psi.CreateNoWindow = $true
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $p = [System.Diagnostics.Process]::Start($psi)
    $outTask = $p.StandardOutput.ReadToEndAsync()
    $errTask = $p.StandardError.ReadToEndAsync()
    try {
      if ($StdinPath) {
        $bytes = [System.IO.File]::ReadAllBytes($StdinPath)
        $p.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
        $p.StandardInput.BaseStream.Flush()
      }
    }
    catch [System.IO.IOException] { Write-Verbose ('stdin cerrado antes de tiempo: ' + $_.Exception.Message) } # el programa pudo terminar antes
    finally {
      try { $p.StandardInput.Close() } catch [System.IO.IOException] { Write-Verbose ('stdin ya cerrado: ' + $_.Exception.Message) }
    }
    $p.WaitForExit()
    return [pscustomobject]@{ ExitCode = $p.ExitCode; Output = ($outTask.Result + $errTask.Result) }
  }

  function Find-SshKeygen {
    $cmd = Get-Command 'ssh-keygen' -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd) { return $cmd.Source }
    if ($env:WINDIR) {
      $p = Join-Path (Join-Path (Join-Path $env:WINDIR 'System32') 'OpenSSH') 'ssh-keygen.exe'
      if (Test-Path -LiteralPath $p -PathType Leaf) { return $p }
    }
    return $null
  }

  # ------------------------------------------------------------ pasos

  function Confirm-Signature([string]$Dir) {
    $ssh = Find-SshKeygen
    if (-not $ssh) {
      Exit-Installer 'No se encuentra ssh-keygen, necesario para verificar la firma de la descarga.' @(
        'Por seguridad no se instala nada sin verificarla.',
        'Activa el "Cliente OpenSSH" de Windows: Configuraci\u00f3n > Aplicaciones > Caracter\u00edsticas opcionales >',
        'Ver caracter\u00edsticas > "Cliente OpenSSH". Despu\u00e9s abre una ventana nueva de PowerShell y repite el comando.',
        ('Instalaci\u00f3n manual: ' + $ManualUrl))
    }
    $allowed = Join-Path $Dir 'allowed_signers'
    $utf8 = New-Object System.Text.UTF8Encoding -ArgumentList $false
    [System.IO.File]::WriteAllText($allowed, ($SignNamespace + ' ' + $SignKey + "`n"), $utf8)
    $r = Invoke-Native $ssh @('-Y', 'verify', '-f', $allowed, '-I', $SignNamespace, '-n', $SignNamespace,
      '-s', (Join-Path $Dir 'checksums.txt.sig')) (Join-Path $Dir 'checksums.txt')
    if ($r.ExitCode -eq 0) {
      Write-Ok 'Firma de la versi\u00f3n verificada'
      return
    }
    if (Test-OldSshKeygenOutput $r.Output) {
      Exit-Installer 'Tu ssh-keygen es demasiado antiguo para verificar la firma (hace falta OpenSSH 8.1 o posterior).' @(
        'Por seguridad no se instala nada sin verificarla.',
        'Actualiza Windows o instala una versi\u00f3n reciente de OpenSSH y repite el comando.',
        ('Instalaci\u00f3n manual: ' + $ManualUrl))
    }
    Exit-Installer 'La firma de la versi\u00f3n no es v\u00e1lida. No se instala nada.' @(
      'Puede que la descarga est\u00e9 da\u00f1ada o manipulada. Vuelve a intentarlo m\u00e1s tarde.',
      ('Si el problema contin\u00faa, avisa en https://github.com/' + $Repo + '/issues'))
  }

  function Confirm-Checksum([string]$Dir, [string]$Archive) {
    $sums = [System.IO.File]::ReadAllText((Join-Path $Dir 'checksums.txt'))
    $entries = Get-ChecksumEntry $sums $Archive
    if ($entries.Count -eq 0) { Exit-Installer ('checksums.txt no incluye ' + $Archive + '. No se instala nada.') }
    if ($entries.Count -gt 1) { Exit-Installer ('checksums.txt lista ' + $Archive + ' m\u00e1s de una vez. No se instala nada.') }
    $want = [string]$entries[0]
    if ($want -notmatch '^[0-9a-fA-F]{64}\z') {
      Exit-Installer ('El checksum de ' + $Archive + ' tiene un formato no v\u00e1lido. No se instala nada.')
    }
    $got = (Get-FileHash -LiteralPath (Join-Path $Dir $Archive) -Algorithm SHA256).Hash
    if ($got.ToLowerInvariant() -ne $want.ToLowerInvariant()) {
      Exit-Installer 'El checksum SHA-256 del archivo descargado no coincide. No se instala nada.' @(
        'Vuelve a intentarlo; si se repite, avisa en el repositorio.')
    }
    Write-Ok 'Checksum SHA-256 correcto'
  }

  # Extrae solo la entrada raiz $EntryName del zip a $Dest (por nombre exacto:
  # ninguna ruta del zip se usa para escribir).
  function Expand-Binary([string]$Zip, [string]$EntryName, [string]$Dest) {
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $za = [System.IO.Compression.ZipFile]::OpenRead($Zip)
    try {
      $entry = $null
      foreach ($e in $za.Entries) {
        if ($e.FullName -ceq $EntryName) { $entry = $e; break }
      }
      if ($null -eq $entry) { Exit-Installer ('El archivo descargado no contiene ' + $EntryName + '. No se instala nada.') }
      if ($entry.Length -gt 100MB) { Exit-Installer ('El archivo descargado contiene un ' + $EntryName + ' demasiado grande. No se instala nada.') }
      $in = $entry.Open()
      try {
        $out = [System.IO.File]::Create($Dest)
        try { $in.CopyTo($out) } finally { $out.Dispose() }
      }
      finally { $in.Dispose() }
    }
    finally { $za.Dispose() }
  }

  # Copia $Source junto al destino y lo renombra. Un .exe en uso no se puede
  # sobrescribir pero si renombrar: el anterior pasa a <nombre>.old, como hace
  # "aulaglobal-mcp update", y el programa lo borra en el siguiente arranque.
  function Install-Executable([string]$Source, [string]$Dir, [string]$ExeName) {
    New-Item -ItemType Directory -Force -Path $Dir | Out-Null
    $target = Join-Path $Dir $ExeName
    $old = $target + '.old'
    $staged = Join-Path $Dir ('.' + $ExeName + '.install-' + [guid]::NewGuid().ToString('N'))
    try {
      Copy-Item -LiteralPath $Source -Destination $staged -Force
      if (Test-Path -LiteralPath $target) {
        try {
          if (Test-Path -LiteralPath $old) { Remove-Item -LiteralPath $old -Force }
          Move-Item -LiteralPath $target -Destination $old -Force
        }
        catch {
          Exit-Installer ('No se pudo sustituir ' + $target + ' porque est\u00e1 en uso.') @(
            'Cierra el asistente que lo usa (Claude, Codex, OpenCode...) y repite el comando.')
        }
        try { Move-Item -LiteralPath $staged -Destination $target }
        catch {
          Move-Item -LiteralPath $old -Destination $target -Force
          throw
        }
      }
      else {
        Move-Item -LiteralPath $staged -Destination $target
      }
    }
    finally {
      if (Test-Path -LiteralPath $staged) { Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue }
    }
  }

  function Get-InstalledVersion([string]$Exe) {
    try {
      $r = Invoke-Native $Exe @('--version') $null
      if ($r.ExitCode -ne 0) { return $null }
      $v = ConvertTo-ReleaseTag (($r.Output -split "`n" | Select-Object -First 1).Trim())
      if ($v) { return $v.Substring(1) }
    }
    catch { Write-Verbose ('no se pudo leer la version: ' + $_.Exception.Message) }
    return $null
  }

  function Install-AulaGlobalMcp {
    # TLS 1.2: Windows PowerShell 5.1 no lo activa siempre por defecto.
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

    $assumeYes = ($env:AULAGLOBAL_YES -eq '1')
    $terminal = $false
    try { $terminal = ([Environment]::UserInteractive -and -not [Console]::IsInputRedirected) } catch { $terminal = $false }
    $interactive = ($terminal -and -not $assumeYes)

    Write-Host (T 'Instalaci\u00f3n de aulaglobal-mcp')
    Write-Host ''
    if ($assumeYes) { Write-Note 'AULAGLOBAL_YES=1: se aceptan las opciones predeterminadas.' }
    elseif (-not $terminal) { Write-Note 'No hay terminal interactiva: se usan las opciones predeterminadas.' }

    # ---- 1/5
    Write-Step 1 'Preparando la instalaci\u00f3n'
    # La direccion base solo se cambia en las pruebas del propio instalador.
    $baseUrl = $env:AULAGLOBAL_RELEASE_BASE_URL
    if (-not $baseUrl) { $baseUrl = 'https://github.com/' + $Repo + '/releases' }
    $baseUrl = $baseUrl.TrimEnd('/')

    $arch = $null
    try { $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() } catch { $arch = $null }
    if (-not $arch) {
      $arch = $env:PROCESSOR_ARCHITEW6432
      if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }
    }
    $platform = ConvertTo-PlatformLabel ([string]$arch)
    if (-not $platform) {
      Exit-Installer ('No hay versi\u00f3n publicada para esta arquitectura de Windows (' + $arch + ').') @(
        'Solo se publican Windows x64 y ARM64 (no 32 bits).',
        ('Instalaci\u00f3n manual: ' + $ManualUrl))
    }
    Write-Note 'Sistema: Windows ({0})' $arch

    $requested = $env:AULAGLOBAL_VERSION
    if ($requested) {
      $tag = ConvertTo-ReleaseTag $requested
      if (-not $tag) {
        Exit-Installer ('La versi\u00f3n \u00ab' + $requested + '\u00bb no es v\u00e1lida.') @(
          'Usa el formato vX.Y.Z, por ejemplo $env:AULAGLOBAL_VERSION = ''v0.1.5''')
      }
      Write-Note 'Versi\u00f3n elegida: {0}' $tag
    }
    else {
      $tag = $null
      try { $tag = Get-LatestTag $baseUrl } catch { $tag = $null }
      if (-not $tag) {
        Exit-Installer 'No se pudo averiguar la \u00faltima versi\u00f3n publicada.' @(
          'Comprueba tu conexi\u00f3n. Para elegir una versi\u00f3n concreta:',
          ('  $env:AULAGLOBAL_VERSION = ''v0.1.5''; irm ' + $ManualUrl + '/download/install.ps1 | iex'),
          ('Instalaci\u00f3n manual: ' + $ManualUrl))
      }
      Write-Note '\u00daltima versi\u00f3n publicada: {0}' $tag
    }
    $version = $tag.Substring(1)
    $archive = '{0}_{1}_{2}.zip' -f $BinName, $version, $platform
    $exeName = $BinName + '.exe'

    $homeDir = $env:USERPROFILE
    if (-not $homeDir) { $homeDir = [Environment]::GetFolderPath('UserProfile') }
    $cwd = (Get-Location).Path
    $installDir = $null
    try {
      if ($env:AULAGLOBAL_INSTALL_DIR) {
        $installDir = Resolve-InstallDir $env:AULAGLOBAL_INSTALL_DIR $homeDir $cwd
      }
      else {
        $localApp = $env:LOCALAPPDATA
        if (-not $localApp) { $localApp = [Environment]::GetFolderPath('LocalApplicationData') }
        if (-not $localApp) {
          Exit-Installer 'No se pudo determinar la carpeta de instalaci\u00f3n.' @('Indica una con $env:AULAGLOBAL_INSTALL_DIR = ''C:\ruta''')
        }
        $default = Join-Path (Join-Path $localApp 'Programs') $BinName
        $installDir = $default
        if ($interactive) {
          Write-Host ''
          $answer = Read-Host -Prompt ((T 'Carpeta de instalaci\u00f3n') + ' [' + $default + ']')
          if ($answer -and $answer.Trim()) { $installDir = Resolve-InstallDir $answer $homeDir $cwd }
        }
      }
    }
    catch [System.ArgumentException], [System.NotSupportedException], [System.IO.PathTooLongException] {
      Exit-Installer 'La carpeta de instalaci\u00f3n no es una ruta v\u00e1lida.' @('Elige otra carpeta.')
    }
    Write-Note 'Carpeta de instalaci\u00f3n: {0}' $installDir
    $target = Join-Path $installDir $exeName

    if (Test-Path -LiteralPath $target -PathType Container) {
      Exit-Installer ($target + ' es una carpeta, no un programa.') @('Elige otra carpeta de instalaci\u00f3n.')
    }
    if (Test-Path -LiteralPath $target) {
      $current = Get-InstalledVersion $target
      if (-not $current) {
        Write-Warn 'Ya hay un programa llamado {0} en esa carpeta; se sustituir\u00e1 por la versi\u00f3n {1}.' $exeName $version
      }
      elseif ($current -eq $version) {
        Write-Ok 'Ya tienes la versi\u00f3n {0} instalada.' $version
        if ($interactive) {
          if (-not (Confirm-Choice '\u00bfQuieres reinstalarla de todos modos? [s/N]' $false $true)) {
            Write-Host ''
            Write-Note 'No se ha cambiado nada.'
            Write-Note ('Para configurarla de nuevo: "{0}" setup' -f $target)
            return
          }
        }
        else { Write-Note 'Sin terminal interactiva se reinstala igualmente.' }
      }
      else {
        Write-Warn 'Tienes la versi\u00f3n {0} instalada; se sustituir\u00e1 por la {1}.' $current $version
      }
    }

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ('aulaglobal-install-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    try {
      # ---- 2/5
      Write-Step 2 'Descargando'
      foreach ($f in @($archive, 'checksums.txt', 'checksums.txt.sig')) {
        Write-Note 'Descargando {0}' $f
        $url = $baseUrl + '/download/' + $tag + '/' + $f
        try { Save-Url $url (Join-Path $tmp $f) }
        catch {
          Exit-Installer ('No se pudo descargar ' + $f + '.') @(
            ('Direcci\u00f3n: ' + $url),
            ('Comprueba tu conexi\u00f3n y que la versi\u00f3n ' + $tag + ' exista.'),
            ('Instalaci\u00f3n manual: ' + $ManualUrl))
        }
      }
      Write-Ok 'Descarga completa'

      # ---- 3/5 (la firma se verifica antes de fiarse de nada de checksums.txt)
      Write-Step 3 'Verificando la descarga'
      Confirm-Signature $tmp
      Confirm-Checksum $tmp $archive
      $extracted = Join-Path $tmp $exeName
      Expand-Binary (Join-Path $tmp $archive) $exeName $extracted
      Write-Ok 'Extra\u00eddo {0}' $exeName

      # ---- 4/5
      Write-Step 4 'Instalando'
      Install-Executable $extracted $installDir $exeName
      Write-Ok 'Instalado {0} {1} en {2}' $BinName $version $installDir
      if (Get-Process -Name $BinName -ErrorAction SilentlyContinue) {
        Write-Warn 'Hay un asistente usando una copia anterior. Rein\u00edcialo para usar la versi\u00f3n nueva.'
      }
    }
    finally {
      Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    # ---- 5/5
    Write-Step 5 'Preparando la terminal'
    $inSession = ((Add-PathEntry ([string]$env:Path) $installDir) -eq [string]$env:Path)
    if ($inSession) {
      Write-Ok 'La carpeta ya est\u00e1 en tu PATH'
    }
    elseif (Confirm-Choice ('\u00bfA\u00f1adir esta carpeta al PATH de tu usuario? [S/n]') $true $interactive) {
      $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
      if ($null -eq $userPath) { $userPath = '' }
      $newUser = Add-PathEntry $userPath $installDir
      if ($newUser -ne $userPath) {
        [Environment]::SetEnvironmentVariable('Path', $newUser, 'User')
        Write-Ok 'A\u00f1adida al PATH del usuario'
        Write-Warn 'Las ventanas que ya estaban abiertas no ven el cambio; abre una nueva.'
      }
      else {
        Write-Note 'El PATH del usuario ya inclu\u00eda la carpeta; no se toca.'
      }
      $env:Path = Add-PathEntry ([string]$env:Path) $installDir
    }
    else {
      Write-Note 'No se ha modificado el PATH.'
      Write-Note ('Para ejecutarlo, usa la ruta completa: ' + $target)
    }

    # ---- setup
    $other = Get-Command $BinName -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($other -and ($other.Source -ine $target)) {
      Write-Warn 'Hay otra copia en {0} que puede ejecutarse antes que esta.' $other.Source
    }
    Write-Host ''
    if (-not $terminal) {
      Write-Note 'No hay terminal interactiva, as\u00ed que no se ejecuta setup.'
      Write-Note ('Cuando quieras configurarlo, ejecuta: "{0}" setup' -f $target)
      return
    }
    Write-Note 'Ahora se abre el asistente de configuraci\u00f3n (setup).'
    Write-Host ''
    & $target setup
    if ($LASTEXITCODE -ne 0) {
      Write-Host ''
      Write-Warn 'setup no termin\u00f3 correctamente.'
      Write-Note ('Puedes repetirlo cuando quieras: "{0}" setup' -f $target)
    }
  }

  try {
    Install-AulaGlobalMcp
  }
  catch {
    $msg = $_.Exception.Message
    Write-Host ''
    if ($msg.StartsWith('@@')) {
      $lines = $msg.Substring(2) -split "`n"
      Write-Host ((T '\u2717') + ' ' + $lines[0]) -ForegroundColor Red
      foreach ($l in ($lines | Select-Object -Skip 1)) { Write-Host ('  ' + $l) }
    }
    else {
      Write-Host ((T '\u2717') + ' ' + (T 'Error inesperado: ') + $msg) -ForegroundColor Red
      Write-Host ('  ' + (T 'Instalaci\u00f3n manual: ') + $ManualUrl)
    }
    $global:LASTEXITCODE = 1
  }
}
