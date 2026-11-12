// Tests for CheckFeatureName, and that Status, Enable and Disable hold a
// feature name to it before anything is sent (FAILURE_PATTERNS 1174's
// sweep).
package winrmdism

import (
	"context"
	"strings"
	"testing"
)

// TestCheckFeatureName admits the names DISM lists and refuses a name that
// would reach dism.exe as more than one argument or as one of its options.
func TestCheckFeatureName(t *testing.T) {
	for _, good := range []string{"Microsoft-Windows-Subsystem-Linux", "IIS-WebServerRole", "NetFx3", "SMB1Protocol-Deprecation", "WCF-HTTP-Activation45", "Containers.Disposable_1"} {
		if err := CheckFeatureName(good); err != nil {
			t.Errorf("%s: %v", good, err)
		}
	}
	for _, bad := range []string{"", `X" /Source:\\evil\share "`, "a b", "/all", "-x", "a:b", "a;b", "a'b", "a\nb", "*", strings.Repeat("a", 256)} {
		if err := CheckFeatureName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// TestFunctions_RefuseAFeatureNameBeforeSending: each function refuses a
// name outside the form before it contacts the host. The session names no
// host at all, so a function that got past the check would fail on the
// connection instead, with a different error.
func TestFunctions_RefuseAFeatureNameBeforeSending(t *testing.T) {
	name := `X" /Source:\\evil\share "`
	ctx := context.Background()
	_, statusErr := Status(ctx, Session{}, `C:\Windows\Temp\dism.log`, name)
	_, enableErr := Enable(ctx, Session{}, `C:\Windows\Temp\dism.log`, name)
	_, disableErr := Disable(ctx, Session{}, `C:\Windows\Temp\dism.log`, name)
	for label, err := range map[string]error{"Status": statusErr, "Enable": enableErr, "Disable": disableErr} {
		if err == nil || !strings.Contains(err.Error(), "is not one DISM lists") {
			t.Errorf("%s = %v, want the name refused before anything is sent", label, err)
		}
	}
}
