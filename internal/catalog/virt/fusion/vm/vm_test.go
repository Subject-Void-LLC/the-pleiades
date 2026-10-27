// Tests of virt.fusion.vm.info, start and stop against the model Fusion host.
package vm

import (
	"errors"
	"strings"
	"testing"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/capability"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/collection"
)

func TestRegistered(t *testing.T) {
	for _, fqcn := range []string{"virt.fusion.vm.info", "virt.fusion.vm.start", "virt.fusion.vm.stop"} {
		d, ok := collection.Lookup(fqcn)
		if !ok {
			t.Fatalf("%s is not registered", fqcn)
		}
		m := d.Manifest
		if m.Status != collection.StatusImplemented || !m.SupportsCheck || len(m.SupportedTransports) != 1 || m.SupportedTransports[0] != "ssh" ||
			len(m.RequiredCapabilities) != 1 || m.RequiredCapabilities[0] != capability.NameVMwareFusion {
			t.Errorf("%s manifest: %+v", fqcn, m)
		}
	}
}

func TestInfo(t *testing.T) {
	for name, tc := range map[string]struct {
		m    model
		want map[string]any
	}{
		"no VM":             {model{}, map[string]any{statExists: false}},
		"off":               {model{exists: true, state: stateOff}, map[string]any{statExists: true, statState: stateOff}},
		"on, tools running": {model{exists: true, state: stateOn, tools: true, ip: "10.0.0.155"}, map[string]any{statExists: true, statState: stateOn, statGuestIP: "10.0.0.155"}},
		"on, no tools":      {model{exists: true, state: stateOn}, map[string]any{statExists: true, statState: stateOn}},
	} {
		t.Run(name, func(t *testing.T) {
			m := tc.m
			closed := onModel(t, &m)
			rc := newRecorder()
			res, err := call(t, "virt.fusion.vm.info", false, rc, map[string]any{paramVMX: vmx})
			if err != nil || res.Changed {
				t.Fatalf("res %+v err %v", res, err)
			}
			if len(rc.stats) != len(tc.want) {
				t.Errorf("stats %v, want %v", rc.stats, tc.want)
			}
			for k, v := range tc.want {
				if rc.stats[k] != v {
					t.Errorf("%s = %v, want %v", k, rc.stats[k], v)
				}
			}
			if len(m.changes()) != 0 || !*closed {
				t.Errorf("a read changed %v, closed %v", m.changes(), *closed)
			}
		})
	}
}

