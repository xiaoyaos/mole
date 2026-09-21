Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()

$configDirectory = Join-Path $env:LOCALAPPDATA "Mole"
$configPath = Join-Path $configDirectory "launcher.json"
$clientPath = Join-Path $PSScriptRoot "mole-client.exe"
$script:stopFile = $null
$saved = @{}
if (Test-Path $configPath) {
    try { $saved = Get-Content $configPath -Raw | ConvertFrom-Json } catch { $saved = @{} }
}

$form = New-Object System.Windows.Forms.Form
$form.Text = "Mole 客户端"
$form.ClientSize = New-Object System.Drawing.Size(520, 405)
$form.StartPosition = "CenterScreen"
$form.FormBorderStyle = "FixedDialog"
$form.MaximizeBox = $false
$form.Font = New-Object System.Drawing.Font("Microsoft YaHei UI", 9)

function Add-Label($text, $x, $y, $parent = $form) {
    $label = New-Object System.Windows.Forms.Label
    $label.Text = $text
    $label.Location = New-Object System.Drawing.Point($x, $y)
    $label.Size = New-Object System.Drawing.Size(135, 25)
    $parent.Controls.Add($label)
}
function Add-TextBox($x, $y, $value, $parent = $form, $password = $false) {
    $box = New-Object System.Windows.Forms.TextBox
    $box.Location = New-Object System.Drawing.Point($x, $y)
    $box.Size = New-Object System.Drawing.Size(315, 25)
    $box.Text = $value
    $box.UseSystemPasswordChar = $password
    $parent.Controls.Add($box)
    return $box
}
function Valid-Port($value, $name) {
    $number = 0
    if (-not [int]::TryParse($value, [ref]$number) -or $number -lt 1 -or $number -gt 65535) {
        [System.Windows.Forms.MessageBox]::Show("$name 应为 1 到 65535 之间的整数。", "Mole") | Out-Null
        return $null
    }
    return $number
}
function PS-Literal($value) { return "'" + ([string]$value).Replace("'", "''") + "'" }

$title = New-Object System.Windows.Forms.Label
$title.Text = "Mole"
$title.Font = New-Object System.Drawing.Font("Microsoft YaHei UI", 18, [System.Drawing.FontStyle]::Bold)
$title.Location = New-Object System.Drawing.Point(24, 18)
$title.Size = New-Object System.Drawing.Size(200, 36)
$form.Controls.Add($title)
$hint = New-Object System.Windows.Forms.Label
$hint.Text = "启动后在管理员终端中使用 list、connect、disconnect 和 exit。"
$hint.ForeColor = [System.Drawing.Color]::DimGray
$hint.Location = New-Object System.Drawing.Point(27, 57)
$hint.Size = New-Object System.Drawing.Size(465, 25)
$form.Controls.Add($hint)

Add-Label "服务器 IP 或域名" 28 96
$server = Add-TextBox 165 92 $(if ($saved.server) { $saved.server } else { "" })
Add-Label "连接端口" 28 132
$port = Add-TextBox 165 128 $(if ($saved.port) { $saved.port } else { "8080" })
$sharing = New-Object System.Windows.Forms.CheckBox
$sharing.Text = "允许其他客户端连接本机"
$sharing.Location = New-Object System.Drawing.Point(165, 164)
$sharing.Size = New-Object System.Drawing.Size(260, 25)
$sharing.Checked = [bool]$saved.sharing
$form.Controls.Add($sharing)

$advancedButton = New-Object System.Windows.Forms.Button
$advancedButton.Text = "显示高级选项"
$advancedButton.Location = New-Object System.Drawing.Point(24, 198)
$advancedButton.Size = New-Object System.Drawing.Size(125, 30)
$form.Controls.Add($advancedButton)
$advancedPanel = New-Object System.Windows.Forms.Panel
$advancedPanel.Location = New-Object System.Drawing.Point(0, 232)
$advancedPanel.Size = New-Object System.Drawing.Size(520, 112)
$advancedPanel.Visible = $false
$form.Controls.Add($advancedPanel)
Add-Label "数据转发端口" 28 6 $advancedPanel
$relay = Add-TextBox 165 2 $(if ($saved.relayPort) { $saved.relayPort } else { "18081" }) $advancedPanel
Add-Label "认证令牌" 28 42 $advancedPanel
$auth = Add-TextBox 165 38 "" $advancedPanel $true
Add-Label "共享网段" 28 78 $advancedPanel
$subnet = Add-TextBox 165 74 $(if ($saved.subnet) { $saved.subnet } else { "" }) $advancedPanel
$advancedButton.Add_Click({
    $advancedPanel.Visible = -not $advancedPanel.Visible
    $advancedButton.Text = if ($advancedPanel.Visible) { "隐藏高级选项" } else { "显示高级选项" }
})

