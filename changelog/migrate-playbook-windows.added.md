`pleiades forge migrate-playbook` converts Windows modules: `win_optional_feature` to `win.feature.install` and
`.remove`, `win_reboot` to `win.reboot`, `win_get_url` with a SHA-256 checksum to `win.file.download`, and
`win_firewall` to `win.firewall.profile`, one task per profile. `win_firewall_rule`, `win_feature` and
`win_scheduled_task` are named as needing a person, with the reason: a rule found by display name, Server Manager
names that differ from DISM's, and lists of actions and triggers.
`win_service`'s `start_mode` (auto to `svc.windows.enable`, disabled to `svc.windows.disable`) and `win_copy` of a
file on the controller (to `win.file.copy`) convert as well.
