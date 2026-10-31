// The link-impairment methods: what netem has set on a node's interfaces, and
// setting and clearing it, each with an exact undo.
package clab

import (
	"context"
	"fmt"
	"net/http"
	"regexp"

	"github.com/Subject-Void-LLC/the-pleiades/pkg/sdk"
)

// durationRE is a Go-style duration as netem takes one: 100ms, 1.5s.
var durationRE = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(us|ms|s|m)$`)

// impairment is one interface's netem settings as the server shows them.
type impairment struct {
	Interface  string  `json:"interface"`
	Delay      string  `json:"delay"`
	Jitter     string  `json:"jitter"`
	PacketLoss float64 `json:"packet_loss"`
	Rate       int     `json:"rate"`
}

// asMap is the impairment as a result.
func (i impairment) asMap() map[string]any {
	return map[string]any{"interface": i.Interface, "delay": i.Delay, "jitter": i.Jitter, "packet_loss": i.PacketLoss, "rate": i.Rate}
}

// impairments turns a list into results, never nil.
func impairments(list []impairment) []any {
	out := make([]any, 0, len(list))
	for _, i := range list {
		out = append(out, i.asMap())
	}
	return out
}

// netemShow reads a node's impairments.
func (s *session) netemShow(ctx context.Context, lab string, n nodeInfo) ([]impairment, error) {
	var out map[string][]impairment
	if err := s.call(ctx, http.MethodGet, "/api/v1/tools/netem/show", query("labName", lab, "nodeName", n.NodeName), nil, &out); err != nil {
		return nil, err
	}
	// The answer is keyed by the node, by the name the server uses for it.
	for _, list := range out {
		return list, nil
	}
	return nil, nil
}

// netemShowHandler is virt.clab.netem.show.
func netemShowHandler(ctx context.Context, s *session, a *args, _ bool) (bool, error) {
	lab, name := a.ident("lab"), a.ident("node")
	if a.err != nil {
		return false, a.err
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	list, err := s.netemShow(ctx, lab, n)
	if err != nil {
		return false, err
	}
	return false, s.stats(map[string]any{"interfaces": impairments(list), "name": n.Name})
}

// netemInverseKeys are the parameters of a netem.set undo.
func netemSetUndo(lab string, n nodeInfo, iface string, prior *impairment, partial bool) sdk.Inverse {
	if prior == nil {
		return sdk.Inverse{FQCN: "virt.clab.netem.reset", Params: map[string]any{"lab": lab, "node": n.NodeName, "interface": iface},
			Description: fmt.Sprintf("Clears the impairments on %s %s, which had none.", n.NodeName, iface), Partial: partial, KeepOnFailure: true}
	}
	params := map[string]any{"lab": lab, "node": n.NodeName, "interface": iface}
	if prior.Delay != "" {
		params["delay"] = prior.Delay
	}
	if prior.Jitter != "" {
		params["jitter"] = prior.Jitter
	}
	if prior.PacketLoss != 0 {
		params["loss"] = prior.PacketLoss
	}
	if prior.Rate != 0 {
		params["rate"] = prior.Rate
	}
	return sdk.Inverse{FQCN: "virt.clab.netem.set", Params: params,
		Description: fmt.Sprintf("Sets %s %s back to the impairments it had.", n.NodeName, iface), Partial: partial, KeepOnFailure: true}
}

// find returns the entry for iface.
func find(list []impairment, iface string) *impairment {
	for i := range list {
		if list[i].Interface == iface {
			return &list[i]
		}
	}
	return nil
}

// netemSet sets impairments on an interface, only when they differ from what
// is there.
func netemSet(ctx context.Context, s *session, a *args, check bool) (bool, error) {
	lab, name, iface := a.ident("lab"), a.ident("node"), a.ident("interface")
	delay, jitter := a.str("delay"), a.str("jitter")
	loss, hasLoss := a.number("loss")
	rate, hasRate := a.integer("rate")
	corruption, hasCorruption := a.number("corruption")
	if a.err != nil {
		return false, a.err
	}
	for field, v := range map[string]string{"delay": delay, "jitter": jitter} {
		if v != "" && !durationRE.MatchString(v) {
			return false, fmt.Errorf("%s: %s %q is not a duration (100ms, 1.5s)", s.fqcn, field, v)
		}
	}
	switch {
	case jitter != "" && delay == "":
		return false, fmt.Errorf("%s: jitter needs delay", s.fqcn)
	case loss < 0 || loss > 100 || corruption < 0 || corruption > 100:
		return false, fmt.Errorf("%s: loss and corruption are percentages, 0 to 100", s.fqcn)
	case rate < 0:
		return false, fmt.Errorf("%s: rate is kbit/s and not negative", s.fqcn)
	case delay == "" && !hasLoss && !hasRate && !hasCorruption:
		return false, fmt.Errorf("%s: set at least one of delay, loss, rate and corruption; virt.clab.netem.reset clears them", s.fqcn)
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	list, err := s.netemShow(ctx, lab, n)
	if err != nil {
		return false, err
	}
	prior := find(list, iface)
	same := prior != nil && prior.Delay == delay && prior.Jitter == jitter && prior.PacketLoss == loss && prior.Rate == rate
	if same && !hasCorruption {
		return false, s.stats(map[string]any{"interfaces": impairments(list), "name": n.Name})
	}
	if check {
		return true, s.stats(map[string]any{"interfaces": impairments(list), "name": n.Name})
	}
	// Corruption is not in what netem.show returns, so what was there cannot
	// be put back whole when it is being set.
	if err := s.undo(netemSetUndo(lab, n, iface, prior, hasCorruption)); err != nil {
		return false, err
	}
	body := map[string]any{"labName": lab, "nodeName": n.NodeName, "interface": iface}
	if delay != "" {
		body["delay"] = delay
	}
	if jitter != "" {
		body["jitter"] = jitter
	}
	if hasLoss {
		body["loss"] = loss
	}
	if hasRate {
		body["rate"] = rate
	}
	if hasCorruption {
		body["corruption"] = corruption
	}
	if err := s.call(ctx, http.MethodPost, "/api/v1/tools/netem/set", nil, body, nil); err != nil {
		return false, err
	}
	after, err := s.netemShow(ctx, lab, n)
	if err != nil {
		return true, err
	}
	return true, s.stats(map[string]any{"interfaces": impairments(after), "name": n.Name})
}

// netemReset clears an interface's impairments, if it has any.
func netemReset(ctx context.Context, s *session, a *args, check bool) (bool, error) {
	lab, name, iface := a.ident("lab"), a.ident("node"), a.ident("interface")
	if a.err != nil {
		return false, a.err
	}
	n, err := s.node(ctx, lab, name)
	if err != nil {
		return false, err
	}
	list, err := s.netemShow(ctx, lab, n)
	if err != nil {
		return false, err
	}
	prior := find(list, iface)
	if prior == nil {
		return false, s.stats(map[string]any{"interfaces": impairments(list), "name": n.Name})
	}
	if check {
		return true, s.stats(map[string]any{"interfaces": impairments(list), "name": n.Name})
	}
	set := netemSetUndo(lab, n, iface, prior, false)
	set.FQCN = "virt.clab.netem.set"
	if err := s.undo(set); err != nil {
		return false, err
	}
	if err := s.call(ctx, http.MethodPost, "/api/v1/tools/netem/reset", nil, map[string]any{"labName": lab, "nodeName": n.NodeName, "interface": iface}, nil); err != nil {
		return false, err
	}
	after, err := s.netemShow(ctx, lab, n)
	if err != nil {
		return true, err
	}
	return true, s.stats(map[string]any{"interfaces": impairments(after), "name": n.Name})
}
