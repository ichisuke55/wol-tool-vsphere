# wol-tool-vsphere
This repository is tool for wake-on-lan vsphere environment.

## Usage
1. define env
```
export AUTH_ID=<YOUR_VSPHERE_USER_ID>
export AUTH_PASS=<YOUR_VSPHERE_USER_PASS>
export SESSION_URL=<YOUR_VSPHERE_FQDN>
export SLACK_SIGNING_SECRET=<BOT_SIGNING_SECRET>
export SLACK_TOKEN=<BOT_TOKEN>
```

2. execute command
```
go run main.go
```

