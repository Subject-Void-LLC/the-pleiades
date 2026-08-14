Added six credential types this platform ships and installs on every controller start:
Machine, Vault, Network, Amazon Web Services, Red Hat Ansible Automation Platform and
HCP Terraform, each under the same namespace AWX uses. Sixteen more AWX credential
types are recognised and reported as not implemented, each with the specific reason, so
an export naming one tells you what is missing rather than reporting an unknown type.
