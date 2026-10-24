// Reading a GitHub Actions job's log: testgate's own account of each pass
// it ran (which failures passed when re-run alone, which failed again, and
// which a -strict pass did not re-run), each failure's output, and for a
// job that failed with no test to blame, the line that says why.
package main

import (
	"regexp"
	"strings"
)

var (
	// stampRE is the timestamp GitHub puts before every log line.
	stampRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z ?`)
	// headings are testgate's headings over a list of failures, each with
	// the verdict its list carries.
	headings = []struct {
		re      *regexp.Regexp
		verdict string
	}{
		{regexp.MustCompile(`^testgate: \d+ failure\(s\) passed when re-run alone`), VerdictContention},
		{regexp.MustCompile(`^testgate: \d+ failure\(s\) failed AGAIN when re-run alone`), VerdictReal},
		{regexp.MustCompile(`^testgate: \d+ failure\(s\), and -strict re-runs nothing`), VerdictStrict},
		{regexp.MustCompile(`^testgate: \d+ failure\(s\) were NOT re-run`), VerdictNotRerun},
	}
	// listedRE is one failure in a heading's list: its package and the rest.
	listedRE = regexp.MustCompile(`^  (\S+): (.+)$`)
	// outputRE opens a failure's output as testgate prints it.
	outputRE = regexp.MustCompile(`^--- (\S+) ?(\S*) ---$`)
	// whyRE opens a contention's output.
	whyRE = regexp.MustCompile(`^  why (\S+) (\S+) failed under load:$`)
	// noiseRE is a line around a job's failure that says nothing about it.
	noiseRE = regexp.MustCompile(`^(exit status \d+|make(\[\d+\])?: (\*\*\*|Leaving|Entering).*|##\[.*|testgate: \d+ failure\(s\).*|Error: Process completed.*|)$`)
)

// byteOrderMark is the mark a log may open with.
const byteOrderMark = "\xef\xbb\xbf"

// jobFailure is one failure testgate listed in a job's log.
type jobFailure struct {
	pkg, test, reason, verdict, err string
}

// jobLog is what one job's log says.
type jobLog struct {
	failures []jobFailure
	// reason is why the job failed, from the lines around its first error,
	// for a job with no test to blame.
	reason string
}

// packageReasons are the words testgate lists a package-level failure by,
// in place of a test.
var packageReasons = []string{"build failed", "the package's test binary failed or timed out"}

// parseJobLog reads a job's log. It is pure, so it is tested against real
// excerpts of real logs.
func parseJobLog(text string) jobLog {
	var lines []string
	for _, l := range strings.Split(strings.TrimPrefix(text, byteOrderMark), "\n") {
		l = escapeRE.ReplaceAllString(strings.TrimRight(stampRE.ReplaceAllString(l, ""), "\r"), "")
		lines = append(lines, l)
	}
	outputs := map[string][]string{}
	var out jobLog
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if m := outputRE.FindStringSubmatch(l); m != nil {
			key := m[1] + " " + m[2]
			for i+1 < len(lines) && lines[i+1] != "" && !strings.HasPrefix(lines[i+1], "testgate: ") && !outputRE.MatchString(lines[i+1]) {
				i++
				outputs[key] = append(outputs[key], lines[i])
			}
			continue
		}
		if m := whyRE.FindStringSubmatch(l); m != nil {
			key := m[1] + " " + m[2]
			for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "    ") {
				i++
				outputs[key] = append(outputs[key], strings.TrimPrefix(lines[i], "    "))
			}
			continue
		}
		for _, h := range headings {
			if !h.re.MatchString(l) {
				continue
			}
			for i+1 < len(lines) && lines[i+1] == "" {
				i++ // the blank line under the heading
			}
			for i+1 < len(lines) {
				m := listedRE.FindStringSubmatch(lines[i+1])
				if m == nil {
					break
				}
				i++
				out.failures = append(out.failures, listed(m[1], m[2], h.verdict))
			}
			break
		}
	}
	for j := range out.failures {
		f := &out.failures[j]
		f.err = firstError(strings.Join(outputs[f.pkg+" "+f.test], "\n"))
	}
	out.reason = jobReason(lines)
	return out
}

// listed is one failure from a heading's list: a test, or a package named
// by what failed, with anything testgate wrote after the test dropped.
func listed(pkg, rest, verdict string) jobFailure {
	f := jobFailure{pkg: pkg, verdict: verdict}
	for _, r := range packageReasons {
		if rest == r {
			f.reason = r
			return f
		}
	}
	f.test, _, _ = strings.Cut(rest, " ")
	return f
}

// jobReason is why a job failed: its first error annotation, or when that
// says only that a process exited, the last line before it that is not
// make's or testgate's framing.
func jobReason(lines []string) string {
	for i, l := range lines {
		msg, ok := strings.CutPrefix(l, "##[error]")
		if !ok {
			continue
		}
		if !strings.HasPrefix(msg, "Process completed with exit code") {
			return scrub(msg)
		}
		for j := i - 1; j >= 0; j-- {
			if t := strings.TrimSpace(lines[j]); !noiseRE.MatchString(t) {
				return scrub(t)
			}
		}
		return scrub(msg)
	}
	return ""
}
