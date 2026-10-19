`virt.vbox.vm.delete` deletes a saved VM. A VM whose state `virt.vbox.vm.stop` saved counted as off, but
VirtualBox refuses to detach media from it ("The machine is not mutable or running (state is Saved)"), so
the delete failed at its first step with nothing deleted. It now discards the saved state first, through
the new `vboxmanage.Host.DiscardState`; a check still sends nothing.
