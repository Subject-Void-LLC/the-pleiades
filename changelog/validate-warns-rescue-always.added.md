`pleiades validate` warns about every block's `rescue:` and `always:`, which are parsed and checked but not yet run:
the warning names the block and how many tasks will not run, so a cleanup step in `always:` can no longer be skipped
without a word. It never fails validation.
