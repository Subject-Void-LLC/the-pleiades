A schedule that could never run is now refused when you save it rather than failing silently at
whatever hour it was set for. That covers a template bound to a credential that prompts for input, a
saved configuration answering a survey password, a saved configuration belonging to a different
template, a project with no source to fetch, and a saved configuration on a project sync, which takes
none. Editing a schedule also keeps the saved configuration it runs with, which a rename previously
discarded without saying so.
