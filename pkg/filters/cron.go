package filters

import (
	"fmt"
	"strconv"
	"strings"
)

// cronFieldBounds names the [min,max] a standard 5-field cron
// expression's fields accept, in field order: minute, hour, day of
// month, month, day of week (0 = Sunday). This is the same 5-field shape
// cron(8) uses, not the 6-or-7-field forms some schedulers add for
// seconds or years.
var cronFieldBounds = [5][2]int{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week
}

// cronSchedule is the parsed form of a 5-field cron expression: each
// field expanded to its exact set of matching values. This structure,
// not just a pass/fail bool, is what Phase 55's CronNextRun/
// CronPreviousRun are expected to walk one candidate minute at a time
// against; this phase's own IsValidCronExpr only needs to know
// parseCronExpr returned no error.
type cronSchedule struct {
	minute, hour, dayOfMonth, month, dayOfWeek map[int]bool
}

// parseCronExpr parses expr as a standard 5-field cron expression
// (minute hour day-of-month month day-of-week). Each field accepts "*",
// a literal, a range ("a-b"), a step ("*/n" or "a-b/n"), or a
// comma-separated list of any of those. It deliberately does not accept
// the non-standard named shorthands some cron implementations add
// ("@daily", "@hourly"): this is a small, hand-rolled parser, matching
// this project's own preference for owning a small parser over taking a
// dependency for one, not a full-featured cron implementation.
func parseCronExpr(expr string) (cronSchedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return cronSchedule{}, fmt.Errorf("filters: cron expression must have 5 fields, got %d", len(fields))
	}
	sets := make([]map[int]bool, 5)
	for i, f := range fields {
		set, err := parseCronField(f, cronFieldBounds[i][0], cronFieldBounds[i][1])
		if err != nil {
			return cronSchedule{}, fmt.Errorf("filters: cron field %d (%q): %w", i, f, err)
		}
		sets[i] = set
	}
	return cronSchedule{
		minute:     sets[0],
		hour:       sets[1],
		dayOfMonth: sets[2],
		month:      sets[3],
		dayOfWeek:  sets[4],
	}, nil
}

func parseCronField(field string, min, max int) (map[int]bool, error) {
	set := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		if part == "" {
			return nil, fmt.Errorf("empty list item")
		}
		rangePart, step, err := splitCronStep(part)
		if err != nil {
			return nil, err
		}
		lo, hi := min, max
		if rangePart != "*" {
			lo, hi, err = parseCronRange(rangePart, min, max)
			if err != nil {
				return nil, err
			}
		}
		if step < 1 {
			return nil, fmt.Errorf("step must be at least 1, got %d", step)
		}
		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	return set, nil
}

// splitCronStep splits "a-b/n" or "*/n" into its range/wildcard part and
// step, defaulting to step 1 when there is no "/n" suffix at all.
func splitCronStep(part string) (rangePart string, step int, err error) {
	idx := strings.IndexByte(part, '/')
	if idx < 0 {
		return part, 1, nil
	}
	rangePart = part[:idx]
	stepStr := part[idx+1:]
	step, err = strconv.Atoi(stepStr)
	if err != nil {
		return "", 0, fmt.Errorf("invalid step %q", stepStr)
	}
	return rangePart, step, nil
}

// parseCronRange parses "a-b" or a bare "a" into an inclusive [lo,hi]
// pair, bounds-checked against [min,max].
func parseCronRange(part string, min, max int) (int, int, error) {
	if idx := strings.IndexByte(part, '-'); idx >= 0 {
		loStr, hiStr := part[:idx], part[idx+1:]
		lo, err := strconv.Atoi(loStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range start %q", loStr)
		}
		hi, err := strconv.Atoi(hiStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range end %q", hiStr)
		}
		if lo < min || hi > max || lo > hi {
			return 0, 0, fmt.Errorf("range %d-%d out of bounds [%d,%d]", lo, hi, min, max)
		}
		return lo, hi, nil
	}
	v, err := strconv.Atoi(part)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid value %q", part)
	}
	if v < min || v > max {
		return 0, 0, fmt.Errorf("value %d out of bounds [%d,%d]", v, min, max)
	}
	return v, v, nil
}

// IsValidCronExpr reports whether expr parses as a standard 5-field cron
// expression via parseCronExpr, this file's own real parser: there is no
// separate regex-based approximation for this to disagree with, so
// IsValidCronExpr returning true and parseCronExpr succeeding on the
// same input are the same event by construction.
func IsValidCronExpr(expr string) bool {
	if len(expr) > MaxInputBytes {
		return false
	}
	_, err := parseCronExpr(expr)
	return err == nil
}
