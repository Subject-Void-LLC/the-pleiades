`win.feature.install` and `win.feature.remove` read a feature's state with `dism.exe /english`, so they work on a
Windows host in any language. Without it, a German host printed `Status : Aktiviert`, and the feature could not be
read at all.
