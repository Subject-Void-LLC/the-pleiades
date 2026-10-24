# genieparser fixtures

Copied by `tools/geniefixtures/gen.py` from Cisco's genieparser (https://github.com/CiscoTestAutomation/genieparser) at commit `e62f9467a383997e190e04679d769c5c009e7460`. Do not edit by hand: regenerate.

Copyright (c) 2021 Cisco Systems, Inc. and/or its affiliates. Licensed under the Apache License, Version 2.0; see `../LICENSE.genieparser` and `../NOTICE.genieparser`. The fixtures are genieparser's, unchanged except that each expected result was converted from a Python literal to JSON.

`skips.json` is not genieparser's: it lists, with a reason each, the fixtures the port does not pass.

Each `*_output.txt` and `*_arguments.json` is genieparser's own file, unchanged. Each `*_expected.json` is the `expected_output` literal of genieparser's `*_expected.py`, converted to JSON; an int dict key (ShowIpRoute's `next_hop_list` index) becomes its decimal string, as JSON writes it.

| Parser | OS | equal | empty |
|---|---|---|---|
| ShowVersion | iosxe | 27 | 1 |
| ShowVersion | ios | 7 | 1 |
| ShowInventory | iosxe | 18 | 1 |
| ShowIpInterfaceBrief | iosxe | 1 | 1 |
| ShowInterfaces | iosxe | 27 | 1 |
| ShowInterfaces | ios | 2 | 1 |
| ShowIpRoute | iosxe | 12 | 1 |
| ShowIpOspfNeighbor | iosxe | 3 | 1 |
| ShowIpBgpSummary | iosxe | 2 | 1 |
| ShowBgpSummary | iosxe | 2 | 1 |
| ShowBgpSummary | ios | 2 | 1 |
| ShowVlan | iosxe | 5 | 1 |
| ShowVlan | ios | 5 | 1 |
| ShowCdpNeighbors | iosxe | 10 | 2 |
| ShowCdpNeighbors | ios | 6 | 2 |
| ShowLldpNeighbors | iosxe | 2 | 1 |
| ShowLldpNeighbors | ios | 1 | 1 |
| ShowArchiveConfigDifferences | iosxe | 3 | 1 |
| ShowArchiveConfigDifferences | ios | 3 | 1 |
