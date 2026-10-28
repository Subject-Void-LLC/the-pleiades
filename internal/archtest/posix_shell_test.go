// Every test that hands commands to remoteexectest's SSH server says it
// needs a POSIX shell, so a run on Windows skips it by name rather than
// failing it.
package archtest

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// shellFreeServers names each test function, as "path:function", that
// starts remoteexectest's server and sends it no command, so it runs
// wherever the server does, with why.
var shellFreeServers = map[string]string{
	"internal/engine/console_dial_test.go:newConsoleFixture":                                                                   "a scripted CML console server answers the shell request; no exec session is opened",
	"cmd/pleiades/upgrade_ios_example_test.go:scriptedSwitch":                                                                  "a scripted IOS CLI; wait.connection opens a terminal on it, never an exec session",
	"internal/catalog/net/cli/cli_session_internal_test.go:scriptedDevice":                                                     "a scripted CLI answers the shell request; no exec session is opened",
	"internal/catalog/net/cli/cli_session_internal_test.go:TestCommand_AnswersTheQuestionsTheTaskLists":                        "a scripted CLI that asks a question; no exec session is opened",
	"internal/catalog/net/ios/command_session_internal_test.go:askingDevice":                                                   "a scripted IOS CLI that asks questions; no exec session is opened",
	"internal/catalog/net/ios/command_output_internal_test.go:outputDevice":                                                    "a scripted IOS CLI printing show output and a banner prompt; no exec session is opened",
	"cmd/pleiades/secret_ref_test.go:secretRouter":                                                                             "a scripted IOS CLI whose CA start asks for a passphrase; no exec session is opened",
	"internal/catalog/net/ios/config_session_internal_test.go:configuringDevice":                                               "a scripted IOS CLI in configuration mode that asks a question; no exec session is opened",
	"internal/catalog/net/ios/ios_session_internal_test.go:iosDevice":                                                          "a scripted IOS CLI answers the shell request; no exec session is opened",
	"internal/catalog/net/ios/config_backup_internal_test.go:backupDevice":                                                     "a scripted IOS CLI in configuration mode showing its running configuration; no exec session is opened",
	"internal/catalog/wait/connection_test.go:TestConnection_ANetworkDeviceNotLettingATerminalInIsNotReady":                    "a scripted network CLI with no session budget; nothing is ever run",
	"internal/catalog/wait/connection_test.go:startCLIDevice":                                                                  "a scripted network CLI; a try opens a terminal and no exec session",
	"internal/catalog/net/ios/ios_session_internal_test.go:TestOpenSession_FailsWhenNoPromptEverArrives":                       "a scripted CLI that never prompts; no exec session is opened",
	"internal/catalog/net/netconf/netconf_session_internal_test.go:scriptedServer":                                             "a scripted NETCONF subsystem; no exec session is opened",
	"internal/catalog/net/netconf/netconf_session_internal_test.go:TestOpenSession_FailsWhenTheDeviceOffersNoNetconfSubsystem": "asks only for a subsystem the server lacks",
	"internal/engine/route_dial_test.go:newRouteFixture":                                                                       "a bastion forwarding to a local echo listener",
	"internal/hosttrust/hosttrust_test.go:TestScan":                                                                            "reads the host key and never logs in",
	"internal/inventory/onboard/probe_kubernetes_onboard_live_test.go:TestKubernetesOnboard_NodesAndRoutes":                    "a bastion forwarding to the k3s API",
	"internal/transport/serialtcp/serialtcp_test.go:TestExec_TunnelsThroughABastionWhenRouteIsSet":                             "a bastion forwarding to a console server",
	"internal/transport/serialtcp/serialtcp_test.go:TestExec_UnreachableConsoleServerThroughBastionFailsWithChannelError":      "a bastion whose forward fails",
	"internal/transport/telnet/telnet_test.go:TestExec_TunnelsThroughABastionWhenRouteIsSet":                                   "a bastion forwarding to a telnet device",
	"internal/transport/telnet/telnet_test.go:TestExec_UnreachableDeviceThroughBastionFailsWithChannelError":                   "a bastion whose forward fails",
	"pkg/remotefile/predict_test.go:TestDirectoryEmpty_ReportsATransportFailure":                                               "a session budget of zero refuses every command before a shell starts",
}

// TestEveryShellServerTestNeedsAPOSIXShell holds every test function that
// starts pkg/remoteexec/remoteexectest's server to calling
// need.POSIXShell, since the server runs each command it is sent through
// /bin/sh, which Windows has none of. #73 added the call to the packages
// its own Windows run reached, and the next branch's run failed 36 tests
// in three packages that had not been in that scope (FAILURE_PATTERNS
// 517): a package fails on Windows the first time a branch changes it
// unless every such test says what it needs. A function that runs no
// command (a bastion that only forwards, a scripted CLI or NETCONF
// device) is named in shellFreeServers with why.
func TestEveryShellServerTestNeedsAPOSIXShell(t *testing.T) {
	root := repoRoot(t)
	exempted := map[string]bool{}
	var starts int
	for _, dir := range []string{"cmd", "internal", "pkg", "tools"} {
		walkGoFiles(t, filepath.Join(root, dir), func(path string, file *ast.File, fset *token.FileSet) {
			if !strings.HasSuffix(path, "_test.go") {
				return
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatalf("resolving %s: %v", path, err)
			}
			rel = filepath.ToSlash(rel)
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				start, guarded := shellServerCalls(fn.Body)
				if start == token.NoPos {
					continue
				}
				starts++
				key := rel + ":" + fn.Name.Name
				if _, ok := shellFreeServers[key]; ok {
					exempted[key] = true
					continue
				}
				if !guarded {
					t.Errorf("%s:%d: %s starts remoteexectest's server, which runs every command through /bin/sh, "+
						"and never calls need.POSIXShell(t), so on Windows it fails rather than skipping; call it "+
						"before the server starts, or name the function in shellFreeServers with why it sends no command",
						rel, fset.Position(start).Line, fn.Name.Name)
				}
			}
		})
	}
	for key := range shellFreeServers {
		if !exempted[key] {
			t.Errorf("shellFreeServers names %s, which no longer starts remoteexectest's server: remove it", key)
		}
	}
	if starts == 0 {
		t.Fatal("no test function starts remoteexectest's server, so this test proved nothing: has the package moved?")
	}
	t.Logf("%d test function(s) start remoteexectest's server", starts)
}

// shellServerCalls reports where body starts remoteexectest's server, and
// whether it calls need.POSIXShell, closures included.
func shellServerCalls(body *ast.BlockStmt) (start token.Pos, guarded bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case pkg.Name == "remoteexectest" && sel.Sel.Name == "Start" && start == token.NoPos:
			start = call.Pos()
		case pkg.Name == "need" && sel.Sel.Name == "POSIXShell":
			guarded = true
		}
		return true
	})
	return start, guarded
}
