`examples/ai_repair_lab`: a runbook deploys a small web server in a BusyBox container, and when one of five faults
breaks it, its `rescue:` asks a local model (`ai.chat` on Ollama, `residency: local`) which of a fixed set of fixes
applies, runs the one it names, and checks again; `pleiades rollback` removes the container. The model picks a value
from a schema's `enum` and never writes a command.
