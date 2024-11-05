# cert-manager

Deploys [cert-manager](https://github.com/jetstack/cert-manager) to the Kubernetes cluster.

## Requirements

- [ansible-common](https://github.com/metal-stack/ansible-common)

## Variables

| Name                                          | Mandatory | Description                                                      |
| --------------------------------------------- | --------- | ---------------------------------------------------------------- |
| cert_manager_version                          |           | The cert-manager version to deploy                               |
| cert_manager_lets_encrypt_expiry_mail_address |           | A mail address to which Let's Encrypt is gonna send expiry mails |
