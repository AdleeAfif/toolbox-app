# Deploying toolbox-app

toolbox-app runs as a single pod on the Raspberry Pi 5 with k3s. The internet reaches it only through the Cloudflare Tunnel that already serves the other hostnames. The app has no database and no env secret.

## Release pipeline

Pushing a tag such as `v0.1.0` runs `.github/workflows/release.yml`:

1. **test** (GitHub-hosted): `go test` and `golangci-lint`.
2. **image** (GitHub-hosted): builds `linux/arm64` and `linux/amd64` and pushes `docker.io/adleeafif/toolbox-app:0.1.0`.
3. **deploy** (self-hosted runner on the Pi, label `rpi5`): sets that tag in `deploy/k8s/kustomization.yaml` (in the job's checkout only), runs `kubectl apply -k deploy/k8s`, and waits for the rollout. If the pod doesn't become ready within 3 minutes, it runs `kubectl rollout undo` and the job fails.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

The committed `newTag` stays at `0.1.0`, so a manual `make deploy` rolls back to that tag. After the pipeline is set up, deploy only by pushing tags. To roll back, push a new tag from an older commit, or run `kubectl -n toolbox-app rollout undo deployment/toolbox-app`.

`GET /healthz` and `GET /readyz` both return `ok`. Nothing outside the process has to be up.

Route stops calls `nominatim.openstreetmap.org`, `router.project-osrm.org`, and `overpass-api.de` over HTTPS when someone searches. The NetworkPolicy only limits ingress, so nothing else is needed. If those services are down, only that tool shows an error.

## One-time setup

1. **Docker Hub token**: reuse the access token already stored for profile-api. In this repo, add it under **Settings > Secrets and variables > Actions** as `DOCKERHUB_TOKEN` (Read & Write). There is no `ENV` secret; this app has no configuration.
2. **Runner on the Pi**: personal-account repos can't share a runner, so register one for this repo. **Settings > Actions > Runners > New self-hosted runner**, Linux / ARM64:

   ```bash
   ./config.sh --url https://github.com/AdleeAfif/toolbox-app --token <token> --labels rpi5 --unattended
   sudo ./svc.sh install && sudo ./svc.sh start
   ```

   `kubectl` is already configured for the `adleerpi5` user at `~/.kube/config`. This repo is private, which is required for a self-hosted runner.
3. **Image pull**: a public Docker Hub repository needs no pull secret. Docker Hub creates `adleeafif/toolbox-app` on the first push. If you make the repository private, create a `dockerhub` pull secret in the `toolbox-app` namespace and add `imagePullSecrets` to the Deployment.
4. **Hostname**: in the existing tunnel's **Public Hostname** tab add:

   | Hostname | Service |
   | --- | --- |
   | `toolbox.adlee.my` | `http://toolbox-app.toolbox-app.svc.cluster.local:8080` |

The tunnel itself stays in the `cloudflared` namespace. Don't add a second connector.

## Manual build

```bash
make image TAG=0.1.0
make deploy
kubectl -n toolbox-app rollout status deployment/toolbox-app
kubectl -n toolbox-app port-forward svc/toolbox-app 8080:8080
```

The pod may use up to 1 GiB. Compress decodes the uploaded image in memory, and an 8192 px PNG is a few hundred megabytes. The Deployment uses `Recreate` so two of those pods never run at once. `/tmp` is an emptyDir because a large multipart form can spill out of memory; the rest of the filesystem is read-only.
