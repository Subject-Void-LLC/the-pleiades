A task parameter of the wrong type is no longer silently treated as absent. `pleiades validate` refuses a value that
does not match the type its method declares, such as `stdin: 4` where text is expected, or `timeout: "600"` where a
number is, and says how to write it. Nothing is converted: an unquoted `0644` is the number 420, not the text `0644`.
A rendered value is held to the same types when the task runs, and a template rendering a number or a boolean into a
text parameter now arrives as text, so `stdin: "{{ vars.n }}"` sends `7`, where it used to send nothing.
`file.json` and `file.yaml`'s `key`, and `win.firewall.rule`'s ports, addresses and profiles, are declared as what
they always accepted: a single value or a list.
