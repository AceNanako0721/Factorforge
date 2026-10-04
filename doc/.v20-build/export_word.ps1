$ErrorActionPreference = 'Stop'
$stagePath = 'runtime/doc-build/factorforge-v20'
$items = Get-Content -LiteralPath (Join-Path $stagePath 'manifest.json') -Raw -Encoding utf8 | ConvertFrom-Json
$wordInstance = $null
$taskDocument = $null
try {
    $wordInstance = New-Object -ComObject Word.Application
    $wordInstance.Visible = $false
    $wordInstance.DisplayAlerts = 0
    foreach ($item in $items) {
        $taskDocument = $wordInstance.Documents.Open($item.local_docx, $false, $false)
        $taskDocument.Fields.Update() | Out-Null
        $taskDocument.Repaginate()
        $taskDocument.Save()
        $taskDocument.ExportAsFixedFormat((Join-Path $stagePath ($item.slug + '.pdf')), 17)
        $pageCount = $taskDocument.ComputeStatistics(2)
        Write-Output ($item.slug + ': ' + $pageCount + ' pages exported')
        $taskDocument.Close(0)
        [System.Runtime.InteropServices.Marshal]::FinalReleaseComObject($taskDocument) | Out-Null
        $taskDocument = $null
        Copy-Item -LiteralPath $item.local_docx -Destination $item.output_docx
    }
} finally {
    if ($null -ne $taskDocument) { $taskDocument.Close(0) }
    if ($null -ne $wordInstance) {
        $wordInstance.Quit()
        [System.Runtime.InteropServices.Marshal]::FinalReleaseComObject($wordInstance) | Out-Null
    }
}
