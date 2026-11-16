// Package snmplive runs pkg/snmp, the net.snmp.* methods, wait.snmp and the
// SNMP onboarding probe against a real net-snmp agent in a container
// (testsupport.StartSNMPD): SNMPv1 and SNMPv2c communities and an SNMPv3 user
// for every authentication and privacy protocol the agent implements. It is
// apart from pkg/snmp so that package's own tests need no Docker and run on
// every platform's container-free legs.
package snmplive
