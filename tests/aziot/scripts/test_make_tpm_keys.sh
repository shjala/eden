#!/bin/bash

DEBIAN_FRONTEND=noninteractive

sudo apt-get update
sudo apt-get install -y tpm2-tools

# make tpm accessible, 777 is fine for testing
sudo chmod 777 /dev/tpm*
# Create the endorsement key (EK) and storage root key (SRK)
sudo tpm2_evictcontrol -c 0x81010001
sudo tpm2_evictcontrol -c 0x81000001
sudo tpm2_createek -c 0x81010001 -G rsa -u ek.pub
sudo tpm2_createprimary -Q -C o -c srk.ctx > /dev/null
sudo tpm2_evictcontrol -c srk.ctx 0x81000001 > /dev/null
sudo tpm2_flushcontext -t > /dev/null
