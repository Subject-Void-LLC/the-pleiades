On a Cisco IOS console line, a message the device prints on its own (the login's `%SEC_LOGIN-5-LOGIN_SUCCESS`) and the
prompt after it were read as the next command's whole answer, so that command came back with the log line and every
command after it a response behind. Such a message, ahead of a command's echo, is now passed over (measured through
the CML console).
