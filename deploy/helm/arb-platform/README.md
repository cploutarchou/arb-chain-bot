# arb-platform Helm chart

Deploys arbd (API + engine + collectors in one process), the Next.js
console, and a migrate Job as a pre-install/upgrade hook.

```
deploy/helm/package.sh            # copies migrations into files/, lints
helm upgrade --install arb deploy/helm/arb-platform \
  -n arb --create-namespace --atomic --timeout 10m \
  -f deploy/helm/arb-platform/values-paper-test.yaml \
  --set image.arbd.digest=sha256:... --set image.web.digest=sha256:...
```

- `arbd.mode` accepts RECORD | PAPER | REPLAY only; the template fails
  on anything else. There is no LIVE mode.
- Secrets: `ExternalSecret` -> `<release>-arb-platform-secrets` from a
  KMS-backed `ClusterSecretStore` (`examples/clustersecretstore-kms.yaml`).
- `web` image: the repo has no `web/Dockerfile` yet; the deploy workflow
  builds one from `deploy/docker/web.Dockerfile`.
- Rollback: `helm rollback`; migrations are forward-only in the hook,
  down files are applied by hand per the restore runbook.
