// The container registry credential a method may ask its run for.
package sdk

// RegistryCredentials is implemented by a RunbookContext that can give a
// method the credential for one container registry. A method asks for the
// host its own image reference names, and gets nothing for any other, so a
// runbook cannot send one registry's password to another host. On the Crawl
// tier the answer comes from the project's credential store
// (`pleiades add-registry-credential`); on the Walk tier from the
// template's one registry credential.
type RegistryCredentials interface {
	// RegistryCredential returns the username and password stored for
	// host, and false when there is none.
	RegistryCredential(host string) (username, password string, ok bool)
}
