Added the `virt.cml` methods, which build and take apart Cisco Modeling Labs labs, nodes and links through a
`cml_controller` device, each change with its own undo, and `node.create` seeding a node with an inventory device's
login as IOS type 9 secrets (kept by the controller in the node's configuration, so seed only from a lab-only
credential) and its address on a management interface. A Cisco device can now be reached through a
CML controller's console server (`transport: console`, `import-cml --via console`), its line logged out after every
task that still holds its connection. `tls_fingerprint` pins one certificate for any device, and `import-cml --controller` reads a controller device's
server, trust and login from the vault.
