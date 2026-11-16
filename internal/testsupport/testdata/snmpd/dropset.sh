#!/bin/sh
# A net-snmp pass script for one object that accepts every SET and keeps
# none: the agent answers the SET as done, and a read back still finds the
# old value, as IOS XE 17.15.1 accepted SNMPv3 users it never created. The
# object is .1.3.6.1.4.1.8072.9999.1.0, an octet string under net-snmp's own
# experimental branch.
OID=.1.3.6.1.4.1.8072.9999.1.0
case "$1" in
-g)
	if [ "$2" = "$OID" ]; then
		printf '%s\nstring\nkept as it was\n' "$OID"
	fi
	;;
-n)
	case "$2" in
	.1.3.6.1.4.1.8072.9999 | .1.3.6.1.4.1.8072.9999.1)
		printf '%s\nstring\nkept as it was\n' "$OID"
		;;
	esac
	;;
-s)
	# Success, and nothing stored.
	;;
esac
exit 0
