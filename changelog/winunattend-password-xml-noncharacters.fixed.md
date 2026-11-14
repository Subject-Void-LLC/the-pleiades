A Windows answer file now refuses a password holding U+FFFE or U+FFFF, which XML cannot carry: the answer file used to write U+FFFD in its place, so the VM took a password other than the stored one.
