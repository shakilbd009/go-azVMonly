# go-azVMonly

Deploy multiple Azure VMs with full concurrency.

Usage:

./go-azVMonly -Env prod -Tier app  -OS redhat -Disks 40,60 -countTO 1-4 -version 7.4 -CRQ 10234 -AppCode txt -subscriptionID yourSubID

running above, will deploy 4 redhat VMs concurrently. 