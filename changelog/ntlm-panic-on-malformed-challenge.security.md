The NTLM library WinRM authenticates with is updated past a version that could panic on a
malformed challenge (GO-2026-5543), which `exec.winrm.shell` reaches on every task and
`http.request` reaches against any server that answers with an NTLM challenge.
