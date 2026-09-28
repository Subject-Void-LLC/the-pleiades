// Tests for the installer script, its DVD, and the keys that start it.
package bsdinstall_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/bsdinstall"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/cloudinit"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/iso9660"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/vboxmanage"
)

func TestScript_PreambleAndSetup(t *testing.T) {
	script, err := bsdinstall.Install{}.Script()
	if err != nil {
		t.Fatal(err)
	}
	preamble, setup, ok := strings.Cut(string(script), "\n#!/bin/sh\n")
	if !ok {
		t.Fatalf("no setup script after the preamble:\n%s", script)
	}
	for _, want := range []string{"PARTITIONS=DEFAULT", `DISTRIBUTIONS="kernel.txz base.txz"`, `export nonInteractive="YES"`} {
		if !strings.Contains(preamble, want) {
			t.Errorf("the preamble lacks %s:\n%s", want, preamble)
		}
	}
	for _, want := range []string{
		`sysrc sshd_enable="YES"`, `sysrc nuageinit_enable="YES"`, "touch /firstboot",
		`console="comconsole,vidconsole"`, "# KEYWORD: firstboot",
		cloudinit.HostKeysBegin, cloudinit.HostKeysEnd,
	} {
		if !strings.Contains(setup, want) {
			t.Errorf("the setup script lacks %s", want)
		}
	}

	named, err := bsdinstall.Install{Distributions: []string{"kernel.txz", "base.txz", "lib32.txz"}}.Script()
	if err != nil || !strings.Contains(string(named), `DISTRIBUTIONS="kernel.txz base.txz lib32.txz"`) {
		t.Errorf("named sets: %v\n%s", err, named)
	}
}

// TestScript_SetupParsesAsShell runs the setup script, and the host-key
// rc script it writes, through sh -n, so a quoting slip fails here rather
// than halfway through a real install.
func TestScript_SetupParsesAsShell(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh to parse with")
	}
	script, err := bsdinstall.Install{}.Script()
	if err != nil {
		t.Fatal(err)
	}
	_, setup, _ := strings.Cut(string(script), "\n#!/bin/sh\n")
	rc := setup[strings.Index(setup, "<<'RC'\n")+len("<<'RC'\n") : strings.Index(setup, "\nRC\n")]
	dir := t.TempDir()
	for name, body := range map[string]string{"setup": "#!/bin/sh\n" + setup, "rc": rc} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(sh, "-n", path).CombinedOutput(); err != nil {
			t.Errorf("sh -n on the %s script: %v\n%s", name, err, out)
		}
	}
}

