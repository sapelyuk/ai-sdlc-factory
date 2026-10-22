<#
.SYNOPSIS
    Apply or roll back PostgreSQL migrations through docker compose (Windows).
.DESCRIPTION
    Windows counterpart of scripts/migrate.sh. GNU make hands recipes to
    cmd.exe on Windows, where POSIX `for f in ...; do ... done` loops and
    single-quoted `sh -c '...'` bodies are not valid syntax.

    The Compose configuration is received through the environment variables
    exported by the Makefile: COMPOSE_FILES, MIGRATIONS_DIR, DB_SERVICE.

    Migration files are copied into the database container instead of being
    piped over stdin, because cmd.exe/PowerShell stdin redirection is not
    byte-safe for the UTF-8 content of the SQL files.
.PARAMETER Direction
    "up" to apply *.up.sql in ascending order, "down" to roll back
    *.down.sql in descending order.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('up', 'down')]
    [string]$Direction
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not $env:COMPOSE_FILES) {
    Write-Error 'COMPOSE_FILES is not set; run this target through `make migrate-up` / `make migrate-down`.'
    exit 1
}

# Defaults mirror the Makefile so the script can also be run manually.
$migrationsDir = if ($env:MIGRATIONS_DIR) { $env:MIGRATIONS_DIR } else { 'migrations/postgres' }
$dbService = if ($env:DB_SERVICE) { $env:DB_SERVICE } else { 'postgres' }

# Split the Compose flags into an argument array for `docker`.
$composeArgs = @('compose') + @($env:COMPOSE_FILES -split '\s+' | Where-Object { $_ })

if (-not (Test-Path -LiteralPath $migrationsDir -PathType Container)) {
    Write-Error "Migrations directory not found: $migrationsDir"
    exit 1
}

$suffix = if ($Direction -eq 'up') { '*.up.sql' } else { '*.down.sql' }
$verb = if ($Direction -eq 'up') { 'Applying' } else { 'Rolling back' }

# Ascending order for up, reverse for down (same as `sort` / `sort -r`).
$files = @(Get-ChildItem -LiteralPath $migrationsDir -Filter $suffix -File | Sort-Object Name)
if ($Direction -eq 'down') { [Array]::Reverse($files) }

if ($files.Count -eq 0) {
    Write-Error "No migration files matched $migrationsDir\$suffix"
    exit 1
}

$remotePath = '/tmp/ai-sdlc-migration.sql'

foreach ($file in $files) {
    Write-Output ("==> {0} {1}" -f $verb, (Join-Path $migrationsDir $file.Name))

    & docker @composeArgs cp $file.FullName "${dbService}:${remotePath}"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    # Backticks keep $POSTGRES_USER / $POSTGRES_DB literal so that the
    # container shell expands them from the Compose-set environment.
    & docker @composeArgs exec -T $dbService sh -c "psql -v ON_ERROR_STOP=1 -U `$POSTGRES_USER -d `$POSTGRES_DB -f $remotePath"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
}

& docker @composeArgs exec -T $dbService sh -c "rm -f $remotePath" | Out-Null

if ($Direction -eq 'up') {
    Write-Output '==> All migrations applied'
} else {
    Write-Output '==> All migrations rolled back'
}
