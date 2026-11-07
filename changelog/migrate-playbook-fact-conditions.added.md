`pleiades forge migrate-playbook` converts an equality or membership test on one of Ansible's facts (`when:
ansible_os_family == "Debian"`) in a play that gathers facts into the native condition over a `facts.gather` it
inserts at the start of the play, decided on each host as Ansible decides it, with the value written the native way.
Orderings, filters, pattern tests, truth reads, values with no one native word and templates reading a fact stay
findings to review.
