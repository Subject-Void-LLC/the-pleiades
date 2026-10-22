The file methods now read a path that starts with a dash as a path. Before, such a path reached `stat` and `readlink` on the device as an option, so the method could misreport what was there.
