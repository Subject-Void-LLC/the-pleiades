#!/bin/bash
# Brings up MariaDB, creates the wordpress database and user (idempotent,
# so a container restart never fails on "database already exists"),
# writes wp-config.php from the sample on first boot only, starts
# Apache, then execs sshd in the foreground as PID 1.
set -euo pipefail

WP_DB=wordpress
WP_DB_USER=wordpress
WP_DB_PASSWORD=pleiades-lab-2026
WP_DIR=/var/www/html/wordpress

service mariadb start

for i in $(seq 1 30); do
	if mysqladmin ping --silent 2>/dev/null; then
		break
	fi
	sleep 1
done

mysql -u root <<-SQL
	CREATE DATABASE IF NOT EXISTS ${WP_DB};
	CREATE USER IF NOT EXISTS '${WP_DB_USER}'@'localhost' IDENTIFIED BY '${WP_DB_PASSWORD}';
	GRANT ALL PRIVILEGES ON ${WP_DB}.* TO '${WP_DB_USER}'@'localhost';
	FLUSH PRIVILEGES;
SQL

if [ ! -f "${WP_DIR}/wp-config.php" ]; then
	cp "${WP_DIR}/wp-config-sample.php" "${WP_DIR}/wp-config.php"
	sed -i "s/database_name_here/${WP_DB}/" "${WP_DIR}/wp-config.php"
	sed -i "s/username_here/${WP_DB_USER}/" "${WP_DIR}/wp-config.php"
	sed -i "s/password_here/${WP_DB_PASSWORD}/" "${WP_DIR}/wp-config.php"
	chown www-data:www-data "${WP_DIR}/wp-config.php"
fi

service apache2 start

exec /usr/sbin/sshd -D -e
