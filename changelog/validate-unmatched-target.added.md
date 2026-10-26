`pleiades validate` warns when a task's hosts or target matches no device or tag in the inventory, where it used to say
there were no issues and the run then failed. A run's text output names each device's warning once, not once per task.
