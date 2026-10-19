`virt.vbox.vm.stop` with mode `acpi` presses the power button again every 30 seconds while it waits. An idle Windows
guest spends the first press waking its display and nothing more, so a stop of one left idle over ten minutes used
to wait out its timeout and fail with the guest still running.
