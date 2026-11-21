// The rule that every key a container takes from a Secret this render
// creates is a key that Secret carries, and that the controller reads each
// key a profile says the chart's Secret must hand it.
//
// A secretKeyRef naming a key its Secret lacks renders and applies cleanly,
// and the pod then never starts (CreateContainerConfigError). That is the
// shape of an environment variable written without the condition its Secret
// key is written under, so every positive profile is held to it. A key the
// Secret carries that the controller never reads is the opposite mistake: an
// operator holds a key, during a change of the facts key say, that nothing
// uses.
package main

import "fmt"

// checkSecretKeys holds a render to both halves of this file's rule.
// controllerReads names the keys of the render's own Secret the controller
// must take as environment variables of the same name. The controller
// container is recognised by MASTER_ENCRYPTION_KEY, which only it reads.
func checkSecretKeys(profile string, objects []manifest, controllerReads []string) []finding {
	var findings []finding
	add := func(object, message string) {
		findings = append(findings, finding{profile: profile, object: object, message: message})
	}

	// What each Secret this render creates carries, by name. A Secret the
	// operator manages is not rendered, so a reference to it is not judged.
	carried := map[string]map[string]bool{}
	for _, obj := range objects {
		if obj.Kind != "Secret" {
			continue
		}
		keys := map[string]bool{}
		for k := range obj.StringData {
			keys[k] = true
		}
		for k := range obj.Data {
			keys[k] = true
		}
		carried[obj.Metadata.Name] = keys
	}

	controllers := 0
	for _, obj := range objects {
		if !workloadKinds[obj.Kind] {
			continue
		}
		name := obj.Kind + "/" + obj.Metadata.Name
		pod := obj.Spec.Template.Spec
		for _, c := range append(append([]container{}, pod.InitContainers...), pod.Containers...) {
			isController := false
			// The keys this container reads under their own names from a
			// Secret this render creates.
			read := map[string]bool{}
			for _, e := range c.Env {
				if e.Name == "MASTER_ENCRYPTION_KEY" {
					isController = true
				}
				if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
					continue
				}
				ref := e.ValueFrom.SecretKeyRef
				keys, rendered := carried[ref.Name]
				if !rendered {
					continue
				}
				if !keys[ref.Key] {
					add(name, fmt.Sprintf("container %s reads key %s from Secret %s, which does not carry it, so the pod would not start", c.Name, ref.Key, ref.Name))
					continue
				}
				if e.Name == ref.Key {
					read[ref.Key] = true
				}
			}
			if isController {
				controllers++
				for _, key := range controllerReads {
					if !read[key] {
						add(name, fmt.Sprintf("container %s does not read %s from the chart's Secret, so the key this profile sets reaches nothing", c.Name, key))
					}
				}
			}
		}
	}
	if len(controllerReads) > 0 && controllers == 0 {
		add("controller", "no controller container was found to examine, so this rule checked nothing")
	}
	return findings
}
