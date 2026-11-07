`pleiades forge migrate-playbook` converts a condition on one of Ansible's facts (`when: ansible_os_family ==
"Debian"`) into the native condition over a `facts.gather` it inserts at the start of the play, decided on each host
as Ansible decides it, with the value written the native way (`debian`, an integer major version). A value with no
native word, and a template reading a fact, stay findings to review.
