<#
.SYNOPSIS
    Seed the SLP v0 ontology through docker compose (Windows).
.DESCRIPTION
    Windows counterpart of scripts/seed-slp.sh. The workspace bootstrap SQL is
    copied into the database container rather than piped over stdin, because
    cmd.exe/PowerShell stdin redirection is not byte-safe for UTF-8 files.

    Compose configuration arrives through the environment variables exported
    by the Makefile: COMPOSE_FILES, DB_SERVICE, SEED_WORKSPACE_ID.
#>
[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if (-not $env:COMPOSE_FILES) {
    Write-Error 'COMPOSE_FILES is not set; run this target through `make seed-slp`.'
    exit 1
}
if (-not $env:SEED_WORKSPACE_ID) {
    Write-Error 'SEED_WORKSPACE_ID is not set; run this target through `make seed-slp`.'
    exit 1
}

# Defaults mirror the Makefile so the script can also be run manually.
$dbService = if ($env:DB_SERVICE) { $env:DB_SERVICE } else { 'postgres' }
$seedFile = if ($env:SEED_FILE) { $env:SEED_FILE } else { 'deploy/compose/postgres/seed/default-workspace.sql' }
$workspaceId = $env:SEED_WORKSPACE_ID

$composeArgs = @('compose') + @($env:COMPOSE_FILES -split '\s+' | Where-Object { $_ })

if (-not (Test-Path -LiteralPath $seedFile -PathType Leaf)) {
    Write-Error "Seed file not found: $seedFile"
    exit 1
}

$remotePath = '/tmp/ai-sdlc-default-workspace.sql'

Write-Output "==> Ensuring seed workspace $workspaceId exists"
& docker @composeArgs cp $seedFile "${dbService}:${remotePath}"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

# Backticks keep $POSTGRES_USER / $POSTGRES_DB literal so that the container
# shell expands them from the Compose-set environment.
& docker @composeArgs exec -T $dbService sh -c "psql -v ON_ERROR_STOP=1 -v workspace_id=$workspaceId -U `$POSTGRES_USER -d `$POSTGRES_DB -f $remotePath"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& docker @composeArgs exec -T $dbService sh -c "rm -f $remotePath" | Out-Null

Write-Output '==> Loading SLP v0 ontology'
& docker @composeArgs exec -T -e "SEED_WORKSPACE_ID=$workspaceId" semantic /app/seed-slp
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
