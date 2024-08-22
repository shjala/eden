#!/bin/bash

DEBIAN_FRONTEND=noninteractive

# install microsoft repository
wget https://packages.microsoft.com/config/ubuntu/22.04/packages-microsoft-prod.deb -O packages-microsoft-prod.deb
sudo dpkg -i packages-microsoft-prod.deb
rm packages-microsoft-prod.deb

# install pre-requisites
sudo apt-get update
sudo apt-get purge -y aziot-identity-service aziot-edge
sudo apt-get install -y moby-engine
sudo apt-get install -y aziot-edge

>config.toml cat <<-EOF
## DPS provisioning with TPM
[provisioning]
source = "dps"
global_endpoint = "https://global.azure-devices-provisioning.net"
id_scope = "$ID_SCOPE"

[provisioning.attestation]
method = "tpm"
registration_id = "$REGISTRATION_ID"
EOF

sudo cp config.toml /etc/aziot/config.toml
sudo iotedge config apply

rm packages-microsoft-prod.deb aziot-edge.deb aziot-identity-service.deb eve-tools.deb config.toml
