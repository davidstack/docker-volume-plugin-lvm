#!/usr/bin/env bash

# install lvm-persist
mkdir -p /var/lib/docker-lvm-volume
cp docker-volume-plugin-lvm /usr/bin/docker-volume-plugin-lvm
chmod +x /usr/bin/docker-volume-plugin-lvm


cp init/docker-volume-plugin-lvm.service /etc/systemd/system/docker-volume-plugin-lvm.service
chmod 750 /etc/systemd/system/docker-volume-plugin-lvm.service

systemctl daemon-reload
systemctl enable docker-volume-plugin-lvm
systemctl start docker-volume-plugin-lvm
systemctl status docker-volume-plugin-lvm
fi