$start = New-Object System.Windows.Forms.Button
$start.Text = "启动"
$start.Location = New-Object System.Drawing.Point(24, 350)
$start.Size = New-Object System.Drawing.Size(90, 32)
$form.Controls.Add($start)
$stop = New-Object System.Windows.Forms.Button
$stop.Text = "安全停止"
$stop.Location = New-Object System.Drawing.Point(122, 350)
$stop.Size = New-Object System.Drawing.Size(90, 32)
$form.Controls.Add($stop)
$status = New-Object System.Windows.Forms.Label
$status.Text = "填写服务器地址后即可启动"
$status.ForeColor = [System.Drawing.Color]::DimGray
$status.Location = New-Object System.Drawing.Point(225, 357)
$status.Size = New-Object System.Drawing.Size(270, 25)
$form.Controls.Add($status)

$start.Add_Click({
    $hostName = $server.Text.Trim()
    if (-not $hostName -or $hostName.Contains("://") -or $hostName.Contains("/")) {
        [System.Windows.Forms.MessageBox]::Show("服务器地址只填写 IP 或域名，不带协议和路径。", "Mole") | Out-Null
        return
    }
    $signalPort = Valid-Port $port.Text "连接端口"
    if ($null -eq $signalPort) { return }
    $dataPort = Valid-Port $relay.Text "数据转发端口"
    if ($null -eq $dataPort) { return }
    if ($signalPort -eq $dataPort) {
        [System.Windows.Forms.MessageBox]::Show("连接端口与数据转发端口不能相同。", "Mole") | Out-Null
        return
    }
    if (-not (Test-Path $clientPath)) {
        [System.Windows.Forms.MessageBox]::Show("安装目录中缺少 mole-client.exe。", "Mole") | Out-Null
        return
    }

    New-Item -ItemType Directory -Path $configDirectory -Force | Out-Null
    @{ server=$hostName; port="$signalPort"; relayPort="$dataPort"; sharing=$sharing.Checked; subnet=$subnet.Text } |
        ConvertTo-Json | Set-Content -Encoding UTF8 $configPath

    $endpointHost = if ($hostName.Contains(":") -and -not $hostName.StartsWith("[")) { "[$hostName]" } else { $hostName }
    $endpoint = "{0}:{1}" -f $endpointHost, $signalPort
    $arguments = @("-server", $endpoint, "-relay-port", "$dataPort")
    if ($sharing.Checked) { $arguments += "-allow-possess" }
    if ($subnet.Text.Trim()) { $arguments += @("-local-subnet", $subnet.Text.Trim()) }
    if ($auth.Text) { $arguments += @("-auth", $auth.Text) }
    $script:stopFile = Join-Path $env:TEMP "mole-launcher-$PID.stop"
    Remove-Item $script:stopFile -Force -ErrorAction SilentlyContinue
    $argumentText = ($arguments | ForEach-Object { PS-Literal $_ }) -join ","
    $command = '$env:MOLE_STOP_FILE=' + (PS-Literal $script:stopFile) + '; & ' +
        (PS-Literal $clientPath) + ' @(' + $argumentText +
        "); Write-Host ''; Read-Host 'Mole 已退出，按回车关闭窗口'"
    $encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    try {
        Start-Process powershell.exe -Verb RunAs -ArgumentList "-NoProfile -ExecutionPolicy Bypass -EncodedCommand $encoded"
        $status.Text = "客户端已在管理员终端启动"
    } catch {
        [System.Windows.Forms.MessageBox]::Show("无法启动管理员终端：$($_.Exception.Message)", "Mole") | Out-Null
    }
})
$stop.Add_Click({
    if (-not $script:stopFile) {
        [System.Windows.Forms.MessageBox]::Show("当前没有由此启动器启动的客户端。", "Mole") | Out-Null
        return
    }
    try {
        [IO.File]::WriteAllText($script:stopFile, "")
        $status.Text = "已请求安全停止，正在清理网络配置…"
    } catch {
        [System.Windows.Forms.MessageBox]::Show("无法发送停止请求：$($_.Exception.Message)", "Mole") | Out-Null
    }
})

[void]$form.ShowDialog()