func TestStart(t *testing.T) {
	t.Run("off: starts, holds the Mac awake, and records a soft stop", func(t *testing.T) {
		m := &model{exists: true, state: stateOff, pid: 79896}
		onModel(t, m)
		rc := newRecorder()
		res, err := call(t, "virt.fusion.vm.start", false, rc, map[string]any{paramVMX: vmx})
		if err != nil || !res.Changed {
			t.Fatalf("res %+v err %v", res, err)
		}
		if rc.stats[statState] != stateOn || rc.stats[statKeptAwake] != true || rc.stats[statPID] != 79896 {
			t.Errorf("stats %v", rc.stats)
		}
		inv := rc.inverse()
		params, _ := inv["params"].(map[string]any)
		if inv["fqcn"] != "virt.fusion.vm.stop" || params[paramMode] != "soft" || params[paramVMX] != vmx {
			t.Errorf("undo %v", inv)
		}
	})
	t.Run("suspended: resumes, and the undo suspends it again", func(t *testing.T) {
		m := &model{exists: true, state: stateSuspended, pid: 7}
		onModel(t, m)
		rc := newRecorder()
		if _, err := call(t, "virt.fusion.vm.start", false, rc, map[string]any{paramVMX: vmx}); err != nil {
			t.Fatal(err)
		}
		params, _ := rc.inverse()["params"].(map[string]any)
		if params[paramMode] != "suspend" {
			t.Errorf("undo %v", rc.inverse())
		}
	})
	t.Run("keep_awake false: starts without the hold", func(t *testing.T) {
		m := &model{exists: true, state: stateOff, pid: 7}
		onModel(t, m)
		rc := newRecorder()
		if _, err := call(t, "virt.fusion.vm.start", false, rc, map[string]any{paramVMX: vmx, paramKeepAwake: false}); err != nil {
			t.Fatal(err)
		}
		for _, c := range m.calls {
			if strings.Contains(c, "caffeinate") {
				t.Fatalf("held the Mac awake anyway: %s", c)
			}
		}
		if rc.stats[statKeptAwake] != false {
			t.Errorf("stats %v", rc.stats)
		}
	})
	t.Run("on: no change and no undo", func(t *testing.T) {
		m := &model{exists: true, state: stateOn}
		onModel(t, m)
		rc := newRecorder()
		res, err := call(t, "virt.fusion.vm.start", false, rc, map[string]any{paramVMX: vmx})
		if err != nil || res.Changed || rc.inverse() != nil || len(m.changes()) != 0 {
			t.Fatalf("res %+v err %v undo %v changes %v", res, err, rc.inverse(), m.changes())
		}
	})
	t.Run("paused: refused", func(t *testing.T) {
		onModel(t, &model{exists: true, state: statePaused})
		if _, err := call(t, "virt.fusion.vm.start", false, newRecorder(), map[string]any{paramVMX: vmx}); err == nil || !strings.Contains(err.Error(), "paused") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("no VM: a run fails and a check is unchecked", func(t *testing.T) {
		onModel(t, &model{})
		if _, err := call(t, "virt.fusion.vm.start", false, newRecorder(), map[string]any{paramVMX: vmx}); err == nil || !strings.Contains(err.Error(), "has no VM") {
			t.Fatalf("run: %v", err)
		}
		var cannot *collection.CannotCheckError
		if _, err := call(t, "virt.fusion.vm.start", true, newRecorder(), map[string]any{paramVMX: vmx}); !errors.As(err, &cannot) {
			t.Fatalf("check: %v", err)
		}
	})
	t.Run("check: predicts and sends nothing", func(t *testing.T) {
		m := &model{exists: true, state: stateOff}
		onModel(t, m)
		rc := newRecorder()
		res, err := call(t, "virt.fusion.vm.start", true, rc, map[string]any{paramVMX: vmx})
		if err != nil || !res.Changed || rc.stats[statState] != stateOn || len(m.changes()) != 0 || rc.inverse() != nil {
			t.Fatalf("res %+v err %v stats %v changes %v", res, err, rc.stats, m.changes())
		}
	})
	t.Run("a bad vmx is refused before anything runs", func(t *testing.T) {
		m := &model{}
		onModel(t, m)
		for _, bad := range []string{"relative.vmx", "/a/b.vmdk", "/a/b\n.vmx"} {
			if _, err := call(t, "virt.fusion.vm.start", false, newRecorder(), map[string]any{paramVMX: bad}); err == nil {
				t.Errorf("%q accepted", bad)
			}
		}
		if len(m.calls) != 0 {
			t.Errorf("ran %v", m.calls)
		}
	})
}

func TestStop(t *testing.T) {
	for name, tc := range map[string]struct {
		before, mode, after string
		tools               bool
	}{
		"soft with tools": {stateOn, "soft", stateOff, true},
		"hard":            {stateOn, "hard", stateOff, false},
		"suspend":         {stateOn, "suspend", stateSuspended, false},
		"default is soft": {stateOn, "", stateOff, true},
	} {
		t.Run(name, func(t *testing.T) {
			m := &model{exists: true, state: tc.before, tools: tc.tools}
			onModel(t, m)
			rc := newRecorder()
			params := map[string]any{paramVMX: vmx}
			if tc.mode != "" {
				params[paramMode] = tc.mode
			}
			res, err := call(t, "virt.fusion.vm.stop", false, rc, params)
			if err != nil || !res.Changed || rc.stats[statState] != tc.after {
				t.Fatalf("res %+v err %v stats %v", res, err, rc.stats)
			}
			if inv := rc.inverse(); inv["fqcn"] != "virt.fusion.vm.start" {
				t.Errorf("undo %v", inv)
			}
		})
	}
	t.Run("soft without tools: the tool's own refusal, and no undo", func(t *testing.T) {
		m := &model{exists: true, state: stateOn}
		onModel(t, m)
		rc := newRecorder()
		_, err := call(t, "virt.fusion.vm.stop", false, rc, map[string]any{paramVMX: vmx, paramMode: "soft"})
		if err == nil || !strings.Contains(err.Error(), "VMware Tools are not running") || rc.inverse() != nil {
			t.Fatalf("err %v undo %v", err, rc.inverse())
		}
	})
	t.Run("already off, or suspended for suspend: no change", func(t *testing.T) {
		for _, tc := range []struct{ state, mode string }{{stateOff, "soft"}, {stateOff, "suspend"}, {stateSuspended, "suspend"}} {
			m := &model{exists: true, state: tc.state}
			onModel(t, m)
			res, err := call(t, "virt.fusion.vm.stop", false, newRecorder(), map[string]any{paramVMX: vmx, paramMode: tc.mode})
			if err != nil || res.Changed || len(m.changes()) != 0 {
				t.Errorf("%s/%s: res %+v err %v changes %v", tc.state, tc.mode, res, err, m.changes())
			}
		}
	})
	t.Run("suspended asked for a power-off, paused, and an unknown mode are refused", func(t *testing.T) {
		for _, tc := range []struct{ state, mode string }{{stateSuspended, "hard"}, {statePaused, "soft"}, {stateOn, "reboot"}} {
			onModel(t, &model{exists: true, state: tc.state})
			if _, err := call(t, "virt.fusion.vm.stop", false, newRecorder(), map[string]any{paramVMX: vmx, paramMode: tc.mode}); err == nil {
				t.Errorf("%s/%s accepted", tc.state, tc.mode)
			}
		}
	})
	t.Run("check: predicts and sends nothing", func(t *testing.T) {
		m := &model{exists: true, state: stateOn}
		onModel(t, m)
		rc := newRecorder()
		res, err := call(t, "virt.fusion.vm.stop", true, rc, map[string]any{paramVMX: vmx, paramMode: "suspend"})
		if err != nil || !res.Changed || rc.stats[statState] != stateSuspended || len(m.changes()) != 0 {
			t.Fatalf("res %+v err %v stats %v changes %v", res, err, rc.stats, m.changes())
		}
	})
}
