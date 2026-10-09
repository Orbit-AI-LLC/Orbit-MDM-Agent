// The Orbit agent's macOS menu-bar app.
//
// A small status item in the signed-in person's menu bar that reads the
// world-readable status file the agent service writes each check-in
// (/Library/Orbit/status.json, internal/status in the Go repository) and shows,
// from it: whether the computer is healthy, when it last checked in, any
// problems the person can act on — above all the macOS privacy permissions the
// agent still needs, each with a button that opens the right System Settings
// pane — and the organization's help desk, so whoever is at the computer knows
// who to call.
//
// It holds no secrets and does nothing privileged; the service, running as root,
// is what manages the computer. Build (no Xcode licence needed, like
// scripts/orbitmark.swift):
//
//   swiftc -O -o OrbitAgentMenu menubar/macos/OrbitAgentMenu.swift
//
// packaging/macos puts it in Orbit Agent.app and loads it as a per-user
// LaunchAgent.
import AppKit
import Foundation

// MARK: - The status file (mirrors internal/status/status.go)

struct Support: Codable {
    var name: String?
    var phone: String?
    var email: String?
    var url: String?
    var isEmpty: Bool { (name ?? "").isEmpty && (phone ?? "").isEmpty && (email ?? "").isEmpty && (url ?? "").isEmpty }
}

struct Issue: Codable {
    var id: String
    var severity: String
    var title: String
    var detail: String?
    var fix: String?
}

struct Status: Codable {
    var version: String?
    var hostname: String?
    var os: String?
    var enrolled: Bool?
    var server: String?
    var last_checkin: String?
    var last_checkin_ok: Bool?
    var healthy: Bool?
    var issues: [Issue]?
    var permissions: [String: String]?
    var support: Support?
}

// statusPath is where the service writes the file, matching config.StatusPath in
// the Go agent (ORBIT_AGENT_HOME overrides the folder, for testing side by side).
func statusPath() -> String {
    if let home = ProcessInfo.processInfo.environment["ORBIT_AGENT_HOME"], !home.isEmpty {
        return (home as NSString).appendingPathComponent("status.json")
    }
    return "/Library/Orbit/status.json"
}

func readStatus() -> Status? {
    guard let data = FileManager.default.contents(atPath: statusPath()) else { return nil }
    return try? JSONDecoder().decode(Status.self, from: data)
}

// MARK: - The app

final class AppDelegate: NSObject, NSApplicationDelegate, NSMenuDelegate {
    private var item: NSStatusItem!
    private var timer: Timer?

