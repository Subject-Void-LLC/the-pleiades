Fixed secret masking removing the command word from `crypto key generate` and `crypto key zeroize` lines (and IOS's
"% The key modulus size" notice), which left an undo line in `net.ios.config`'s `undo_lines` that could not be run
again. The words after `crypto key` that name a command are now kept.
