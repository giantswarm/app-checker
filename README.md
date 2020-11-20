[![CircleCI](https://circleci.com/gh/giantswarm/app-checker.svg?&style=shield)](https://circleci.com/gh/giantswarm/app-checker) [![Docker Repository on Quay](https://quay.io/repository/giantswarm/app-checker/status "Docker Repository on Quay")](https://quay.io/repository/giantswarm/app-checker)

# app-checker

This program listens to the GitHub deployment event from [GitHub webhook](https://docs.github.com/en/free-pro-team@latest/developers/webhooks-and-events/webhook-events-and-payloads#deployment) (e.g. `http://app-checker.CLUSTER-API-ENDPOINT`) and deploys apps accordingly. 

If you try to deploy the apps by `opsctl` together with the feature from `app-checker`, please put `--method` flag as `github`. 

Example:
``` 
opsctl deploy -i geckon --jumphost-user jhbok gatekeeper-app@testing-br --method github 
```

# For the new installations


1. Please check `app-checker-unique` ingress object in `giantswarm` namespace and copy the first `hosts` name from the result.
```
kubectl -n giantswarm get ingress app-checker-unique -o yaml
...
  tls:
  - hosts:
    - app-checker-unique.g8s.geckon.gridscale.kvm.gigantic.io
    secretName: app-checker-unique-ingress
```

2. Create the new GitHub webhook with the `hosts` value in [GiantSwarm organization's setting](https://github.com/organizations/giantswarm/settings/hooks). Only `deployment` event would be sufficient. 

3. Please add a secret token from our draughtsman! 
