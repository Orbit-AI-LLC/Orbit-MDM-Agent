import Foundation
import OrbitAgentKit

// The Orbit RMM agent for macOS.
//
//   orbit-agent install --server https://mdm.example.com --token orbe_…
//   orbit-agent run          (what the service runs)
//   orbit-agent once         (check in and run what comes back, then exit)
//   orbit-agent status
//   orbit-agent uninstall
//   orbit-agent version
//
// Windows and Linux are served by the Go binary in this repository; this is the
// macOS port. The CLI surface here is a contract: the installers and the .pkg
// postinstall invoke `install --package`, `run`, etc.

let agentVersion = "1.0.0"   // overridden at build time; see scripts/build/

func usage() {
    FileHandle.standardError.write(Data("""
    Usage: orbit-agent <command>
      install --server URL --token TOKEN [--no-service]   enroll this computer and start the service
      install --package [--server URL --token TOKEN]      what the .pkg runs
      run                                                 run the agent (the service does this)
      once                                                check in once and run what comes back
      status                                              show the enrollment
      uninstall                                           remove the agent
      version                                             print the version

    """.utf8))
}

func printStatus() -> Int32 {
    do {
        let cfg = try AgentConfig.load()
        print("Enrolled with Orbit RMM.")
        print("  server:   \(cfg.server)")
        print("  agent id: \(cfg.agentID)")
        print("  device:   \(cfg.device)")
        if let s = try? StatusFile.read(from: Paths.statusPath) {
            print("  healthy:  \(s.healthy)")
            if let last = s.lastCheckin { print("  last check-in: \(last)") }
            if !s.issues.isEmpty {
                print("  issues:")
                for i in s.issues { print("    - [\(i.severity)] \(i.title)") }
            }
        }
        return 0
    } catch AgentConfig.ConfigError.notEnrolled {
        print("This computer isn't enrolled; run orbit-agent install.")
        return 0
    } catch {
        FileHandle.standardError.write(Data("orbit-agent: \(error)\n".utf8))
        return 1
    }
}

func notYetPorted(_ command: String) -> Int32 {
    FileHandle.standardError.write(Data(
        "orbit-agent: `\(command)` is not yet available in the Swift macOS agent (in progress; the Go agent still serves production).\n".utf8))
    return 1
}

let args = Array(CommandLine.arguments.dropFirst())
guard let command = args.first else { usage(); exit(2) }

switch command {
case "version":
    print("orbit-agent \(agentVersion) (darwin/\(machineArch()))")
    exit(0)
case "status":
    exit(printStatus())
case "install", "run", "once", "uninstall":
    exit(notYetPorted(command))
default:
    usage()
    exit(2)
}

func machineArch() -> String {
    var sysinfo = utsname()
    uname(&sysinfo)
    let machine = withUnsafeBytes(of: &sysinfo.machine) { raw -> String in
        let ptr = raw.bindMemory(to: CChar.self).baseAddress!
        return String(cString: ptr)
    }
    return machine == "x86_64" ? "amd64" : (machine == "arm64" ? "arm64" : machine)
}
