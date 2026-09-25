// The three accessors that make a Server capability.WindowsShellCapable:
// where a command starts, and where the device keeps its two
// interpreters.
package windows

// WorkingDirectory returns the "working_directory" property: where a
// command run over WinRM starts. An empty value leaves the choice to
// the WinRM service (the account's profile directory), which is honest
// where a default such as C:\ would be a guess.
func (w *Server) WorkingDirectory() string {
	dir, _ := w.Properties().String("working_directory")
	return dir
}

// CmdPath returns the "cmd_path" property, defaulting to cmd.exe's
// stock location. The path is absolute on purpose: a command started
// with no shell in front of it resolves a bare program name by searching
// the working directory first, so "cmd.exe" alone could run a planted
// file of that name.
func (w *Server) CmdPath() string {
	if path, ok := w.Properties().String("cmd_path"); ok && path != "" {
		return path
	}
	return `C:\Windows\System32\cmd.exe`
}

// PowerShellPath returns the "powershell_path" property, defaulting to
// Windows PowerShell 5.1's stock location. A device that should run
// PowerShell 7 instead names pwsh.exe here. Absolute for the reason
// CmdPath gives.
func (w *Server) PowerShellPath() string {
	if path, ok := w.Properties().String("powershell_path"); ok && path != "" {
		return path
	}
	return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`
}
