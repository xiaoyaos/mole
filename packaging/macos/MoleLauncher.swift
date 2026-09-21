import Cocoa

private func shellQuote(_ value: String) -> String {
    "'" + value.replacingOccurrences(of: "'", with: "'\\''") + "'"
}

private func appleScriptQuote(_ value: String) -> String {
    value.replacingOccurrences(of: "\\", with: "\\\\")
        .replacingOccurrences(of: "\"", with: "\\\"")
}

@main
final class AppDelegate: NSObject, NSApplicationDelegate {
    private let defaults = UserDefaults.standard
    private var window: NSWindow!
    private let server = NSTextField()
    private let port = NSTextField()
    private let sharing = NSButton(checkboxWithTitle: "允许其他客户端连接本机", target: nil, action: nil)
    private let relayPort = NSTextField()
    private let auth = NSSecureTextField()
    private let subnet = NSTextField()
    private let advanced = NSStackView()
    private let status = NSTextField(labelWithString: "填写服务器地址后即可启动")
    private var stopFile: URL?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        let content = NSStackView()
        content.orientation = .vertical
        content.alignment = .leading
        content.spacing = 12
        content.edgeInsets = NSEdgeInsets(top: 20, left: 22, bottom: 20, right: 22)

        let title = NSTextField(labelWithString: "Mole")
        title.font = .systemFont(ofSize: 24, weight: .semibold)
        content.addArrangedSubview(title)
        let hint = NSTextField(wrappingLabelWithString: "连接中继服务器，并在打开的终端中使用 list、connect 和 disconnect。")
        hint.textColor = .secondaryLabelColor
        content.addArrangedSubview(hint)

        server.placeholderString = "例如 203.0.113.10"
        server.stringValue = defaults.string(forKey: "server") ?? ""
        port.stringValue = defaults.string(forKey: "port") ?? "8080"
        relayPort.stringValue = defaults.string(forKey: "relayPort") ?? "18081"
        subnet.placeholderString = "留空自动检测，例如 192.168.1.0/24"
        subnet.stringValue = defaults.string(forKey: "subnet") ?? ""
        sharing.state = defaults.bool(forKey: "sharing") ? .on : .off
        content.addArrangedSubview(row("服务器 IP 或域名", server))
        content.addArrangedSubview(row("连接端口", port))
        content.addArrangedSubview(sharing)

        advanced.orientation = .vertical
        advanced.alignment = .leading
        advanced.spacing = 10
        advanced.addArrangedSubview(row("数据转发端口", relayPort))
        advanced.addArrangedSubview(row("认证令牌", auth))
        advanced.addArrangedSubview(row("共享网段", subnet))
        advanced.isHidden = true
        let advancedButton = NSButton(title: "显示高级选项", target: self, action: #selector(toggleAdvanced(_:)))
        advancedButton.bezelStyle = .inline
        content.addArrangedSubview(advancedButton)
        content.addArrangedSubview(advanced)

        let buttons = NSStackView()
        buttons.orientation = .horizontal
        buttons.spacing = 8
        let start = NSButton(title: "启动", target: self, action: #selector(startClient))
        start.keyEquivalent = "\r"
        let stop = NSButton(title: "安全停止", target: self, action: #selector(stopClient))
        buttons.addArrangedSubview(start)
        buttons.addArrangedSubview(stop)
        content.addArrangedSubview(buttons)
        status.textColor = .secondaryLabelColor
        content.addArrangedSubview(status)

        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 500, height: 430),
                          styleMask: [.titled, .closable, .miniaturizable], backing: .buffered, defer: false)
        window.title = "Mole 客户端"
        window.contentView = content
        window.center()
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }

    private func row(_ label: String, _ field: NSTextField) -> NSView {
        field.translatesAutoresizingMaskIntoConstraints = false
        field.widthAnchor.constraint(equalToConstant: 290).isActive = true
        let name = NSTextField(labelWithString: label)
        name.widthAnchor.constraint(equalToConstant: 125).isActive = true
        let row = NSStackView(views: [name, field])
        row.orientation = .horizontal
        row.spacing = 10
        return row
    }

    @objc private func toggleAdvanced(_ sender: NSButton) {
        advanced.isHidden.toggle()
        sender.title = advanced.isHidden ? "显示高级选项" : "隐藏高级选项"
    }

    private func validPort(_ text: String, name: String) -> Int? {
        guard let value = Int(text), (1...65535).contains(value) else {
            alert("\(name)应为 1 到 65535 之间的整数。")
            return nil
        }
        return value
    }

    @objc private func startClient() {
        let host = server.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !host.isEmpty, !host.contains("://"), !host.contains("/") else {
            alert("服务器地址只填写 IP 或域名，不带协议和路径。")
            return
        }
        guard let signalPort = validPort(port.stringValue, name: "连接端口"),
              let dataPort = validPort(relayPort.stringValue, name: "数据转发端口") else { return }
        guard signalPort != dataPort else {
            alert("连接端口与数据转发端口不能相同。")
            return
        }
        guard let executable = Bundle.main.url(forResource: "mole-client", withExtension: nil) else {
            alert("安装包内缺少 mole-client。")
            return
        }

        defaults.set(host, forKey: "server")
        defaults.set(String(signalPort), forKey: "port")
        defaults.set(String(dataPort), forKey: "relayPort")
        defaults.set(sharing.state == .on, forKey: "sharing")
        defaults.set(subnet.stringValue, forKey: "subnet")

        let endpoint = host.contains(":") && !host.hasPrefix("[") ? "[\(host)]:\(signalPort)" : "\(host):\(signalPort)"
        var arguments = ["-server", endpoint, "-relay-port", String(dataPort)]
        if sharing.state == .on { arguments.append("-allow-possess") }
        let subnetValue = subnet.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if !subnetValue.isEmpty { arguments += ["-local-subnet", subnetValue] }
        if !auth.stringValue.isEmpty { arguments += ["-auth", auth.stringValue] }

        let signal = FileManager.default.temporaryDirectory
            .appendingPathComponent("mole-launcher-\(ProcessInfo.processInfo.processIdentifier).stop")
        try? FileManager.default.removeItem(at: signal)
        stopFile = signal
        let command = "export MOLE_STOP_FILE=\(shellQuote(signal.path)); sudo \(shellQuote(executable.path)) " +
            arguments.map(shellQuote).joined(separator: " ") +
            "; result=$?; printf '\\nMole 已退出（状态 %s）。按回车关闭窗口。' \"$result\"; read"
        let source = "tell application \"Terminal\"\nactivate\ndo script \"\(appleScriptQuote(command))\"\nend tell"
        var error: NSDictionary?
        let result = NSAppleScript(source: source)?.executeAndReturnError(&error)
        if result == nil {
            alert("无法打开终端：\(error?[NSAppleScript.errorMessage] ?? "未知错误")")
            return
        }
        status.stringValue = "客户端已在终端启动；停止时可点击“安全停止”"
    }

    @objc private func stopClient() {
        guard let stopFile else {
            alert("当前没有由此启动器启动的客户端。")
            return
        }
        do {
            try Data().write(to: stopFile, options: .atomic)
            status.stringValue = "已请求安全停止，正在清理网络配置…"
        } catch {
            alert("无法发送停止请求：\(error.localizedDescription)")
        }
    }

    private func alert(_ message: String) {
        let alert = NSAlert()
        alert.messageText = "Mole"
        alert.informativeText = message
        alert.runModal()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
}
