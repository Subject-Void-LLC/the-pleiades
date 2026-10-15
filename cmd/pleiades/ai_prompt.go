// Package main: `pleiades ai prompt keygen` and `sign`, which make a
// prompt signing key and sign a prompt bundle with it. Both need a person
// at a terminal: a signature says a person approved a document, which an
// agent or a pipeline cannot say for them.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Subject-Void-LLC/the-pleiades/internal/engine"
	"github.com/Subject-Void-LLC/the-pleiades/internal/prompt"
	"github.com/Subject-Void-LLC/the-pleiades/internal/termsafe"
	"github.com/Subject-Void-LLC/the-pleiades/pkg/promptsign"
)

// runAIPromptKeygen makes an Ed25519 signing key: the private half in a
// file of its own, mode 0600, outside the project so it is never
// committed beside the prompts it signs; the public half printed as the
// trusted keys entry to add.
func runAIPromptKeygen(args []string) error {
	fs := flag.NewFlagSet("ai prompt keygen", flag.ContinueOnError)
	out := fs.String("out", "", "the private key file to write, outside the project")
	dir := fs.String("dir", ".", "project directory")
	uses := fs.String("uses", "", "the uses the key may sign for, comma separated, for the trusted keys entry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *out == "":
		return errors.New("usage: pleiades ai prompt keygen --out <file> [--uses triage,summarize] [--dir .]")
	case insideDir(*out, *dir):
		return fmt.Errorf("%s is inside the project %s: a private key there is one commit from being published; keep it outside", *out, *dir)
	}
	term, err := prompt.OpenPerson("making a prompt signing key")
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore() }()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("writing the key: %w", err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	useList := "[]"
	if *uses != "" {
		useList = "[" + strings.Join(strings.Split(*uses, ","), ", ") + "]"
	}
	fmt.Printf("wrote %s (key %s)\n\nadd it to %s:\n\nkeys:\n  - public_key: %s\n    uses: %s\n", *out, promptsign.KeyID(pub),
		filepath.Join(*dir, engine.AITrustedKeysFile), base64.StdEncoding.EncodeToString(pub), useList)
	return nil
}

// runAIPromptSign signs a prompt bundle (or a parameter set or routing
// table) after showing it to the person signing and asking them to
// confirm, and writes its detached signature beside it as <file>.sig.
func runAIPromptSign(args []string) error {
	file, rest, err := splitPositional(args, nil)
	if err != nil {
		return fmt.Errorf("usage: pleiades ai prompt sign <bundle.json> --key <file> [--dir .]: %w", err)
	}
	fs := flag.NewFlagSet("ai prompt sign", flag.ContinueOnError)
	keyFile := fs.String("key", "", "the private key file, as keygen writes it")
	dir := fs.String("dir", ".", "project directory")
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if *keyFile == "" {
		return errors.New("--key is required: the private key file to sign with")
	}
	term, err := prompt.OpenPerson("signing a prompt")
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore() }()
	project, rel, err := projectFile(*dir, file)
	if err != nil {
		return err
	}
	defer func() { _ = project.Close() }()
	raw, err := readFromRoot(project, rel, promptsign.MaxDocumentBytes)
	if err != nil {
		return err
	}
	doc, err := promptsign.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	key, err := readSigningKey(*keyFile)
	if err != nil {
		return err
	}
	w := term.Out().(*os.File)
	fmt.Fprintf(w, "%s %s, version %d, for use %s\n", doc.Kind, termsafe.EscapeLine(doc.Name), doc.Version, termsafe.EscapeLine(doc.Use))
	if doc.Expires != "" {
		fmt.Fprintf(w, "expires %s\n", doc.Expires)
	}
	fmt.Fprintf(w, "sha256 %s\n\n", doc.Hash)
	if doc.Text != "" {
		fmt.Fprintln(w, "text:")
		printEscapedBlock(w, doc.Text)
	}
	for _, part := range []struct {
		name string
		v    any
	}{{"variants", doc.Variants}, {"parameters", doc.Parameters}, {"body", doc.Body}} {
		if b, _ := json.MarshalIndent(part.v, "", "  "); len(b) > 0 && string(b) != "null" && string(b) != "{}" {
			fmt.Fprintf(w, "%s:\n", part.name)
			printEscapedBlock(w, string(b))
		}
	}
	answer, err := term.Line("\nsign this, as key " + promptsign.KeyID(key.Public().(ed25519.PublicKey)) + "? type sign: ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(answer) != "sign" {
		return errors.New("not signed")
	}
	sig, err := json.Marshal(promptsign.Sign(doc, key))
	if err != nil {
		return err
	}
	if err := project.WriteFile(rel+".sig", append(sig, '\n'), 0o644); err != nil { // #nosec G306 -- a signature is public
		return err
	}
	fmt.Printf("wrote %s.sig\n", file)
	warnUntrusted(*dir, key.Public().(ed25519.PublicKey), doc.Use)
	return nil
}

// projectFile opens the project in dir as an os.Root and returns file's
// path inside it, refusing a file outside the project or that is not a
// .json document: what is signed is the project's own prompts, parameter
// sets and routing tables, and nothing read or written here can leave it.
func projectFile(dir, file string) (*os.Root, string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}
	absFile, err := filepath.Abs(file)
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(absDir, absFile)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !strings.HasSuffix(rel, ".json") {
		return nil, "", fmt.Errorf("%s is not a .json document inside the project %s: sign the project's own bundles (--dir names the project)", file, dir)
	}
	root, err := os.OpenRoot(absDir)
	if err != nil {
		return nil, "", err
	}
	return root, rel, nil
}

// readFromRoot reads at most limit bytes of rel inside root, refusing a
// larger file.
func readFromRoot(root *os.Root, rel string, limit int64) ([]byte, error) {
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s is over %d bytes", rel, limit)
	}
	return data, nil
}

// readSigningKey reads a PKCS#8 Ed25519 private key, refusing a file
// others can read, as ssh refuses one.
func readSigningKey(path string) (ed25519.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by others (mode %v): chmod 600 it", path, info.Mode().Perm())
	}
	data, err := readBounded(path, 16<<10)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s is not a PEM private key", path)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an Ed25519 key", path)
	}
	return key, nil
}

// warnUntrusted says when the project's trusted keys would not verify
// what was just signed: the key not listed, or listed without the use.
func warnUntrusted(dir string, pub ed25519.PublicKey, use string) {
	path := filepath.Join(dir, engine.AITrustedKeysFile)
	data, err := readBounded(path, promptsign.MaxKeysFileBytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s cannot be read (%v), so nothing verifies this signature yet\n", path, err)
		return
	}
	keys, err := promptsign.ParseTrustedKeys(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s: %v\n", path, err)
		return
	}
	for _, k := range keys {
		if k.ID() == promptsign.KeyID(pub) {
			if !slices.Contains(k.Uses, use) {
				fmt.Fprintf(os.Stderr, "warning: key %s is trusted for %s, not %s, so verification will refuse this\n", k.ID(), strings.Join(k.Uses, ", "), use)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "warning: key %s is not in %s, so verification will refuse this until it is\n", promptsign.KeyID(pub), path)
}
