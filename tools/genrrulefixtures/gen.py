#!/usr/bin/env python3
"""Generate AWX-parity golden vectors for internal/schedule/rrule.

AWX computes schedule occurrences with python-dateutil's rrule. This script
expands a table of representative rules with that exact library and writes
the results to internal/schedule/rrule/testdata/awx_parity.json, which the
Go tests assert against.

The JSON is COMMITTED. Python is deliberately NOT a build or CI dependency:
`make ci` never runs this file, and nothing in the Go build references it.
Regenerating is a manual step, the same way `go generate ./internal/ent` is,
and is only needed when a case is added or dateutil's own behaviour changes.

    pip install python-dateutil
    python3 tools/genrrulefixtures/gen.py

Cases are chosen for the places a hand-rolled expander and dateutil can
plausibly disagree, not for coverage of happy paths: daylight-saving
transitions in both hemispheres, a zone with a half-hour offset and no DST,
leap days, month lengths, ordinal weekdays, BYSETPOS, and exclusion rules
that straddle a transition.
"""

import json
import os
from datetime import datetime
from zoneinfo import ZoneInfo

from dateutil.rrule import rrulestr

# Each case is (name, timezone, dtstart, rrule, [exclusions], count).
# dtstart is a naive wall-clock time, localised into the case's zone -- the
# same split the Schedule entity stores (a local dtstart plus a named zone),
# so the fixtures exercise the shape production actually uses.
CASES = [
    # --- Daylight saving, northern hemisphere, spring forward -------------
    ("daily_9am_spring_forward_ny", "America/New_York",
     "2024-03-08T09:00:00", "FREQ=DAILY", [], 10),
    ("daily_0230_into_spring_gap_ny", "America/New_York",
     "2024-03-08T02:30:00", "FREQ=DAILY", [], 6),
    ("hourly_across_spring_forward_ny", "America/New_York",
     "2024-03-10T00:00:00", "FREQ=HOURLY", [], 8),

    # --- Daylight saving, northern hemisphere, fall back ------------------
    ("daily_0130_across_fall_back_ny", "America/New_York",
     "2024-11-01T01:30:00", "FREQ=DAILY", [], 8),
    ("daily_9am_fall_back_london", "Europe/London",
     "2024-10-25T09:00:00", "FREQ=DAILY", [], 8),
    ("weekly_mon_across_bst_end", "Europe/London",
     "2024-10-21T07:30:00", "FREQ=WEEKLY;BYDAY=MO", [], 6),

    # --- Southern hemisphere: transitions run the other way ---------------
    ("daily_9am_sydney_dst_start", "Australia/Sydney",
     "2024-10-04T09:00:00", "FREQ=DAILY", [], 8),
    ("daily_9am_sydney_dst_end", "Australia/Sydney",
     "2024-04-05T09:00:00", "FREQ=DAILY", [], 8),

    # --- Half-hour offset, no DST at all ----------------------------------
    ("daily_kolkata", "Asia/Kolkata",
     "2024-03-08T09:00:00", "FREQ=DAILY", [], 6),
    ("weekly_kolkata_bymin", "Asia/Kolkata",
     "2024-01-01T06:15:00", "FREQ=WEEKLY;BYDAY=MO,TH", [], 8),

    # --- UTC baseline ------------------------------------------------------
    ("hourly_interval_2_utc", "UTC", "2024-01-01T00:00:00",
     "FREQ=HOURLY;INTERVAL=2", [], 10),
    ("minutely_interval_15_utc", "UTC", "2024-01-01T00:00:00",
     "FREQ=MINUTELY;INTERVAL=15", [], 10),

    # --- Month lengths and leap days --------------------------------------
    ("monthly_31st_skips_short_months", "UTC", "2024-01-31T00:00:00",
     "FREQ=MONTHLY;BYMONTHDAY=31", [], 10),
    ("monthly_last_day", "UTC", "2024-01-31T08:00:00",
     "FREQ=MONTHLY;BYMONTHDAY=-1", [], 14),
    ("yearly_leap_day", "UTC", "2024-02-29T12:00:00",
     "FREQ=YEARLY;BYMONTH=2;BYMONTHDAY=29", [], 4),
    ("monthly_29th_across_feb", "UTC", "2024-01-29T00:00:00",
     "FREQ=MONTHLY;BYMONTHDAY=29", [], 14),

    # --- Ordinal weekdays --------------------------------------------------
    ("monthly_last_friday", "America/New_York", "2024-01-01T09:00:00",
     "FREQ=MONTHLY;BYDAY=-1FR", [], 12),
    ("monthly_first_and_last_monday", "UTC", "2024-01-01T09:00:00",
     "FREQ=MONTHLY;BYDAY=1MO,-1MO", [], 12),
    ("monthly_second_tuesday", "UTC", "2024-01-01T03:00:00",
     "FREQ=MONTHLY;BYDAY=2TU", [], 10),
    ("yearly_last_thursday_november", "America/New_York",
     "2024-01-01T09:00:00", "FREQ=YEARLY;BYMONTH=11;BYDAY=-1TH", [], 4),

    # --- BYSETPOS ----------------------------------------------------------
    ("monthly_third_weekday", "UTC", "2024-01-01T06:00:00",
     "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=3", [], 10),
    ("monthly_last_weekday", "UTC", "2024-01-01T06:00:00",
     "FREQ=MONTHLY;BYDAY=MO,TU,WE,TH,FR;BYSETPOS=-1", [], 10),
    ("monthly_last_weekend_day", "UTC", "2024-01-01T22:00:00",
     "FREQ=MONTHLY;BYDAY=SA,SU;BYSETPOS=-1", [], 10),

    # --- WKST changes which weeks an INTERVAL>1 weekly rule selects --------
    ("biweekly_wkst_mo", "UTC", "2024-01-07T12:00:00",
     "FREQ=WEEKLY;INTERVAL=2;BYDAY=SU,MO;WKST=MO", [], 8),
    ("biweekly_wkst_su", "UTC", "2024-01-07T12:00:00",
     "FREQ=WEEKLY;INTERVAL=2;BYDAY=SU,MO;WKST=SU", [], 8),

    # --- COUNT and UNTIL ---------------------------------------------------
    ("daily_count_5", "UTC", "2024-01-01T00:00:00",
     "FREQ=DAILY;COUNT=5", [], 10),
    ("daily_until_utc", "UTC", "2024-01-01T00:00:00",
     "FREQ=DAILY;UNTIL=20240110T000000Z", [], 20),
    ("weekly_until_across_dst_ny", "America/New_York", "2024-02-01T09:00:00",
     "FREQ=WEEKLY;BYDAY=TH;UNTIL=20240418T130000Z", [], 20),

    # --- BYHOUR / BYMINUTE fan-out ----------------------------------------
    ("daily_two_times", "America/New_York", "2024-03-08T00:00:00",
     "FREQ=DAILY;BYHOUR=6,18;BYMINUTE=30", [], 12),
    ("weekly_business_hours", "UTC", "2024-01-01T00:00:00",
     "FREQ=WEEKLY;BYDAY=MO,WE,FR;BYHOUR=9;BYMINUTE=0", [], 9),

    # --- Exclusions: the release gate's own shape -------------------------
    ("daily_excluding_weekends_across_spring_forward", "America/New_York",
     "2024-03-07T09:00:00", "FREQ=DAILY", ["FREQ=WEEKLY;BYDAY=SA,SU"], 10),
    ("daily_excluding_weekends_across_fall_back", "America/New_York",
     "2024-10-31T09:00:00", "FREQ=DAILY", ["FREQ=WEEKLY;BYDAY=SA,SU"], 10),
    ("hourly_excluding_night_hours", "UTC", "2024-01-01T00:00:00",
     "FREQ=HOURLY", ["FREQ=HOURLY;BYHOUR=0,1,2,3,4,5"], 10),
    ("daily_excluding_first_of_month", "Europe/London",
     "2024-03-28T07:00:00", "FREQ=DAILY", ["FREQ=MONTHLY;BYMONTHDAY=1"], 10),
    ("weekly_excluding_one_exdate", "America/New_York",
     "2024-03-07T09:00:00", "FREQ=DAILY",
     ["EXDATE;TZID=America/New_York:20240311T090000"], 8),
    ("daily_excluding_two_exdates_utc", "UTC", "2024-01-01T00:00:00",
     "FREQ=DAILY", ["EXDATE:20240103T000000Z,20240105T000000Z"], 8),
]


