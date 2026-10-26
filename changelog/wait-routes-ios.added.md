`wait.routes` waits for a Cisco IOS router's routing table too (`show ip route`, the global table, read through
pkg/iosparse), over SSH, the controller's console or Telnet with `insecure_telnet`, as it does for FRR, and refuses any
other device before contacting it.
