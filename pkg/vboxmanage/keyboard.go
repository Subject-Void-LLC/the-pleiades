// Typing into a running machine's console, as its keyboard would: text,
// named keys and pauses, written as Packer's boot_command writes them.
package vboxmanage

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Keystroke is one step of what Type sends: text to type, a key's scan
// codes, or a pause.
type Keystroke struct {
	// Text is typed as it is, one character a keystroke.
	Text string
	// Scancodes are a key's make and break codes, in hex.
	Scancodes []string
	// Pause is how long to wait before the next step.
	Pause time.Duration
}

// keys are the named keys Keys reads, each as its set 1 make and break
// scan codes. The e0 prefix marks the keys a PC keyboard added later.
var keys = map[string][]string{
	"enter": {"1c", "9c"}, "tab": {"0f", "8f"}, "esc": {"01", "81"}, "bs": {"0e", "8e"},
	"spacebar": {"39", "b9"}, "del": {"e0", "53", "e0", "d3"}, "insert": {"e0", "52", "e0", "d2"},
	"up": {"e0", "48", "e0", "c8"}, "down": {"e0", "50", "e0", "d0"},
	"left": {"e0", "4b", "e0", "cb"}, "right": {"e0", "4d", "e0", "cd"},
	"home": {"e0", "47", "e0", "c7"}, "end": {"e0", "4f", "e0", "cf"},
	"pageup": {"e0", "49", "e0", "c9"}, "pagedown": {"e0", "51", "e0", "d1"},
	"f1": {"3b", "bb"}, "f2": {"3c", "bc"}, "f3": {"3d", "bd"}, "f4": {"3e", "be"},
	"f5": {"3f", "bf"}, "f6": {"40", "c0"}, "f7": {"41", "c1"}, "f8": {"42", "c2"},
	"f9": {"43", "c3"}, "f10": {"44", "c4"}, "f11": {"57", "d7"}, "f12": {"58", "d8"},
}

// KeyNames returns the names Keys reads between angle brackets, sorted.
func KeyNames() []string {
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// keyToken is a named key or a pause: <enter>, <wait>, <wait5>.
var keyToken = regexp.MustCompile(`<([a-z0-9]+)>`)

// maxPause bounds one <waitN>.
const maxPause = 60

// Keys reads keystrokes written as Packer's boot_command writes them:
// text as it is, a named key between angle brackets (<enter>, <tab>), and
// <wait> or <waitN> for a pause of one or N seconds. Text may hold only
// printable ASCII, which is what a console's US layout types; anything
// else, and an unknown name in brackets, is refused.
func Keys(written string) ([]Keystroke, error) {
	var out []Keystroke
	rest := written
	for rest != "" {
		loc := keyToken.FindStringSubmatchIndex(rest)
		text := rest
		if loc != nil {
			text = rest[:loc[0]]
		}
		if text != "" {
			if strings.ContainsFunc(text, func(r rune) bool { return r > unicode.MaxASCII || !unicode.IsPrint(r) }) {
				return nil, fmt.Errorf("vboxmanage: %q holds a character other than printable ASCII, which a console keyboard does not type", text)
			}
			out = append(out, Keystroke{Text: text})
		}
		if loc == nil {
			break
		}
		name := rest[loc[2]:loc[3]]
		switch codes, ok := keys[name]; {
		case ok:
			out = append(out, Keystroke{Scancodes: codes})
		case strings.HasPrefix(name, "wait"):
			seconds := 1
			if n := strings.TrimPrefix(name, "wait"); n != "" {
				var err error
				if seconds, err = strconv.Atoi(n); err != nil || seconds < 1 || seconds > maxPause {
					return nil, fmt.Errorf("vboxmanage: <%s> is not a pause of 1 to %d seconds", name, maxPause)
				}
			}
			out = append(out, Keystroke{Pause: time.Duration(seconds) * time.Second})
		default:
			return nil, fmt.Errorf("vboxmanage: <%s> is not a key; the keys are %s, and <wait> or <waitN>", name, strings.Join(KeyNames(), ", "))
		}
		rest = rest[loc[1]:]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("vboxmanage: nothing to type")
	}
	return out, nil
}

// Type sends strokes to vm's keyboard, pausing where they say. Text
// travels on VBoxManage's command line, so it must never be a secret.
func (h Host) Type(ctx context.Context, vm string, strokes []Keystroke) error {
	if err := CheckName("VM", vm); err != nil {
		return err
	}
	for _, s := range strokes {
		var err error
		switch {
		case s.Text != "":
			_, err = h.run(ctx, "controlvm", vm, "keyboardputstring", s.Text)
		case len(s.Scancodes) > 0:
			_, err = h.run(ctx, append([]string{"controlvm", vm, "keyboardputscancode"}, s.Scancodes...)...)
		default:
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(s.Pause):
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}
