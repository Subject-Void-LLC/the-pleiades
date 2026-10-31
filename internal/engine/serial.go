// Package engine: serial:, how many devices a run changes at a time, and the
// one function both tiers split a run's devices into batches with.
//
// serial: is a runbook key, as a play's is in Ansible: a number of devices
// (serial: 2), a percentage of them (serial: "30%"), or a list of either
// (serial: [1, "50%"]), the last size repeating for every later batch. The
// batches are run one after another by whatever fans the runbook out (the
// CLI's run pipeline, the Controller's dispatcher), never by the Executor, so
// there is one way devices are batched and the two tiers cannot disagree.
package engine

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// SerialSize is one batch size: N devices, or N percent of them.
type SerialSize struct {
	// N is the count, or the percentage when Percent is set.
	N int
	// Percent says N is a percentage of the run's devices.
	Percent bool
}

// String writes s as a runbook would.
func (s SerialSize) String() string {
	if s.Percent {
		return strconv.Itoa(s.N) + "%"
	}
	return strconv.Itoa(s.N)
}

// of returns how many of total devices s is: a percentage rounds down, as
// Ansible's pct_to_int does, and is never less than one device.
func (s SerialSize) of(total int) int {
	if !s.Percent {
		return s.N
	}
	if n := s.N * total / 100; n > 0 {
		return n
	}
	return 1
}

// parseSerialSize reads one size from text ("3", "30%"), refusing what
// Ansible would take to mean "every device at once" (zero, a negative): a
// runbook says that by leaving serial: out.
func parseSerialSize(text string) (SerialSize, error) {
	text = strings.TrimSpace(text)
	if IsTemplate(text) {
		return SerialSize{}, fmt.Errorf("serial: %q is a template; the batch size must be written literally, since it decides how many devices change at once", text)
	}
	s := SerialSize{}
	if strings.HasSuffix(text, "%") {
		s.Percent = true
		text = strings.TrimSpace(strings.TrimSuffix(text, "%"))
	}
	n, err := strconv.Atoi(text)
	switch {
	case err != nil:
		return SerialSize{}, fmt.Errorf("serial: %q is neither a number of devices nor a percentage such as \"30%%\"", text)
	case n < 1:
		return SerialSize{}, fmt.Errorf("serial: %d is not a batch size; leave serial: out to run every device at once", n)
	case s.Percent && n > 100:
		return SerialSize{}, fmt.Errorf("serial: %d%% is more than every device", n)
	}
	s.N = n
	return s, nil
}

// SerialList is a runbook's serial: one size or a list of them.
type SerialList []SerialSize

// UnmarshalYAML reads a number, a percentage string, or a list of either.
func (l *SerialList) UnmarshalYAML(node *yaml.Node) error {
	var items []*yaml.Node
	switch node.Kind {
	case yaml.ScalarNode:
		items = []*yaml.Node{node}
	case yaml.SequenceNode:
		items = node.Content
	default:
		return fmt.Errorf("line %d: serial: takes a number, a percentage or a list of them", node.Line)
	}
	return l.parse(func(yield func(string) error) error {
		for _, item := range items {
			if item.Kind != yaml.ScalarNode {
				return fmt.Errorf("line %d: serial: takes a number, a percentage or a list of them", item.Line)
			}
			if err := yield(item.Value); err != nil {
				return fmt.Errorf("line %d: %w", item.Line, err)
			}
		}
		return nil
	})
}

// UnmarshalJSON reads the same shapes from JSON.
func (l *SerialList) UnmarshalJSON(data []byte) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	items, ok := raw.([]any)
	if !ok {
		items = []any{raw}
	}
	return l.parse(func(yield func(string) error) error {
		for _, item := range items {
			switch v := item.(type) {
			case float64:
				if err := yield(strconv.FormatFloat(v, 'f', -1, 64)); err != nil {
					return err
				}
			case string:
				if err := yield(v); err != nil {
					return err
				}
			default:
				return fmt.Errorf("serial: takes a number, a percentage or a list of them")
			}
		}
		return nil
	})
}

// parse fills l from each text each yields.
func (l *SerialList) parse(each func(yield func(string) error) error) error {
	var out SerialList
	err := each(func(text string) error {
		s, err := parseSerialSize(text)
		if err != nil {
			return err
		}
		out = append(out, s)
		return nil
	})
	if err != nil {
		return err
	}
	if len(out) == 0 {
		return fmt.Errorf("serial: is an empty list; leave it out to run every device at once")
	}
	*l = out
	return nil
}

// MarshalJSON writes l as a list of the sizes as written, so a runbook's
// version hash reads them as the author did.
func (l SerialList) MarshalJSON() ([]byte, error) {
	out := make([]string, len(l))
	for i, s := range l {
		out[i] = s.String()
	}
	return json.Marshal(out)
}

// Batches splits items, in their order, into the batches sizes describes,
// Ansible's _get_serialized_batches: each size is taken of the whole count,
// the last size repeats until every item is placed, and no sizes at all is
// one batch of everything. It never returns an empty batch.
func Batches[T any](items []T, sizes SerialList) [][]T {
	if len(items) == 0 {
		return nil
	}
	if len(sizes) == 0 {
		return [][]T{items}
	}
	var out [][]T
	rest, i := items, 0
	for len(rest) > 0 {
		n := min(sizes[i].of(len(items)), len(rest))
		out = append(out, rest[:n:n])
		rest = rest[n:]
		if i < len(sizes)-1 {
			i++
		}
	}
	return out
}
