`pleiades run` and `pleiades adhoc` take `--tls-keylog FILE`, which writes the run's TLS session secrets in the
NSS key log format to a new file, mode 0600, so a packet tool can decrypt a capture of the run's gNMI, RESTCONF
and `http.request` device sessions. A name that already exists is refused, and so is one inside the project (whose files a run's methods can read and send to
devices), every such run warns that the file
decrypts those sessions and the credentials in them, and a Runner never writes one.
