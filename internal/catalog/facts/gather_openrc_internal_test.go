// Tests of the OpenRC services probe's parser against output captured from
// a real OpenRC (Alpine 3.20, OpenRC 0.54). The probe itself runs against a
// real OpenRC in internal/catalog/facts/live.
package facts

import (
	"reflect"
	"testing"
)

func TestParseOpenRC_ReadsItsThreeSections(t *testing.T) {
	out := "sshd\ncrond\nlocal\nkillprocs\nsshd\n" + serviceSectionMark + "\n                sshd |      default                           \n" +
		"            killprocs |                     shutdown          \n               crond |      default    boot     \n" + serviceSectionMark +
		"\n[default]\nsshd =  started \ncrond =  crashed \n[Dynamic manual]\nlocal =  started \n"
	got := valuesOf(parseOpenRC(out))
	want := map[string]any{
		"service.sshd":      map[string]any{"state": "running", "enabled": true, "status": "default", "source": "openrc"},
		"service.crond":     map[string]any{"state": "crashed", "enabled": true, "status": "default,boot", "source": "openrc"},
		"service.local":     map[string]any{"state": "running", "enabled": false, "status": "disabled", "source": "openrc"},
		"service.killprocs": map[string]any{"state": "stopped", "enabled": false, "status": "shutdown", "source": "openrc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("openrc = %v\nwant %v", got, want)
	}
	if parseOpenRC("one section") != nil {
		t.Error("output missing its sections was read")
	}
}
