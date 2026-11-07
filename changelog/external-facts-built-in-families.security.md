An external Collection can no longer declare a fact a built-in method holds, or one inside a built-in family
(`package.`, `service.`, `service_state.`, `file.`, `port.`, `user.`, `group.`, `ansible_`): it could have made a
device's facts say what the device is not, and so steered targeting and baselines. Such a program is refused at load.
