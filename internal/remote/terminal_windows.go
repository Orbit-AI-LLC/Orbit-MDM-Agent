//go:build windows

package remote

import (
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// winTerminal is a shell on a Windows pseudo console (ConPTY), which the agent
// runs as SYSTEM. The console reads its input from one pipe and writes its
// output to another; we hold the other ends.
type winTerminal struct {
	console windows.Handle
	in      *os.File // we write keystrokes here
	out     *os.File // we read the console's output here
	process windows.Handle
	thread  windows.Handle
	once    sync.Once
	done    chan struct{}
	code    int
}

// handleValue reinterprets a console handle's bits as the attribute value the
// pseudo console attribute wants (the HPCON passed by value). Going through a
// real pointer avoids a uintptr→Pointer conversion; the handle is a kernel
// handle, not Go-managed memory, so no object moves under it.
func handleValue(h *windows.Handle) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(h))
}

func openTerminal(shell string, cols, rows uint16) (terminal, error) {
	command := map[string]string{
		"cmd":        "cmd.exe",
		"powershell": "powershell.exe -NoLogo",
	}[shell]
	if command == "" {
		command = "cmd.exe"
	}

	// Two pipes: the console reads inRead, we write inWrite; the console
	// writes outWrite, we read outRead.
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err := windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, err
	}
	if err := windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		return nil, err
	}

	var console windows.Handle
	err := windows.CreatePseudoConsole(windows.Coord{X: int16(cols), Y: int16(rows)}, inRead, outWrite, 0, &console)
	// The pseudo console duplicated the ends it uses; we no longer need them.
	windows.CloseHandle(inRead)
	windows.CloseHandle(outWrite)
	if err != nil {
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, fmt.Errorf("couldn't open a console: %w", err)
	}

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, err
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, handleValue(&console), unsafe.Sizeof(console)); err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, err
	}

	si := new(windows.StartupInfoEx)
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.ProcThreadAttributeList = attrs.List()
	si.Flags |= windows.STARTF_USESTDHANDLES

	command16, err := windows.UTF16PtrFromString(command)
	if err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, err
	}
	pi := new(windows.ProcessInformation)
	err = windows.CreateProcess(nil, command16, nil, nil, false,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT,
		nil, nil, &si.StartupInfo, pi)
	if err != nil {
		windows.ClosePseudoConsole(console)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		return nil, fmt.Errorf("couldn't start %s: %w", command, err)
	}

	t := &winTerminal{
		console: console,
		in:      os.NewFile(uintptr(inWrite), "orbit-pty-in"),
		out:     os.NewFile(uintptr(outRead), "orbit-pty-out"),
		process: pi.Process,
		thread:  pi.Thread,
		done:    make(chan struct{}),
	}
	go func() {
		windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		var code uint32
		if windows.GetExitCodeProcess(pi.Process, &code) == nil {
			t.code = int(code)
		}
		close(t.done)
	}()
	return t, nil
}

func (t *winTerminal) Read(p []byte) (int, error)  { return t.out.Read(p) }
func (t *winTerminal) Write(p []byte) (int, error) { return t.in.Write(p) }

func (t *winTerminal) Resize(cols, rows uint16) error {
	return windows.ResizePseudoConsole(t.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}

func (t *winTerminal) Wait() int {
	<-t.done
	return t.code
}

func (t *winTerminal) Close() error {
	t.once.Do(func() {
		// Closing the console ends the shell; then free everything.
		windows.ClosePseudoConsole(t.console)
		_ = t.in.Close()
		_ = t.out.Close()
		windows.TerminateProcess(t.process, 1)
		windows.CloseHandle(t.thread)
		windows.CloseHandle(t.process)
	})
	return nil
}
