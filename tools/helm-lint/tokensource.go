// The rule that the controller is given exactly one way of checking API
// tokens: a JWT secret from the Secret, or an identity provider's JWKS URL.
//
// The controller refuses to start with both (FAILURE_PATTERNS 1229), so a
// render that set both would install cleanly and then crash-loop, and one
// that set neither would do the same with a different message. Every
// positive profile and both setup renders are held to it.
package main

import (
	"fmt"
	"strings"
)

// checkTokenSource holds a render to one token check. wantJWKS is the JWKS
// URL the profile set, or empty when the profile uses the JWT secret.
//
// With a JWKS URL the controller container sets JWKS_URL to exactly that
// value and no JWT_SECRET, and no Secret the chart renders carries
// JWT_SECRET. Without one the controller reads JWT_SECRET and sets no
// JWKS_URL. The container is recognised by MASTER_ENCRYPTION_KEY, which only
// the controller reads, so a sidecar added later is not mistaken for it.
func checkTokenSource(profile string, objects []manifest, wantJWKS string) []finding {
	var findings []finding
	add := func(object, message string) {
		findings = append(findings, finding{profile: profile, object: object, message: message})
	}

	checked := 0
	for _, obj := range objects {
		name := obj.Kind + "/" + obj.Metadata.Name
		if obj.Kind == "Secret" && wantJWKS != "" {
			if _, has := obj.StringData["JWT_SECRET"]; has {
				add(name, "carries JWT_SECRET while controller.jwksUrl is set; the controller checks tokens against the identity provider and refuses a secret beside it")
			}
		}
		if obj.Kind != "Deployment" || !strings.HasSuffix(obj.Metadata.Name, "-controller") {
			continue
		}
		for _, c := range obj.Spec.Template.Spec.Containers {
			env := map[string]envVar{}
			for _, e := range c.Env {
				env[e.Name] = e
			}
			if _, isController := env["MASTER_ENCRYPTION_KEY"]; !isController {
				continue
			}
			checked++
			jwks, hasJWKS := env["JWKS_URL"]
			_, hasSecret := env["JWT_SECRET"]
			switch {
			case wantJWKS != "" && (!hasJWKS || jwks.Value != wantJWKS):
				add(name, fmt.Sprintf("container %s sets JWKS_URL to %q, want controller.jwksUrl's %q", c.Name, jwks.Value, wantJWKS))
			case wantJWKS != "" && hasSecret:
				add(name, fmt.Sprintf("container %s sets JWT_SECRET beside JWKS_URL, and the controller refuses to start with both", c.Name))
			case wantJWKS == "" && !hasSecret:
				add(name, fmt.Sprintf("container %s sets no JWT_SECRET and no JWKS URL was configured, so the controller refuses to start", c.Name))
			case wantJWKS == "" && hasJWKS:
				add(name, fmt.Sprintf("container %s sets JWKS_URL though controller.jwksUrl is empty", c.Name))
			}
		}
	}
	if checked == 0 {
		add("controller", "no controller container was found to examine, so this rule checked nothing")
	}
	return findings
}
