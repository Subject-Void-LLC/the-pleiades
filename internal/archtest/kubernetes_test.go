// Package archtest: where Kubernetes' client libraries may be imported, and
// that no ambient Kubernetes credential is ever reached (Part XVIII D5).
package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// kubernetesPorts are the packages allowed to import k8s.io and helm.sh:
// the Kubernetes port, and Helm's when 119h adds it.
var kubernetesPorts = map[string]bool{
	modulePath + "/pkg/kubeapi":     true,
	modulePath + "/pkg/helmrelease": true,
}

// kubernetesImport reports whether an import path is one of Kubernetes' or
// Helm's own libraries.
func kubernetesImport(path string) bool {
	return strings.HasPrefix(path, "k8s.io/") || strings.HasPrefix(path, "sigs.k8s.io/") || strings.HasPrefix(path, "helm.sh/")
}

// TestOnlyTheKubernetesPortImportsClientGo holds D5's confinement: every
// other package reaches a cluster through pkg/kubeapi, so client-go's
// choices (what a kubeconfig may run, which credential is ambient) are
// made in one place. The control: the port itself must be found importing
// client-go, or the query is misaimed.
func TestOnlyTheKubernetesPortImportsClientGo(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/...")
	if len(pkgs) == 0 {
		t.Fatal("go list matched no packages in the module, so this rule examined nothing")
	}
	portImports := false
	for _, pkg := range pkgs {
		for _, imp := range pkg.Imports {
			if !kubernetesImport(imp) {
				continue
			}
			if kubernetesPorts[pkg.ImportPath] {
				portImports = true
				continue
			}
			t.Errorf("%s imports %s; only %s may, so a cluster is reached through the port", pkg.ImportPath, imp, modulePath+"/pkg/kubeapi")
		}
	}
	if !portImports {
		t.Error("pkg/kubeapi imports no k8s.io package, so this rule's query is misaimed")
	}
}

// ambientEntryPoints are client-go's ways to a credential nobody bound: an
// in-cluster ServiceAccount, the kubeconfig loading rules (KUBECONFIG and
// ~/.kube/config), and a kubeconfig turned into a client, which is what
// runs its exec and auth-provider programs.
var ambientEntryPoints = map[string]bool{
	"InClusterConfig":                              true,
	"BuildConfigFromFlags":                         true,
	"BuildConfigFromKubeconfigGetter":              true,
	"NewDefaultClientConfigLoadingRules":           true,
	"NewNonInteractiveDeferredLoadingClientConfig": true,
	"NewInteractiveDeferredLoadingClientConfig":    true,
	"NewDefaultClientConfig":                       true,
	"NewNonInteractiveClientConfig":                true,
	"NewInteractiveClientConfig":                   true,
	"RESTConfigFromKubeConfig":                     true,
	"RecommendedHomeFile":                          true,
	"RecommendedConfigPathEnvVar":                  true,
}

// TestNoAmbientKubernetesCredential reads every non-test file of the
// Kubernetes port for a call or reference to one of client-go's ambient
// entry points, and every non-test file in the module for the KUBECONFIG
// variable's name. A Collection must reach a cluster with the credential
// bound to it and nothing else (D5's edge case 6). The control: the port's
// own files must be found and parsed.
func TestNoAmbientKubernetesCredential(t *testing.T) {
	pkgs := goList(t, false, modulePath+"/...")
	parsed := 0
	fset := token.NewFileSet()
	for _, pkg := range pkgs {
		for _, name := range pkg.GoFiles {
			path := filepath.Join(pkg.Dir, name)
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parsing %s: %v", path, err)
			}
			if kubernetesPorts[pkg.ImportPath] {
				parsed++
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.SelectorExpr:
					if kubernetesPorts[pkg.ImportPath] && ambientEntryPoints[n.Sel.Name] {
						t.Errorf("%s uses %s, an ambient Kubernetes credential; a cluster is reached only with its bound credential",
							fset.Position(n.Pos()), n.Sel.Name)
					}
				case *ast.BasicLit:
					if n.Kind == token.STRING && strings.Trim(n.Value, "\"`") == "KUBECONFIG" {
						t.Errorf("%s names KUBECONFIG; nothing reads an ambient kubeconfig", fset.Position(n.Pos()))
					}
				}
				return true
			})
		}
	}
	if parsed == 0 {
		t.Error("no file of pkg/kubeapi was parsed, so this rule examined nothing")
	}
}
