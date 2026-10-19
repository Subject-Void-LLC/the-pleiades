`wait.connection` now logs in afresh on every try. In a run that keeps SSH connections open, a try could reuse an earlier task's session and pass without logging in at all.
