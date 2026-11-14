// The Schema/Injection Hardening table for the answer files: every field a
// caller sets, given each hostile value, is refused by a message naming the
// field, or carried exactly, escaped, in its own element only. It runs the
// fuzz targets' own checks over a fixed table, so these values are proven
// on every test run rather than only when a fuzzer happens on them.
package winunattend

import "testing"

// hostile is each value: quotes, a backtick, "$(", ";", a line break, a
// NUL, U+FFFE, a ".." segment, a UNC path and a drive.
var hostile = []string{"'", `"`, "`", "$(calc)", ";calc", "\n", "\x00", "\uFFFE", "..", `\\server\share`, `C:\`, "]]><x/>", "&amp;", "%WINDIR%"}

func TestAnswerFiles_HostileValuesAreRefusedOrCarried(t *testing.T) {
	seedBase, err := baseSeed.XML()
	if err != nil {
		t.Fatal(err)
	}
	installBase, err := baseInstall.XML()
	if err != nil {
		t.Fatal(err)
	}
	good := seedInput{"PLEIADES-W1", "Correct-Horse-9", "08:00:27:aa:bb:cc", "192.168.56.30/24", "", ""}
	goodInstall := installInput{"Windows Server 2025 Standard Evaluation (Desktop Experience)", "Correct-Horse-9", ""}
	for _, v := range hostile {
		// A password meeting Windows' policy around the value, so the value
		// itself is what is refused or carried.
		password := "Aa1!" + v + "wxyz"
		for field, in := range map[string]seedInput{
			"name":     {v, good.password, good.mac, good.address, good.lang, good.zone},
			"password": {good.name, password, good.mac, good.address, good.lang, good.zone},
			"mac":      {good.name, good.password, v, good.address, good.lang, good.zone},
			"address":  {good.name, good.password, good.mac, v, good.lang, good.zone},
			"language": {good.name, good.password, good.mac, good.address, v, good.zone},
			"zone":     {good.name, good.password, good.mac, good.address, good.lang, "UTC" + v},
		} {
			t.Run("seed "+field+" "+v, func(t *testing.T) { checkSeedInput(t, seedBase, in) })
		}
		for field, in := range map[string]installInput{
			"image":    {"Windows" + v, goodInstall.password, goodInstall.lang},
			"password": {goodInstall.image, password, goodInstall.lang},
			"language": {goodInstall.image, goodInstall.password, v},
		} {
			t.Run("install "+field+" "+v, func(t *testing.T) { checkInstallInput(t, installBase, in) })
		}
	}
}
