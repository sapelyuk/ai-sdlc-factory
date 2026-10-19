$ErrorActionPreference = "Stop"

$baseUrl = if ($env:API_BASE_URL) { $env:API_BASE_URL.TrimEnd("/") } else { "http://localhost:8000" }
$workspaceId = $env:WORKSPACE_ID
$authorId = $env:AUTHOR_ID
$approverId = $env:APPROVER_ID

foreach ($entry in @(
    @{ Name = "WORKSPACE_ID"; Value = $workspaceId },
    @{ Name = "AUTHOR_ID"; Value = $authorId },
    @{ Name = "APPROVER_ID"; Value = $approverId }
)) {
    $parsed = [guid]::Empty
    if (-not [guid]::TryParse($entry.Value, [ref]$parsed) -or $parsed -eq [guid]::Empty) {
        throw "$($entry.Name) must contain a non-zero UUID"
    }
}
if ($authorId -eq $approverId) {
    throw "APPROVER_ID must differ from AUTHOR_ID"
}

$headers = @{
    "X-Workspace-ID" = $workspaceId
    "X-Author-ID" = $authorId
}

function Invoke-Registry {
    param(
        [Parameter(Mandatory = $true)][string]$Method,
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][int]$ExpectedStatus,
        [hashtable]$Headers = $script:headers,
        [object]$Body = $null
    )

    $tempFile = [System.IO.Path]::GetTempFileName()
    try {
        $curlArgs = @(
            "--silent", "--show-error",
            "--output", $tempFile,
            "--write-out", "%{http_code}",
            "--request", $Method
        )
        foreach ($key in $Headers.Keys) {
            $curlArgs += @("--header", "$key`: $($Headers[$key])")
        }
        if ($null -ne $Body) {
            $curlArgs += @("--header", "Content-Type: application/json")
            $curlArgs += @("--data-binary", ($Body | ConvertTo-Json -Depth 20 -Compress))
        }
        $statusText = & curl.exe @curlArgs "$baseUrl$Path"
        if ($LASTEXITCODE -ne 0) {
            throw "curl failed for $Method $Path with exit code $LASTEXITCODE"
        }
        $status = [int]("$statusText".Trim())
        $content = Get-Content -Raw $tempFile
        if ($status -ne $ExpectedStatus) {
            throw "$Method $Path returned HTTP $status; expected $ExpectedStatus. Body: $content"
        }
        if ($content) {
            return $content | ConvertFrom-Json
        }
    }
    finally {
        Remove-Item -LiteralPath $tempFile -Force
    }
}

function Assert-Equal {
    param([object]$Actual, [object]$Expected, [string]$Description)
    if ($Actual -ne $Expected) {
        throw "$Description`: got '$Actual', expected '$Expected'"
    }
}

$suffix = [guid]::NewGuid().ToString("N")
$roleName = "registry-smoke-$suffix"
$roleSpec = @{
    description = "Registry smoke test role."
    system_prompt_ref = "packages/ontology/prompts/smoke.yaml"
    inputs = @(@{ name = "request"; type = "document" })
    outputs = @(@{ name = "result"; type = "document" })
    tools = @{ allow = @("fs.read") }
    model = @{ capability = "code-strong" }
    limits = @{ max_steps = 10; max_cost_usd = 1.0 }
    autonomy = "balanced"
}
$role = Invoke-Registry POST "/api/v1/registry/roles" 201 $headers @{
    apiVersion = "ai-sdlc-factory/v1"
    kind = "Role"
    metadata = @{ name = $roleName; category = "engineering" }
    spec = $roleSpec
}
Assert-Equal $role.version "1.0.0" "new role version"
Assert-Equal $role.status "draft" "new role status"
Invoke-Registry GET "/api/v1/registry/roles/$($role.id)" 200 $headers | Out-Null

$roleSpec.description = "Updated draft role."
Invoke-Registry PATCH "/api/v1/registry/roles/$($role.id)" 200 $headers @{ spec = $roleSpec } | Out-Null

$selfApprovalHeaders = $headers.Clone()
$selfApprovalHeaders["X-Approver-ID"] = $authorId
Invoke-Registry PATCH "/api/v1/registry/roles/$($role.id)/status" 403 $selfApprovalHeaders @{ status = "approved" } | Out-Null

$approvalHeaders = $headers.Clone()
$approvalHeaders["X-Approver-ID"] = $approverId
Invoke-Registry PATCH "/api/v1/registry/roles/$($role.id)/status" 200 $approvalHeaders @{ status = "approved" } | Out-Null
Invoke-Registry PATCH "/api/v1/registry/roles/$($role.id)" 405 $headers @{ spec = $roleSpec } | Out-Null
$roleClone = Invoke-Registry POST "/api/v1/registry/roles/$($role.id)/clone" 201 $headers
Assert-Equal $roleClone.version "1.1.0" "cloned role version"
$roleRevert = Invoke-Registry POST "/api/v1/registry/roles/$($roleClone.id)/revert" 201 $headers
Assert-Equal $roleRevert.version "1.2.0" "reverted role version"
Assert-Equal $roleRevert.spec.description "Updated draft role." "reverted role spec"

$team = Invoke-Registry POST "/api/v1/registry/teams" 201 $headers @{
    apiVersion = "ai-sdlc-factory/v1"
    kind = "Team"
    metadata = @{ name = "registry-smoke-team-$suffix" }
    spec = @{
        description = "Registry smoke team."
        topology = "orchestrated"
        members = @(@{ role_name = $roleName; role_version = "1.0.0"; count = 1 })
        budget = @{ max_cost_usd = 2.0; max_steps = 20 }
        review_matrix = @{ required_reviewers = 1 }
    }
}
Assert-Equal $team.version "1.0.0" "new team version"

$process = Invoke-Registry POST "/api/v1/registry/processes" 201 $headers @{
    apiVersion = "ai-sdlc-factory/v1"
    kind = "Process"
    metadata = @{ name = "registry-smoke-process-$suffix" }
    spec = @{
        description = "Registry smoke process."
        stages = @(@{ name = "Idea"; order = 0; gate = "requirements_approved" })
    }
}
Assert-Equal $process.version "1.0.0" "new process version"

Write-Host "Registry smoke test passed: roles, teams, processes, versioning, immutability, and approval flow."
