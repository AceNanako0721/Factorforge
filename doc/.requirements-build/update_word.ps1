$ErrorActionPreference = 'Stop'
$stagePath = 'runtime/doc-build/requirements-build'
$wordInstance = $null
$taskDocument = $null
try {
    $wordInstance = New-Object -ComObject Word.Application
    $wordInstance.Visible = $false
    $wordInstance.DisplayAlerts = 0
    $taskDocument = $wordInstance.Documents.Open((Join-Path $stagePath 'requirements.docx'), $false, $false)
    $taskDocument.Fields.Update() | Out-Null
    foreach ($tocItem in $taskDocument.TablesOfContents) { $tocItem.Update() }
    $taskDocument.Repaginate()
    foreach ($tocItem in $taskDocument.TablesOfContents) { $tocItem.UpdatePageNumbers() }
    $taskDocument.Save()
    $tocText = $taskDocument.TablesOfContents.Item(1).Range.Text
    [System.IO.File]::WriteAllText((Join-Path $stagePath 'toc.txt'), $tocText, [System.Text.UTF8Encoding]::new($false))
    $taskDocument.ExportAsFixedFormat((Join-Path $stagePath 'requirements.pdf'), 17)
    $pageCount = $taskDocument.ComputeStatistics(2)
    Write-Output "Word updated and exported; page count: $pageCount"
} finally {
    if ($null -ne $taskDocument) { $taskDocument.Close(0) }
    if ($null -ne $wordInstance) { $wordInstance.Quit() }
}
