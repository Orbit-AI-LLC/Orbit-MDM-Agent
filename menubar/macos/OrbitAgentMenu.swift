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
    // When each Settings pane was last opened for a missing permission, so the
    // nudge below doesn't reopen it on every wake (see requestMissingPermissions).
    private var lastAsked: [String: Date] = [:]

    func applicationDidFinishLaunching(_ notification: Notification) {
        item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        let menu = NSMenu()
        menu.delegate = self
        item.menu = menu
        refreshIcon()
        // Keep the icon's health colour current even while the menu is closed.
        timer = Timer.scheduledTimer(withTimeInterval: 15, repeats: true) { [weak self] _ in self?.refreshIcon() }
        // At launch (so each login, and each self-update, which reloads the app)
        // ask for whatever is still missing.
        requestMissingPermissions()
        // Do the same the moment the Mac wakes or someone signs in, and refresh so a
        // permission the service is re-asking for (internal/agent watchPermissions)
        // shows as resolved without waiting for the next tick.
        let wsn = NSWorkspace.shared.notificationCenter
        for name in [NSWorkspace.didWakeNotification, NSWorkspace.sessionDidBecomeActiveNotification, NSWorkspace.screensDidWakeNotification] {
            wsn.addObserver(self, selector: #selector(wakeOrLogin), name: name, object: nil)
        }
        // Re-ink the mark promptly when the Light/Dark setting changes (the timer
        // above also catches a wallpaper change that darkens the bar on its own).
        DistributedNotificationCenter.default().addObserver(self, selector: #selector(refresh),
            name: NSNotification.Name("AppleInterfaceThemeChangedNotification"), object: nil)
    }

    // wakeOrLogin refreshes the icon and re-asks for any missing permission when the
    // Mac wakes or someone signs in.
    @objc private func wakeOrLogin() {
        refreshIcon()
        requestMissingPermissions()
    }

    // requestMissingPermissions takes the person to the right System Settings pane
    // for each permission that's still denied. macOS only raises an on-screen prompt
    // for a foreground app in the signed-in user's session, never for the root
    // service — so for both Full Disk Access (which has no prompt at all) and Screen
    // Recording (whose prompt the daemon can't trigger) the reliable way to ask is
    // to open the pane for the person to switch Orbit Agent on. Accessibility is left
    // out until the remote-control feature that needs it ships. To avoid reopening
    // Settings on every wake, each pane is opened at most once an hour, and never
    // once the permission is granted.
    private func requestMissingPermissions() {
        guard let perms = readStatus()?.permissions else { return }
        // Permission → the Settings pane to open for it.
        let settingsOnly = [
            "full_disk_access": "open_full_disk_access",
            "screen_recording": "open_screen_recording",
        ]
        for (permission, fix) in settingsOnly {
            guard perms[permission] == "denied", let pane = settingsPane(for: fix) else { continue }
            if let last = lastAsked[permission], Date().timeIntervalSince(last) < 3600 { continue }
            lastAsked[permission] = Date()
            if let url = URL(string: pane) { NSWorkspace.shared.open(url) }
        }
    }

    // MARK: Icon

    private func refreshIcon() {
        guard let button = item.button else { return }
        let status = readStatus()
        let worst = severity(of: status)
        // Colour the mark ourselves instead of using a template image: on recent
        // macOS a template inks to the system Light/Dark setting rather than the
        // menu bar's real appearance, so in Light mode over a dark wallpaper — a
        // dark-looking bar — it came out black and vanished. We pick the ink from
        // the status item's own appearance, which does follow the bar, and turn it
        // amber or red only when something needs attention.
        let ink: NSColor
        switch worst {
        case .error: ink = .systemRed
        case .warning: ink = .systemOrange
        case .none: ink = Self.menuBarIsDark(button) ? .white : .black
        }
        button.image = Self.markImage(ink)
        button.contentTintColor = nil
        button.toolTip = status == nil ? "Orbit agent: no status yet" : "Orbit agent"
    }

    // menuBarIsDark reports whether the bar behind the status item is dark, so the
    // mark can be inked to contrast with it. It reads the status item's own
    // appearance, which follows the menu bar (dark over a dark wallpaper even in
    // Light mode) — unlike a template image, which on recent macOS inked to the
    // system Light/Dark setting instead and left a black mark on a dark bar.
    private static func menuBarIsDark(_ button: NSStatusBarButton) -> Bool {
        button.effectiveAppearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
    }

    // markImage draws the Orbit RMM mark (a monitor with a heartbeat, in orbit) in
    // one colour at the menu-bar size — in place of a bare SF Symbol, which read as
    // an anonymous red "!". The glyph is the mark's "small" form, a path in a
    // 100-unit box with y running down, generated with
    //   orbitmark path rmm 100 0.82 small
    // from scripts/orbitmark.swift (the Orbit family mark renderer); regenerate the
    // path there, never edit it by hand.
    private static func markImage(_ color: NSColor) -> NSImage {
        let box: CGFloat = 20
        let image = NSImage(size: NSSize(width: box, height: box))
        image.lockFocus()
        if let ctx = NSGraphicsContext.current?.cgContext {
            let raw = AppDelegate.markPath
            let b = raw.boundingBoxOfPath
            let inset: CGFloat = 0.98
            let s = min(box / b.width, box / b.height) * inset
            let ox = (box - b.width * s) / 2
            let oy = (box - b.height * s) / 2
            // The box runs y down; flip it and fit the glyph's bounds to the icon.
            var t = CGAffineTransform(translationX: ox, y: oy)
                .scaledBy(x: s, y: -s)
                .translatedBy(x: -b.minX, y: -b.maxY)
            if let fitted = raw.copy(using: &t) {
                ctx.addPath(fitted)
                ctx.setFillColor(color.cgColor)
                ctx.fillPath()
            }
        }
        image.unlockFocus()
        return image
    }

    // markPath parses the embedded glyph (absolute SVG M/L/Q/C/Z commands) into a
    // CGPath in the 100-unit box it was generated in.
    private static let markPath: CGPath = parsePath(markPathData)

    private static func parsePath(_ d: String) -> CGPath {
        let path = CGMutablePath()
        var nums: [CGFloat] = []
        var cmd: Character = " "
        var token = ""
        func flush() { if !token.isEmpty { nums.append(CGFloat(Double(token) ?? 0)); token = "" } }
        func p(_ i: Int) -> CGPoint { CGPoint(x: nums[i], y: nums[i + 1]) }
        func apply() {
            flush()
            switch cmd {
            case "M": path.move(to: p(0))
            case "L": path.addLine(to: p(0))
            case "Q": path.addQuadCurve(to: p(2), control: p(0))
            case "C": path.addCurve(to: p(4), control1: p(0), control2: p(2))
            case "Z", "z": path.closeSubpath()
            default: break
            }
            nums.removeAll(keepingCapacity: true)
        }
        for ch in d {
            if ch.isLetter { apply(); cmd = ch }
            else if ch == " " || ch == "\n" || ch == "," { flush() }
            else { token.append(ch) }
        }
        apply()
        return path
    }

    private static let markPathData = "M53.85 71.57L62.13 71.57C63.32 71.57 64.3 72.47 64.41 73.63L64.43 73.86C64.43 75.13 63.4 76.15 62.13 76.15L36.49 76.15C35.31 76.15 34.33 75.25 34.21 74.09L34.2 73.86C34.2 72.99 34.69 72.24 35.4 71.85L36.64 71.57L44.78 71.57L44.78 69.44C47.8 68.55 50.84 67.51 53.85 66.35ZM84.2 40.85C85.42 42.12 85.38 44.13 84.12 45.35C73.22 55.89 49.59 66.62 30.98 69.58C21.31 71.11 14.26 70.39 10.76 67.08C9.96 66.33 9.38 65.46 9 64.53L14.9 62.14C14.95 62.26 15.03 62.36 15.13 62.46C15.79 63.08 17.31 63.62 19.6 63.86C22.3 64.13 25.82 63.96 29.98 63.3C47.37 60.53 69.8 50.34 79.7 40.78C80.96 39.55 82.98 39.59 84.2 40.85ZM79.54 57.06C79.54 59.8 77.31 62.03 74.57 62.03L63.9 62.03C69.69 59.27 75.06 56.19 79.54 52.98ZM79.54 28.81L79.54 37.32C78.83 37.61 78.17 38.03 77.59 38.59C72.58 43.44 64.06 48.51 54.66 52.58C54.82 52.41 54.96 52.2 55.08 51.96L59.06 44.1L67.45 44.1C69.15 44.1 70.52 42.73 70.52 41.03C70.52 39.33 69.15 37.95 67.45 37.95L57.17 37.95C56.01 37.95 54.95 38.6 54.43 39.64L52.92 42.62L48.6 29.74C47.74 27.18 44.23 26.89 42.97 29.29L38.39 37.95L28.16 37.95C26.46 37.95 25.08 39.33 25.08 41.03C25.08 42.73 26.46 44.1 28.16 44.1L40.25 44.1C41.39 44.1 42.43 43.47 42.97 42.46L45.05 38.51L49.42 51.55C49.86 52.86 50.99 53.58 52.16 53.64C44.59 56.76 36.59 59.18 29.51 60.3C26.15 60.83 23.23 61.04 20.93 60.92C19.81 60.01 19.09 58.62 19.09 57.06L19.09 28.81C19.09 26.07 21.31 23.85 24.05 23.85L74.57 23.85C77.31 23.85 79.54 26.07 79.54 28.81ZM48.44 38.8L48.36 38.83L47.93 37.55C47.08 35.02 43.62 34.74 42.37 37.1L40.28 41.05C40.28 41.06 40.26 41.07 40.25 41.07L28.57 41.07L28.73 40.99L38.39 40.99C39.52 40.99 40.55 40.36 41.08 39.37L44.08 33.67C44.85 33.35 45.63 33.04 46.42 32.76ZM91 31.1C91 34.47 88.27 37.2 84.9 37.2C84.08 37.2 83.29 37.04 82.57 36.74L82.57 28.81C82.57 27.73 82.36 26.7 81.96 25.76C82.83 25.28 83.84 25 84.9 25C88.27 25 91 27.73 91 31.1Z"

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
