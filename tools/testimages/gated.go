// Leaving out an image no test can start where this command runs.
package main

import (
	"os"
	"strings"
)

// tokenGated maps the prefix of an image no test can start without a token
// to the environment variable that holds it. Every test that starts
// LocalStack asks testsupport.LocalStackToken first, which skips the test
// when LOCALSTACK_AUTH_TOKEN is empty (TestEveryLocalStackStartIsGated holds
// that true), and no hosted CI job carries the token, so listing the image
// there had every containers shard pull or load 2.1 GB that nothing started,
// and cache it once per shard.
var tokenGated = map[string]string{"localstack/": "LOCALSTACK_AUTH_TOKEN"}

// startable returns images less those tokenGated leaves out because getenv
// has no token for them, and the ones it left out.
func startable(images []string, getenv func(string) string) (kept, omitted []string) {
	for _, image := range images {
		gated := false
		for prefix, variable := range tokenGated {
			if strings.HasPrefix(image, prefix) && getenv(variable) == "" {
				gated = true
			}
		}
		if gated {
			omitted = append(omitted, image)
			continue
		}
		kept = append(kept, image)
	}
	return kept, omitted
}

// startableHere is startable with this process's environment.
func startableHere(images []string) (kept, omitted []string) {
	return startable(images, os.Getenv)
}
