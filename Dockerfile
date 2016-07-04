FROM debian:latest
ENV VGNAME lvm-vg
RUN mkdir -p /var/lib/docker-lvm-volume
ADD conf/lvm-volume-plugin.ini /var/lib/docker-lvm-volume/lvm-volume-plugin.ini
ADD lvm-persist /usr/bin/docker-volume-plugin-lvm
RUN chmod +x /usr/bin/docker-volume-plugin-lvm
ADD dumb-init /usr/bin/dumb-init
RUN chmod +x /usr/bin/dumb-init
CMD ["dumb-init", "/usr/bin/docker-volume-plugin-lvm"]