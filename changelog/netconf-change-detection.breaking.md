`net.netconf.config` reports `changed` from reads of the configuration the edit can touch, before and after it, instead
of from the device answering ok, so a re-run that changes nothing reports no change; every task makes those two reads
(the whole datastore with `default_operation: replace`). Content nesting deeper than 256 elements (512 before), malformed
XML, and an unknown `default_operation` or `error_option` are refused before a session opens.