def expand(zone, dtstart_text, rule, exclusions, count):
    tz = ZoneInfo(zone)
    dtstart = datetime.fromisoformat(dtstart_text).replace(tzinfo=tz)

    lines = ["RRULE:" + rule]
    for ex in exclusions:
        if ex.upper().startswith("EXDATE"):
            lines.append(ex)
        else:
            lines.append("EXRULE:" + ex)

    rs = rrulestr("\n".join(lines), dtstart=dtstart, forceset=True)

    out = []
    for occ in rs:
        out.append({
            "local": occ.strftime("%Y-%m-%dT%H:%M:%S%z"),
            "utc": occ.astimezone(ZoneInfo("UTC")).strftime("%Y-%m-%dT%H:%M:%SZ"),
        })
        if len(out) >= count:
            break
    return out


def main():
    root = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
    dest = os.path.join(root, "internal", "schedule", "rrule", "testdata", "awx_parity.json")
    os.makedirs(os.path.dirname(dest), exist_ok=True)

    fixtures = []
    for name, zone, dtstart, rule, exclusions, count in CASES:
        fixtures.append({
            "name": name,
            "timezone": zone,
            "dtstart": dtstart,
            "rrule": rule,
            "exclusions": exclusions,
            "occurrences": expand(zone, dtstart, rule, exclusions, count),
        })

    payload = {
        "_comment": (
            "GENERATED by tools/genrrulefixtures/gen.py from python-dateutil, "
            "the recurrence library AWX schedules on. Do not hand-edit. "
            "Python is not a build or CI dependency; this file is committed "
            "and regenerated manually."
        ),
        "cases": fixtures,
    }
    with open(dest, "w") as f:
        json.dump(payload, f, indent=2)
        f.write("\n")
    print(f"wrote {len(fixtures)} cases to {dest}")


if __name__ == "__main__":
    main()