    func applicationDidFinishLaunching(_ notification: Notification) {
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        let menu = NSMenu()
        menu.delegate = self
        item.menu = menu
        refreshIcon()
        // Keep the icon's health colour current even while the menu is closed.
        timer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in self?.refreshIcon() }
    }

    // MARK: Icon

    private func refreshIcon() {
        guard let button = item.button else { return }
        let status = readStatus()
        let worst = severity(of: status)
        let symbol: String
        switch worst {
        case .error: symbol = "exclamationmark.triangle.fill"
        case .warning: symbol = "exclamationmark.circle.fill"
        case .none: symbol = "display"
        }
        let image = NSImage(systemSymbolName: symbol, accessibilityDescription: "Orbit agent")
        image?.isTemplate = (worst == .none)
        button.image = image
        switch worst {
        case .error: button.contentTintColor = .systemRed
        case .warning: button.contentTintColor = .systemOrange
        case .none: button.contentTintColor = nil
        }
        button.toolTip = status == nil ? "Orbit agent: no status yet" : "Orbit agent"
    }

    private enum Level { case none, warning, error }

    private func severity(of status: Status?) -> Level {
        guard let status = status else { return .warning }
        var level = Level.none
        for issue in status.issues ?? [] {
            if issue.severity == "error" { return .error }
            if issue.severity == "warning" { level = .warning }
        }
        if status.last_checkin_ok == false && level == .none { level = .warning }
        return level
    }

    // MARK: Menu, rebuilt each time it opens

    func menuNeedsUpdate(_ menu: NSMenu) {
        menu.removeAllItems()
        let status = readStatus()
        guard let status = status else {
            menu.addItem(disabled("Orbit agent — no status yet"))
            menu.addItem(disabled("Waiting for the first check-in…"))
            addFooter(menu, status: nil)
            return
        }

        // Health header.
        let host = status.hostname ?? "This computer"
        let healthy = status.healthy ?? true
        let header = NSMenuItem(title: host, action: nil, keyEquivalent: "")
        header.isEnabled = false
        menu.addItem(header)
        menu.addItem(disabled(healthy ? "● Healthy" : "● Needs attention",
                              color: healthy ? .systemGreen : .systemRed))
        if let last = relativeCheckin(status) {
            menu.addItem(disabled(last, color: status.last_checkin_ok == false ? .systemOrange : nil))
        }

        // Problems, each with a one-click fix where we have one.
        let issues = status.issues ?? []
        if !issues.isEmpty {
            menu.addItem(.separator())
            menu.addItem(sectionLabel("Needs attention"))
            for issue in issues {
                menu.addItem(issueItem(issue))
                if let detail = issue.detail, !detail.isEmpty {
                    menu.addItem(disabled("   " + detail, small: true))
                }
            }
        }

        // Help desk.
        if let support = status.support, !support.isEmpty {
            menu.addItem(.separator())
            menu.addItem(sectionLabel(support.name?.isEmpty == false ? support.name! : "Get help"))
            if let phone = support.phone, !phone.isEmpty {
                menu.addItem(linkItem("Call " + phone, url: telURL(phone)))
            }
            if let email = support.email, !email.isEmpty {
                menu.addItem(linkItem("Email " + email, url: "mailto:" + email))
            }
            if let url = support.url, !url.isEmpty {
                menu.addItem(linkItem("Open support site", url: url))
            }
        }

        addFooter(menu, status: status)
    }

    private func addFooter(_ menu: NSMenu, status: Status?) {
        menu.addItem(.separator())
        let refresh = NSMenuItem(title: "Refresh", action: #selector(refresh), keyEquivalent: "r")
        refresh.target = self
        menu.addItem(refresh)
        var about = "Orbit agent"
        if let v = status?.version, !v.isEmpty { about += " \(v)" }
        menu.addItem(disabled(about, small: true))
        let quit = NSMenuItem(title: "Quit", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        menu.addItem(quit)
    }

    // MARK: Menu item builders

    private func disabled(_ title: String, color: NSColor? = nil, small: Bool = false) -> NSMenuItem {
        let mi = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        mi.isEnabled = false
        var attrs: [NSAttributedString.Key: Any] = [:]
        if small { attrs[.font] = NSFont.menuFont(ofSize: NSFont.smallSystemFontSize) }
        if let color = color { attrs[.foregroundColor] = color }
        if !attrs.isEmpty { mi.attributedTitle = NSAttributedString(string: title, attributes: attrs) }
        return mi
    }

    private func sectionLabel(_ title: String) -> NSMenuItem {
        let mi = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        mi.isEnabled = false
        mi.attributedTitle = NSAttributedString(string: title.uppercased(), attributes: [
            .font: NSFont.systemFont(ofSize: NSFont.smallSystemFontSize, weight: .semibold),
            .foregroundColor: NSColor.secondaryLabelColor,
        ])
        return mi
    }

    private func issueItem(_ issue: Issue) -> NSMenuItem {
        let mi = NSMenuItem(title: issue.title, action: nil, keyEquivalent: "")
        let dot = issue.severity == "error" ? "🔴 " : (issue.severity == "warning" ? "🟠 " : "🔵 ")
        if let pane = settingsPane(for: issue.fix) {
            mi.title = dot + issue.title + " — Fix…"
            mi.representedObject = pane
            mi.action = #selector(openRepresented(_:))
            mi.target = self
        } else {
            mi.attributedTitle = NSAttributedString(string: dot + issue.title)
            mi.isEnabled = false
        }
        return mi
    }

    private func linkItem(_ title: String, url: String) -> NSMenuItem {
        let mi = NSMenuItem(title: title, action: #selector(openRepresented(_:)), keyEquivalent: "")
        mi.representedObject = url
        mi.target = self
        return mi
    }

    // settingsPane maps a fix action to the System Settings privacy pane URL.
    private func settingsPane(for fix: String?) -> String? {
        switch fix {
        case "open_screen_recording":
            return "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"
        case "open_full_disk_access":
            return "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"
        case "open_accessibility":
            return "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"
        default:
            return nil
        }
    }

    // MARK: Actions

    @objc private func openRepresented(_ sender: NSMenuItem) {
        guard let s = sender.representedObject as? String, let url = URL(string: s) else { return }
        NSWorkspace.shared.open(url)
    }

    @objc private func refresh() { refreshIcon() }

    // MARK: Helpers

    private func telURL(_ phone: String) -> String {
        let digits = phone.filter { $0.isNumber || $0 == "+" }
        return "tel:" + digits
    }

    private func relativeCheckin(_ status: Status) -> String? {
        guard let raw = status.last_checkin, !raw.isEmpty else {
            return status.last_checkin_ok == false ? "Can't reach the server" : nil
        }
        let f = ISO8601DateFormatter()
        guard let date = f.date(from: raw) else { return nil }
        let secs = Int(Date().timeIntervalSince(date))
        let ago: String
        switch secs {
        case ..<90: ago = "just now"
        case ..<3600: ago = "\(secs / 60) min ago"
        case ..<86400: ago = "\(secs / 3600) h ago"
        default: ago = "\(secs / 86400) d ago"
        }
        return (status.last_checkin_ok == false ? "Last check-in " : "Checked in ") + ago
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
let delegate = AppDelegate()
app.delegate = delegate
app.run()
