# The classic LAMP-stack CMS pattern: Ubuntu + Apache + PHP + MariaDB +
# WordPress, all in one host. Heavier and more stateful than the nginx
# bootstrap pattern: multiple services, a database, and file ownership
# that a real runbook has to get right.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        openssh-server sudo curl ca-certificates wget tar \
        apache2 php php-mysql libapache2-mod-php \
        mariadb-server && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /var/run/sshd

RUN useradd -m -s /bin/bash -G sudo svc-netauto && \
    echo 'svc-netauto:pleiades-lab-2026' | chpasswd && \
    echo 'svc-netauto ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/svc-netauto

COPY keys/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key
COPY keys/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ed25519_key.pub
RUN chmod 600 /etc/ssh/ssh_host_ed25519_key && \
    rm -f /etc/ssh/ssh_host_rsa_key* /etc/ssh/ssh_host_ecdsa_key* && \
    printf 'HostKey /etc/ssh/ssh_host_ed25519_key\nPasswordAuthentication yes\nPubkeyAuthentication yes\n' \
        > /etc/ssh/sshd_config.d/lab.conf

# WordPress itself, downloaded at build time rather than depending on an
# apt package (Ubuntu ships none): a self-contained image needs no
# network access at container start, only at build time.
RUN wget -q https://wordpress.org/latest.tar.gz -O /tmp/wordpress.tar.gz && \
    tar -xzf /tmp/wordpress.tar.gz -C /var/www/html --strip-components=0 && \
    rm -rf /var/www/html/index.html /tmp/wordpress.tar.gz && \
    chown -R www-data:www-data /var/www/html/wordpress

# Point the default vhost at the wordpress/ subdirectory rather than
# /var/www/html itself, and allow .htaccess overrides for permalinks,
# the standard WordPress-on-Apache layout.
RUN sed -i 's#DocumentRoot /var/www/html#DocumentRoot /var/www/html/wordpress#' /etc/apache2/sites-available/000-default.conf && \
    printf '<Directory /var/www/html/wordpress>\n\tAllowOverride All\n</Directory>\n' >> /etc/apache2/sites-available/000-default.conf && \
    a2enmod rewrite

COPY start.sh /usr/local/bin/start.sh
RUN chmod 755 /usr/local/bin/start.sh

EXPOSE 22 80
CMD ["/usr/local/bin/start.sh"]
