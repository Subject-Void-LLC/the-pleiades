// Tests for decodeCLIXML, on error output captured from a real Windows
// host (Windows PowerShell 5.1, build 26200) through the WinRM lab
// account: each stream a script can write, plain text a native program
// wrote to the same handle, and the escapes PowerShell uses.
package winrmexec

import (
	"strings"
	"testing"
)

// objs wraps stream records in the <Objs> document PowerShell writes.
func objs(records string) string {
	return `<Objs Version="1.1.0.1" xmlns="http://schemas.microsoft.com/powershell/2004/04">` + records + `</Objs>`
}

// hostInformationRecord is Write-Host's information record as the host
// sent it, trimmed of the properties that do not affect decoding.
const hostInformationRecord = `<Obj S="information" RefId="0"><TN RefId="0"><T>System.Management.Automation.InformationRecord</T>` +
	`<T>System.Object</T></TN><ToString>from write-host</ToString><Props><Obj N="MessageData" RefId="1"><TN RefId="1">` +
	`<T>System.Management.Automation.HostInformationMessage</T><T>System.Object</T></TN><ToString>from write-host</ToString>` +
	`<Props><S N="Message">from write-host</S><B N="NoNewLine">false</B></Props></Obj><S N="Source">Write-Host</S>` +
	`<Obj N="Tags" RefId="2"><TN RefId="2"><T>System.Collections.Generic.List</T></TN><LST><S>PSHOST</S></LST></Obj>` +
	`<S N="User">VENGEANCE\pleiades-gate</S></Props></Obj>`

func TestDecodeCLIXML(t *testing.T) {
	tests := []struct {
		name, stderr, want string
	}{
		{
			name: "an error record, one element per line",
			stderr: "#< CLIXML\r\n" + objs(`<S S="Error">Get-Service : Cannot find any service with service name 'VBoxSDS'._x000D__x000A_</S>`+
				`<S S="Error">At line:2 char:1_x000D__x000A_</S><S S="Error"> _x000D__x000A_</S>`),
			want: "Get-Service : Cannot find any service with service name 'VBoxSDS'.\r\nAt line:2 char:1\r\n \r\n",
		},
		{
			name:   "warning, verbose and debug carry the console's prefix",
			stderr: "#< CLIXML\r\n" + objs(`<S S="warning">a warning</S><S S="verbose">some verbose</S><S S="debug">some debug</S>`),
			want:   "WARNING: a warning\r\nVERBOSE: some verbose\r\nDEBUG: some debug\r\n",
		},
		{
			name:   "an information record is dropped: what a console shows of it reached stdout",
			stderr: "#< CLIXML\r\n" + objs(hostInformationRecord+`<S S="Error">boom_x000D__x000A_</S>`),
			want:   "boom\r\n",
		},
		{
			name: "a native program's text outside the block is kept as written, markup and all",
			stderr: "#< CLIXML\r\nconsole error <&>\r\nnative stderr \r\n" +
				objs(`<S S="Error">[Console]::Error.WriteLine('console error &lt;&amp;&gt;')_x000D__x000A_</S>`) + "after\r\n",
			want: "console error <&>\r\nnative stderr \r\n[Console]::Error.WriteLine('console error <&>')\r\nafter\r\n",
		},
		{
			name:   "an escaped underscore and a surrogate pair",
			stderr: "#< CLIXML\r\n" + objs(`<S S="Error">an error with a _x005F_x0041_ and a smile _xD83D__xDE00__x000D__x000A_</S>`),
			want:   "an error with a _x0041_ and a smile \U0001F600\r\n",
		},
		{
			name:   "two blocks",
			stderr: "#< CLIXML\r\n" + objs(`<S S="Error">one_x000A_</S>`) + objs(`<S S="warning">two</S>`),
			want:   "one\nWARNING: two\r\n",
		},
		{
			name:   "not CLIXML",
			stderr: "Get-Service : plain text _x000D__x000A_",
			want:   "Get-Service : plain text _x000D__x000A_",
		},
		{
			name:   "a block that does not parse is kept",
			stderr: "#< CLIXML\r\n<Objs Version=\"1\"><S S=\"Error\">a & b</S></Objs>",
			want:   "<Objs Version=\"1\"><S S=\"Error\">a & b</S></Objs>",
		},
		{
			name:   "an unterminated block is kept",
			stderr: "#< CLIXML\r\n<Objs Version=\"1\"><S S=\"Error\">cut",
			want:   "<Objs Version=\"1\"><S S=\"Error\">cut",
		},
		{
			name:   "an unpaired surrogate becomes the replacement character",
			stderr: "#< CLIXML\r\n" + objs(`<S S="Error">x_xD83D_y</S>`),
			want:   "x�y",
		},
		{
			name:   "not quite an escape",
			stderr: "#< CLIXML\r\n" + objs(`<S S="Error">_x00G0_ _x12_ _x</S>`),
			want:   "_x00G0_ _x12_ _x",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeCLIXML(tt.stderr); got != tt.want {
				t.Errorf("decodeCLIXML =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// FuzzDecodeCLIXML holds the two promises that matter whatever a host
// sends: decoding never panics, and output that is not CLIXML is never
// changed.
func FuzzDecodeCLIXML(f *testing.F) {
	f.Add("#< CLIXML\r\n" + objs(`<S S="Error">a_x000D__x000A_</S><S S="warning">w</S>`))
	f.Add("#< CLIXML\r\nnative <&>\r\n" + objs(hostInformationRecord))
	f.Add("#< CLIXML\r\n<Objs><S S=\"Error\">_xD83D__xDE00_</S></Objs>")
	f.Add("plain")
	f.Fuzz(func(t *testing.T, stderr string) {
		got := decodeCLIXML(stderr)
		if !strings.HasPrefix(stderr, clixmlHeader) && got != stderr {
			t.Errorf("non-CLIXML output changed: %q -> %q", stderr, got)
		}
	})
}