func TestValidate_RefusesAnythingButASetName(t *testing.T) {
	for _, bad := range []string{"", ".txz", "base", "base.tgz", "Base.txz", "../base.txz", "base.txz; reboot", "base .txz"} {
		if _, err := (bsdinstall.Install{Distributions: []string{bad}}).Script(); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

func TestISO_CarriesTheScriptUnderItsLabel(t *testing.T) {
	when := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	image, err := bsdinstall.Install{}.ISO(when)
	if err != nil {
		t.Fatal(err)
	}
	script, _ := bsdinstall.Install{}.Script()
	if !bytes.Contains(image, script) {
		t.Error("the image does not hold the script")
	}
	// The primary volume descriptor's identifier, at sector 16, offset 40.
	pvd := image[16*iso9660.SectorSize:]
	if got := strings.TrimRight(string(pvd[40:72]), " "); got != bsdinstall.VolumeID {
		t.Errorf("the volume is labeled %q, want %q", got, bsdinstall.VolumeID)
	}
	again, _ := bsdinstall.Install{}.ISO(when)
	if !bytes.Equal(image, again) {
		t.Error("the same install and time gave two different images")
	}
}

func TestKeys_AreKeystrokes(t *testing.T) {
	for _, written := range []string{bsdinstall.ShellKeys, bsdinstall.ScriptCommand} {
		if _, err := vboxmanage.Keys(written); err != nil {
			t.Fatalf("%q does not parse: %v", written, err)
		}
	}
	strokes, _ := vboxmanage.Keys(bsdinstall.ScriptCommand)
	var typed strings.Builder
	for _, s := range strokes {
		typed.WriteString(s.Text)
	}
	for _, want := range []string{"/dev/iso9660/" + bsdinstall.VolumeID, "bsdinstall script /tmp/pleiades/" + bsdinstall.FileName, "&& poweroff"} {
		if !strings.Contains(typed.String(), want) {
			t.Errorf("the typed command lacks %q: %s", want, typed.String())
		}
	}
}

// screen is a w by h PNG, blue in the given fraction of its rows from the
// top and black below, as the installer's screen and a boot screen are.
func screen(t *testing.T, w, h int, blueRows float64, blue color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{0, 0, 0, 255}
			if float64(y) < blueRows*float64(h) {
				c = blue
			}
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestWelcomeOn(t *testing.T) {
	vgaBlue := color.RGBA{0, 0, 168, 255}
	for name, tc := range map[string]struct {
		png  []byte
		want bool
	}{
		"the installer's screen":         {screen(t, 720, 400, 0.82, vgaBlue), true},
		"a renderer rounding the blue":   {screen(t, 720, 400, 0.9, color.RGBA{4, 2, 176, 255}), true},
		"the loader's or kernel's":       {screen(t, 720, 400, 0, vgaBlue), false},
		"a little blue on a boot screen": {screen(t, 720, 400, 0.3, vgaBlue), false},
		"another blue altogether":        {screen(t, 720, 400, 1, color.RGBA{0, 0, 255, 255}), false},
	} {
		got, err := bsdinstall.WelcomeOn(tc.png)
		if err != nil || got != tc.want {
			t.Errorf("%s: %v, %v; want %v", name, got, err, tc.want)
		}
	}
	if _, err := bsdinstall.WelcomeOn([]byte("\x89PNG\r\n\x1a\n not really")); err == nil {
		t.Error("a broken PNG was read as a screen")
	}
}

func TestStartedOn(t *testing.T) {
	if bsdinstall.StartedOn([]byte("Welcome to FreeBSD\r\nConsole type [vt100]: ")) {
		t.Error("a console without the marker read as started")
	}
	if !bsdinstall.StartedOn([]byte("\x1b[0m" + bsdinstall.Started + "\r\nDistribution extract: base.txz")) {
		t.Error("the marker was not read")
	}
	if !strings.Contains(bsdinstall.ScriptCommand, "echo '"+bsdinstall.Started+"' > /dev/cuau0") {
		t.Error("the typed command does not write the marker to the serial port")
	}
}

// TestScript_HostKeysReachTheReaderClonesUse proves the host-key block the
// base prints is the block cloudinit.HostKeys takes, by printing it as the
// rc script would and reading it back.
func TestScript_HostKeysReachTheReaderClonesUse(t *testing.T) {
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMp8fYjc7cLwZr3VOQ2m0j9e2f5x5bd8KfJqz6y2Vk1a root@freebsd"
	console := cloudinit.HostKeysBegin + "\n" + key + "\n" + cloudinit.HostKeysEnd + "\n"
	script, _ := bsdinstall.Install{}.Script()
	if !strings.Contains(string(script), `echo "`+cloudinit.HostKeysBegin+`"`) || !strings.Contains(string(script), `echo "`+cloudinit.HostKeysEnd+`"`) {
		t.Fatal("the rc script does not print cloudinit's markers")
	}
	keys, err := cloudinit.HostKeys(console)
	if err != nil || len(keys) != 1 {
		t.Fatalf("cloudinit.HostKeys read %v, %v", keys, err)
	}
}

// TestWelcomeOn_TheRealInstaller reads the screen FreeBSD 15.1's DVD
// showed the lab host at its Welcome dialog, captured through
// virt.vbox.vm.screenshot on 2026-09-27, so the rule is held to the real
// screen and not only to images drawn to fit it.
func TestWelcomeOn_TheRealInstaller(t *testing.T) {
	captured, err := os.ReadFile(filepath.Join("testdata", "welcome-15.1.png"))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := bsdinstall.WelcomeOn(captured); err != nil || !ok {
		t.Errorf("the real Welcome screen: %v, %v", ok, err)
	}
}
