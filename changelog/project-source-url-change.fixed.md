Changing a project's repository address now takes effect. A sync previously kept fetching from the
address the first clone used, so an edit changed nothing and the project went on serving the old
repository while reporting success. A project repointed at a different repository is now checked out
afresh, and if that fetch fails the previous checkout keeps serving so templates that name it go on
working.
